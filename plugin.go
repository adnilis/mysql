package plugins

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/adnilis/wma"
	"github.com/jmoiron/sqlx"
)

// MySQLPlugin 是 WMA 框架的 MySQL 插件实现
type MySQLPlugin struct {
	mu          sync.RWMutex            // 读写锁，保护 state 等低频写入字段
	name        string                  // 插件名称
	config      MySQLPluginConfig       // 插件配置
	app         *wma.App                // WMA 应用实例
	db          atomic.Pointer[sqlx.DB] // sqlx 数据库连接（无锁读取，热路径优化）
	stopCh      chan struct{}           // 停止通道（外部观察）
	done        chan struct{}           // 触发监听 goroutine 退出
	stopOnce    sync.Once               // 确保停止操作只执行一次
	state       mysqlPluginState        // 插件状态
	queryLogger *QueryLogger            // 查询日志记录器
}

// mysqlPluginState 插件状态枚举
type mysqlPluginState int

const (
	mysqlPluginStateReady    mysqlPluginState = iota // 就绪状态
	mysqlPluginStateRunning                          // 运行状态
	mysqlPluginStateStopping                         // 停止中
	mysqlPluginStateStopped                          // 已停止
)

// NewMySQLPlugin 创建 MySQL 插件实例
// name: 插件名称
// cfg: 插件配置，传 nil 使用默认配置
func NewMySQLPlugin(name string, cfg *MySQLPluginConfig) *MySQLPlugin {
	config := normalizeMySQLPluginConfig(cfg)
	return &MySQLPlugin{
		name:   name,
		config: config,
		stopCh: make(chan struct{}),
		done:   make(chan struct{}),
		state:  mysqlPluginStateReady,
	}
}

// Type 返回插件类型
func (p *MySQLPlugin) Type() wma.PluginType {
	return wma.PluginTypeCustom
}

// Name 返回插件名称
func (p *MySQLPlugin) Name() string {
	return p.name
}

// Init 初始化插件
func (p *MySQLPlugin) Init(app *wma.App) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// 验证配置
	if err := p.config.Validate(); err != nil {
		return err
	}

	p.app = app
	return nil
}

// DB 返回底层 sqlx.DB 数据库连接
// 使用 atomic.Load 实现无锁读取，适合高频调用
func (p *MySQLPlugin) DB() *sqlx.DB {
	return p.db.Load()
}

// getDB 安全获取数据库连接句柄；插件未启动或已停止时返回 ErrMySQLNotEnabled
// 内部使用，统一替代 12+ 处 nil check 样板
// 使用 atomic.Load 实现无锁读取，热路径上避免 RWMutex 竞争
func (p *MySQLPlugin) getDB() (*sqlx.DB, error) {
	db := p.db.Load()
	if db == nil {
		return nil, ErrMySQLNotEnabled
	}
	return db, nil
}

// Ping 检查 MySQL 连接是否正常
func (p *MySQLPlugin) Ping(ctx context.Context) error {
	db := p.db.Load()
	if db == nil {
		return fmt.Errorf("mysql not initialized")
	}
	return db.PingContext(ctx)
}

// Stats 返回插件统计信息
func (p *MySQLPlugin) Stats() MySQLStats {
	// 仅在读取 state 时短暂持锁
	p.mu.RLock()
	state := p.state
	p.mu.RUnlock()

	stats := MySQLStats{
		Name:         p.name,
		Addr:         p.config.Addr,
		DBName:       p.config.DBName,
		State:        state.String(),
		PoolSize:     p.config.PoolSize,
		MinIdleConns: p.config.MinIdleConns,
		MaxIdleConns: p.config.MaxIdleConns,
	}

	// 获取数据库连接池统计信息（无锁）
	if db := p.db.Load(); db != nil {
		dbStats := db.Stats()
		stats.OpenConnections = dbStats.OpenConnections
		stats.InUse = dbStats.InUse
		stats.Idle = dbStats.Idle
		stats.WaitCount = dbStats.WaitCount
		stats.WaitDuration = dbStats.WaitDuration
		stats.MaxIdleClosed = dbStats.MaxIdleClosed
		stats.MaxLifetimeClosed = dbStats.MaxLifetimeClosed
	}

	return stats
}

// String 返回插件状态的字符串表示
func (s mysqlPluginState) String() string {
	switch s {
	case mysqlPluginStateReady:
		return "ready"
	case mysqlPluginStateRunning:
		return "running"
	case mysqlPluginStateStopping:
		return "stopping"
	case mysqlPluginStateStopped:
		return "stopped"
	default:
		return "unknown"
	}
}
