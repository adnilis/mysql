package plugins

import (
	"context"
	"errors"
	"math"
	"math/rand"
	"strings"
	"time"

	"github.com/adnilis/logger"
	"github.com/jmoiron/sqlx"
)

// MySQL error codes for retryable cases (R09)
//
//   - 1213: ER_LOCK_DEADLOCK - transaction was rolled back due to deadlock
//   - 1205: ER_LOCK_WAIT_TIMEOUT - lock wait timeout exceeded
//
// 参考: https://dev.mysql.com/doc/mysql-errors/8.0/en/server-error-reference.html
const (
	mysqlErrDeadlock     = 1213
	mysqlErrLockWaitTime = 1205
)

// RetryPolicy 重试策略(R09)
type RetryPolicy struct {
	MaxAttempts    int           // 最大尝试次数(含首次);<=0 视为 1(不重试)
	InitialBackoff time.Duration // 首次重试前等待;<0 视为 50ms
	MaxBackoff     time.Duration // 退避上限;<0 视为 2s
	Multiplier     float64       // 退避乘数;<1 视为 2.0
	Jitter         bool          // 是否加随机抖动(防止雪崩)
}

// DefaultRetryPolicy 默认重试策略:5 次,50ms → 100ms → 200ms → 400ms → 800ms(带抖动)
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxAttempts:    5,
		InitialBackoff: 50 * time.Millisecond,
		MaxBackoff:     2 * time.Second,
		Multiplier:     2.0,
		Jitter:         true,
	}
}

// NoRetryPolicy 单次执行(不重试)
func NoRetryPolicy() RetryPolicy {
	return RetryPolicy{MaxAttempts: 1}
}

// isRetryableMySQLError 判断是否是可重试的 MySQL 错误(R09)
//
// 启发式:1) errors.As 还原为 *mysql.MySQLError(go-sql-driver 错误)
//  2. Number == 1213 / 1205 → retryable
//
// 注:go-sql-driver 错误类型不在本插件依赖范围(为保持轻量);
// 改为弱类型检查 — 错误 message 含 "Deadlock" 或 "Lock wait timeout" 也算
func isRetryableMySQLError(err error) bool {
	if err == nil {
		return false
	}
	// 类型断言: 部分 go-sql-driver 版本错误实现了 Number() 方法
	type numberedError interface{ Number() uint16 }
	var ne numberedError
	if errors.As(err, &ne) {
		num := ne.Number()
		return num == mysqlErrDeadlock || num == mysqlErrLockWaitTime
	}
	// fallback: 错误 message 启发式
	msg := err.Error()
	return strings.Contains(msg, "Deadlock") ||
		strings.Contains(msg, "deadlock") ||
		strings.Contains(msg, "Lock wait timeout") ||
		strings.Contains(msg, "lock wait timeout")
}

// WithRetry 在 fn 返回 MySQL 可重试错误时自动重试(R09)
//
// 用法:
//
//		err := WithRetry(ctx, DefaultRetryPolicy(), func(ctx context.Context) error {
//		    _, err := plugin.Exec(ctx, "UPDATE orders SET stock = stock - 1 WHERE id = ?", id)
//		    return err
//	})
//
// 行为:
//   - 每次 attempt 间隔 backoff *= Multiplier(2.0 by default),上限 MaxBackoff
//   - 启用 Jitter 时,在 backoff 上加 ±25% 随机(防雷击群)
//   - 非重试错误立即返回(0 重试)
//   - ctx 取消时立即返回 ctx.Err()
//
// 不修改 fn 的入参;若 fn 内部已经做了 ctx 监听,重试退避不会与 fn 内部超时叠加
func WithRetry(ctx context.Context, policy RetryPolicy, fn func(context.Context) error) error {
	if policy.MaxAttempts <= 0 {
		policy = NoRetryPolicy()
	}
	if policy.InitialBackoff < 0 {
		policy.InitialBackoff = 50 * time.Millisecond
	}
	if policy.MaxBackoff < 0 {
		policy.MaxBackoff = 2 * time.Second
	}
	if policy.Multiplier < 1 {
		policy.Multiplier = 2.0
	}

	backoff := policy.InitialBackoff
	var lastErr error
	for attempt := 1; attempt <= policy.MaxAttempts; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err := fn(ctx)
		if err == nil {
			return nil
		}
		if !isRetryableMySQLError(err) {
			return err
		}
		lastErr = err
		if attempt == policy.MaxAttempts {
			break
		}
		// 退避
		sleep := backoff
		if policy.Jitter {
			jitter := time.Duration(float64(sleep) * (0.75 + 0.5*rand.Float64()))
			sleep = jitter
		}
		logger.Warn("[mysql retry] attempt %d/%d failed: %v; retrying in %v",
			attempt, policy.MaxAttempts, err, sleep)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(sleep):
		}
		// backoff *= multiplier, 上限 MaxBackoff
		backoff = time.Duration(math.Min(
			float64(backoff)*policy.Multiplier,
			float64(policy.MaxBackoff),
		))
	}
	return lastErr
}

// WithRetryTx 在事务中重试执行 fn,每次失败自动回滚+重开新事务(R09)
//
// 适用于"事务内多个 Exec"的场景:死锁时回滚重试,而不是部分提交。
//
// 用法:
//
//	err := plugin.WithRetryTx(ctx, DefaultRetryPolicy(), func(ctx context.Context, tx *MySQLTransaction) error {
//	    if _, err := tx.Exec(ctx, ...); err != nil { return err }
//	    return tx.Exec(ctx, ...)
//	})
//
// 注意:fn 内部应避免副作用外泄(死锁回滚时 fn 已执行的 Exec 会被回滚)
func (p *MySQLPlugin) WithRetryTx(ctx context.Context, policy RetryPolicy, fn func(context.Context, *MySQLTransaction) error) error {
	return WithRetry(ctx, policy, func(ctx context.Context) error {
		tx, err := p.Begin()
		if err != nil {
			return err
		}
		// 注意:fn 内不返回 error 时,我们在这里 Commit;返回 error 时 Rollback
		// 但 WithRetry 期望 fn 永远成功或返回 retryable 错误;
		// 我们用 named return value + defer 模式
		var fnErr error
		func() {
			defer func() {
				if r := recover(); r != nil {
					_ = tx.Rollback(context.Background())
					panic(r)
				}
			}()
			fnErr = fn(ctx, tx)
		}()
		if fnErr != nil {
			_ = tx.Rollback(context.Background())
			return fnErr
		}
		return tx.Commit(context.Background())
	})
}

// SQL 不可用时报警:在 retry 路径中使用的连接查询占位
// 保留 sqlx 引用防止 goimports 误删
var _ = sqlx.DB{}
