package plugins

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// RetryBudget 全局失败计数 + 熔断器(R13)
//
// 用法:
//
//	budget := NewRetryBudget(50, 1*time.Minute) // 1 分钟内 50 次失败触发熔断
//	if !budget.Allow() {
//	    return errors.New("retry budget exhausted")
//	}
//	// ... 走 WithRetry ...
//	budget.RecordSuccess()  // 成功后减少计数
//	budget.RecordFailure()  // 失败后增加计数
//
// 行为契约:
//   - 计数 < threshold:Allow=true,正常重试
//   - 计数 >= threshold:Allow=false(熔断),持续 cooldownDuration
//   - cooldown 后计数自动归零,Allow=true
//
// 用于:WithRetry 包裹前先 Allow,避免在下游已故障时盲目重试雪崩
type RetryBudget struct {
	threshold     int           // 熔断阈值
	cooldown      time.Duration // 冷却时间
	mu            sync.Mutex
	failures      int
	circuitOpenAt atomic.Int64 // 熔断开始时间(纳秒);0 表示未熔断
	successive    int32        // 连续成功次数(用于衰减失败计数)
}

// NewRetryBudget 创建一个熔断器(R13)
func NewRetryBudget(threshold int, cooldown time.Duration) *RetryBudget {
	if threshold <= 0 {
		threshold = 50
	}
	if cooldown <= 0 {
		cooldown = time.Minute
	}
	return &RetryBudget{
		threshold: threshold,
		cooldown:  cooldown,
	}
}

// Allow 检查是否允许重试(R13)
//
// 返回 false 表示熔断器已开,调用方应放弃并返回错误
func (b *RetryBudget) Allow() bool {
	if b == nil {
		return true
	}
	openAt := b.circuitOpenAt.Load()
	if openAt == 0 {
		return true
	}
	// 熔断中,检查 cooldown 是否到期
	elapsed := time.Since(time.Unix(0, openAt))
	if elapsed >= b.cooldown {
		// Cooldown 到期,半开(允许一次试探)
		// 试探成功后由 RecordSuccess 重置;失败则重新开启
		return true
	}
	return false
}

// RecordSuccess 记录成功,衰减失败计数(R13)
//
// 若熔断器处于 cooldown 试探通过状态,自动重置熔断标志
func (b *RetryBudget) RecordSuccess() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	// 半开状态下试探成功 → 关闭熔断器
	openAt := b.circuitOpenAt.Load()
	if openAt != 0 {
		b.circuitOpenAt.Store(0)
		b.failures = 0
		b.successive = 0
		return
	}
	// 正常成功 → 增加连续成功计数
	n := atomic.AddInt32(&b.successive, 1)
	if n >= 5 {
		// 连续 5 次成功 → 衰减失败计数
		b.failures = b.failures / 2
		atomic.StoreInt32(&b.successive, 0)
	}
}

// RecordFailure 记录失败,增加失败计数,达阈值触发熔断(R13)
func (b *RetryBudget) RecordFailure() {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.failures++
	atomic.StoreInt32(&b.successive, 0)
	if b.failures >= b.threshold {
		b.circuitOpenAt.Store(time.Now().UnixNano())
	}
	b.mu.Unlock()
}

// Failures 当前失败计数(R13)
func (b *RetryBudget) Failures() int {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.failures
}

// IsOpen 熔断器是否处于打开状态(R13)
func (b *RetryBudget) IsOpen() bool {
	if b == nil {
		return false
	}
	return b.circuitOpenAt.Load() != 0
}

// withRetryBudget 全局默认熔断器(R13)
var globalRetryBudget = NewRetryBudget(50, time.Minute)

// WithRetryBudget 全局包装:在 WithRetry 前检查熔断器(R13)
//
// 用户可替换 globalRetryBudget 调高/调低阈值
func WithRetryBudget(ctx context.Context, policy RetryPolicy, fn func(context.Context) error) error {
	if !globalRetryBudget.Allow() {
		return errors.New("mysql retry budget exhausted")
	}
	err := WithRetry(ctx, policy, fn)
	if err != nil {
		globalRetryBudget.RecordFailure()
		return err
	}
	globalRetryBudget.RecordSuccess()
	return nil
}
