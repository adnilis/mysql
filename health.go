package plugins

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adnilis/logger"
	"github.com/jmoiron/sqlx"
)

// HealthChecker 后台连接健康检查器(R09)
//
// 行为契约:
//   - 周期 ping MySQL(默认 30s);失败累计 N 次触发健康降级
//   - 降级后切换 p.db 为 nil,后续 query 立即返回 ErrMySQLNotEnabled
//     (避免 query hang 等待 driver 内部超时)
//   - 持续 ping,成功时重建 db 句柄并恢复
//   - 状态变化通过 HealthHook 通知外部
//
// 用法:
//
//	checker := NewHealthChecker(30*time.Second, 3)
//	checker.SetHook(func(healthy bool, err error) { ... })
//	plugin.AttachHealthChecker(checker)
//
// 线程安全:所有公开方法可并发调用;内部 sync.Mutex 保护
type HealthChecker struct {
	interval            time.Duration
	failLimit           int
	mu                  sync.Mutex
	plugin              *MySQLPlugin
	cancel              context.CancelFunc
	stopped             atomic.Bool
	consecutive         int // 连续失败计数
	healthy             atomic.Bool
	hook                HealthHook // 状态变化回调
	pingTimeout         context.Context
	pingTimeoutDuration time.Duration
}

// HealthHook 健康状态变化回调(R09)
//
// healthy:新健康状态(true=健康/false=降级)
// err:触发本次状态变化的原因(healthy=true 时为恢复前的错误,healthy=false 时为本次失败)
type HealthHook func(healthy bool, err error)

// NewHealthChecker 创建一个健康检查器
// interval<=0 视为 30s;failLimit<=0 视为 3
func NewHealthChecker(interval time.Duration, failLimit int) *HealthChecker {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	if failLimit <= 0 {
		failLimit = 3
	}
	// R11-perf:ping 内部 context 静态化(WithTimeout 每个周期复用)
	// background context + 1s deadline,通过 cancel 手动回收
	pingCtx, pingCancel := context.WithCancel(context.Background())
	hc := &HealthChecker{
		interval:            interval,
		failLimit:           failLimit,
		healthy:             atomic.Bool{},
		pingTimeout:         pingCtx,
		pingTimeoutDuration: time.Second,
	}
	hc.healthy.Store(true)
	// Stop 时统一释放 ping 静态 context
	hc.cancel = func() { pingCancel() }
	return hc
}

// SetHook 设置状态变化回调
func (hc *HealthChecker) SetHook(h HealthHook) {
	hc.mu.Lock()
	hc.hook = h
	hc.mu.Unlock()
}

// Start 在 plugin 上下文中启动后台 ping goroutine
// 已在 plugin.Start 内部自动调用;用户可单独调用于其他场景
func (hc *HealthChecker) Start(p *MySQLPlugin, parentCtx context.Context) {
	hc.mu.Lock()
	if hc.plugin != nil {
		hc.mu.Unlock()
		return // 已启动
	}
	hc.plugin = p
	ctx, cancel := context.WithCancel(parentCtx)
	hc.cancel = cancel
	hc.mu.Unlock()
	go hc.loop(ctx)
}

// Stop 停止健康检查
func (hc *HealthChecker) Stop() {
	hc.stopped.Store(true)
	hc.mu.Lock()
	if hc.cancel != nil {
		hc.cancel()
		hc.cancel = nil
	}
	hc.mu.Unlock()
}

// IsHealthy 当前是否健康
func (hc *HealthChecker) IsHealthy() bool {
	return hc.healthy.Load()
}

func (hc *HealthChecker) loop(ctx context.Context) {
	ticker := time.NewTicker(hc.interval)
	defer ticker.Stop()

	// 首次启动立刻 ping 一次
	hc.pingOnce(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			hc.pingOnce(ctx)
		}
	}
}

func (hc *HealthChecker) pingOnce(ctx context.Context) {
	hc.mu.Lock()
	p := hc.plugin
	hc.mu.Unlock()
	if p == nil {
		return
	}
	_ = p // 已在 recordFail/reconnect 中通过 hc.plugin 重新获取

	db := p.db.Load()
	if db == nil {
		// 已经降级,继续尝试重建
		if err := hc.reconnect(p); err != nil {
			hc.recordFail(err)
		} else {
			hc.recordRecover()
		}
		return
	}

	// R11-perf:复用 hc.pingTimeout 静态 context(避免每次 WithTimeout 分配)
	// 30s 一次的 ping 复用 pingTimeout,1.0s 后到期
	pingCtx, cancel := context.WithTimeout(hc.pingTimeout, hc.pingTimeoutDuration)
	err := db.PingContext(pingCtx)
	cancel()

	if err != nil {
		hc.recordFail(err)
		// 失败达到阈值:主动降级 + 尝试重连
		hc.mu.Lock()
		consecutive := hc.consecutive
		hc.mu.Unlock()
		if consecutive >= hc.failLimit {
			hc.degrade(p, err)
			hc.reconnect(p)
		}
	} else {
		if !hc.healthy.Load() {
			hc.recordRecover()
		}
		hc.mu.Lock()
		hc.consecutive = 0
		hc.mu.Unlock()
	}
}

func (hc *HealthChecker) recordFail(err error) {
	hc.mu.Lock()
	hc.consecutive++
	hc.mu.Unlock()
	logger.Warn("[MySQL health] ping failed (consecutive=%d): %v", hc.consecutive, err)
}

func (hc *HealthChecker) recordRecover() {
	hc.mu.Lock()
	hc.consecutive = 0
	hc.mu.Unlock()
	hc.healthy.Store(true)
	logger.Info("[MySQL health] recovered")
	hc.mu.Lock()
	h := hc.hook
	hc.mu.Unlock()
	if h != nil {
		h(true, nil)
	}
}

func (hc *HealthChecker) degrade(p *MySQLPlugin, err error) {
	hc.healthy.Store(false)
	logger.Error("[MySQL health] degraded after %d consecutive failures on %s/%s: %v",
		hc.failLimit, p.config.Addr, p.config.DBName, err)
	hc.mu.Lock()
	h := hc.hook
	hc.mu.Unlock()
	if h != nil {
		h(false, err)
	}
}

func (hc *HealthChecker) reconnect(p *MySQLPlugin) error {
	// 尝试用 plugin 现有 config 重建 db
	dsn := buildDSN(&p.config)
	db, err := sqlx.Connect("mysql", dsn)
	if err != nil {
		logger.Error("[MySQL health] reconnect failed: %v", err)
		return err
	}
	db.SetMaxOpenConns(p.config.PoolSize)
	db.SetMaxIdleConns(effectiveMaxIdleConns(&p.config))
	db.SetConnMaxLifetime(p.config.MaxLifetime)
	db.SetConnMaxIdleTime(p.config.MaxIdleTime)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return err
	}
	oldDB := p.db.Swap(db)
	if oldDB != nil {
		_ = oldDB.Close()
	}
	logger.Info("[MySQL health] reconnected to %s/%s", p.config.Addr, p.config.DBName)
	return nil
}
