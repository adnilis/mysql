package plugins

import (
	"context"
	"fmt"
	"sync"

	"github.com/adnilis/wma"
	"github.com/jmoiron/sqlx"
)

// MySQLPlugin 是 WMA 框架的 MySQL 插件实现
type MySQLPlugin struct {
	mu          sync.RWMutex       // 读写锁，保证并发安全
	name        string             // 插件名称
	config      MySQLPluginConfig  // 插件配置
	app         *wma.App           // WMA 应用实例
	db          *sqlx.DB           // sqlx 数据库连接
	stopCh      chan struct{}       // 停止通道
	stopOnce    sync.Once          // 确保停止操作只执行一次
	state       mysqlPluginState   // 插件状态
	queryLogger *QueryLogger       // 查询日志记录器
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
func (p *MySQLPlugin) DB() *sqlx.DB {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.db
}

// Ping 检查 MySQL 连接是否正常
func (p *MySQLPlugin) Ping(ctx context.Context) error {
	p.mu.RLock()
	db := p.db
	p.mu.RUnlock()

	if db == nil {
		return fmt.Errorf("mysql not initialized")
	}
	return db.PingContext(ctx)
}

// Stats 返回插件统计信息
func (p *MySQLPlugin) Stats() MySQLStats {
	p.mu.RLock()
	defer p.mu.RUnlock()

	stats := MySQLStats{
		Name:         p.name,
		Addr:         p.config.Addr,
		DBName:       p.config.DBName,
		State:        p.state.String(),
		PoolSize:     p.config.PoolSize,
		MinIdleConns: p.config.MinIdleConns,
		MaxIdleConns: p.config.MaxIdleConns,
	}

	// 获取数据库连接池统计信息
	if p.db != nil {
		dbStats := p.db.Stats()
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
