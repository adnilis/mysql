package plugins

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
)

// MySQLTransaction MySQL 事务包装器
//
// 生命周期管理:
//   - Begin() 后应显式调用 Commit() 或 Rollback()
//   - 推荐用 defer tx.Close() 安全网:若未 Commit/Rollback,Close 会自动回滚
//   - Close 内部用 sync.Once 保证幂等,可多次调用
type MySQLTransaction struct {
	plugin    *MySQLPlugin // 所属插件
	tx        *sqlx.Tx     // 底层事务
	closeOnce sync.Once    // 保证 Close/自动回滚只执行一次
	committed bool         // Commit 成功标志
	rolled    bool         // Rollback 成功标志
}

// Commit 提交事务
// ctx 用于日志记录的上下文传递（事务底层的 sqlx.Tx.Commit 本身不接受 ctx）
//
// 重复 Commit 会返回错误(底层 sql.ErrTxDone 透传)
func (t *MySQLTransaction) Commit(ctx context.Context) error {
	start := time.Now()
	err := t.tx.Commit()
	duration := time.Since(start)

	if err != nil {
		t.plugin.queryLogger.LogError(ctx, "COMMIT", duration, err)
		return fmt.Errorf("commit failed: %w", err)
	}

	t.committed = true
	t.plugin.queryLogger.LogTransaction(ctx, "COMMIT", duration)
	return nil
}

// Rollback 回滚事务
// ctx 用于日志记录的上下文传递。重复回滚返回 nil（容忍 sql.ErrTxDone）
func (t *MySQLTransaction) Rollback(ctx context.Context) error {
	start := time.Now()
	err := t.tx.Rollback()
	duration := time.Since(start)

	if err != nil && err != sql.ErrTxDone {
		t.plugin.queryLogger.LogError(ctx, "ROLLBACK", duration, err)
		return fmt.Errorf("rollback failed: %w", err)
	}

	t.rolled = true
	t.plugin.queryLogger.LogTransaction(ctx, "ROLLBACK", duration)
	return nil
}

// Close 自动安全网:若未 Commit/Rollback,自动回滚释放资源
//
// 推荐用法:
//
//	tx, _ := db.Begin()
//	defer tx.Close()  // 即使 panic 或早 return,事务也会回滚
//	// ... work ...
//	tx.Commit()       // 提交成功后再 Close 不会有副作用
//
// 多次 Close 安全(sync.Once 保证)。Close 不会覆盖 Commit 成功的结果。
func (t *MySQLTransaction) Close() error {
	var closeErr error
	t.closeOnce.Do(func() {
		// 已 Commit 或 Rollback,无需操作
		if t.committed || t.rolled {
			return
		}
		// 未提交/未回滚,执行回滚
		if err := t.tx.Rollback(); err != nil && err != sql.ErrTxDone {
			closeErr = err
			return
		}
		t.rolled = true
	})
	return closeErr
}

// Get 在事务中执行单行查询
func (t *MySQLTransaction) Get(ctx context.Context, dest interface{}, query string, args ...interface{}) error {
	start := time.Now()
	err := t.tx.GetContext(ctx, dest, query, args...)
	duration := time.Since(start)

	if err != nil {
		t.plugin.queryLogger.LogError(ctx, query, duration, err, args...)
		return err
	}

	t.plugin.queryLogger.LogQuery(ctx, query, duration, 1, args...)
	return nil
}

// Select 在事务中执行多行查询
func (t *MySQLTransaction) Select(ctx context.Context, dest interface{}, query string, args ...interface{}) error {
	start := time.Now()
	err := t.tx.SelectContext(ctx, dest, query, args...)
	duration := time.Since(start)

	if err != nil {
		t.plugin.queryLogger.LogError(ctx, query, duration, err, args...)
		return err
	}

	t.plugin.queryLogger.LogQuery(ctx, query, duration, 0, args...)
	return nil
}

// Exec 在事务中执行 SQL 语句
func (t *MySQLTransaction) Exec(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	start := time.Now()
	result, err := t.tx.ExecContext(ctx, query, args...)
	duration := time.Since(start)

	if err != nil {
		t.plugin.queryLogger.LogError(ctx, query, duration, err, args...)
		return nil, err
	}

	rowsAffected, _ := result.RowsAffected()
	t.plugin.logQ(ctx, "EXEC", query, duration, rowsAffected, args...)
	return result, nil
}
