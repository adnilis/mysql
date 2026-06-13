package plugins

import (
	"context"
	"fmt"
	"sync"

	"github.com/adnilis/logger"
	"github.com/jmoiron/sqlx"
)

// mysqlLoggerConfig 适配器：把 MySQLPluginConfig 暴露为 QueryLoggerConfig 接口
type mysqlLoggerConfig struct {
	enabled       bool
	slowThreshold int64
}

func (c *mysqlLoggerConfig) EnableQueryLog() bool { return c.enabled }
func (c *mysqlLoggerConfig) SlowThreshold() int64 { return c.slowThreshold }

// mysqlQueryResultPool QueryResult 对象池
// 使用 sync.Pool 复用 QueryResult 对象，减少内存分配
var mysqlQueryResultPool = sync.Pool{
	New: func() interface{} {
		return &MySQLQueryResult{
			joins:   make([]joinClause, 0, 4),   // 预分配 4 个 JOIN
			wheres:  make([]whereClause, 0, 8),  // 预分配 8 个 WHERE
			groups:  make([]string, 0, 2),       // 预分配 2 个 GROUP
			havings: make([]havingClause, 0, 2), // 预分配 2 个 HAVING
			orders:  make([]string, 0, 4),       // 预分配 4 个 ORDER
			args:    make([]interface{}, 0, 16), // 预分配 16 个参数
		}
	},
}

// acquireMySQLQueryResult 从对象池获取 QueryResult
func acquireMySQLQueryResult() *MySQLQueryResult {
	qr := mysqlQueryResultPool.Get().(*MySQLQueryResult)
	qr.reset()
	return qr
}

// releaseMySQLQueryResult 释放 QueryResult 到对象池
func releaseMySQLQueryResult(qr *MySQLQueryResult) {
	if qr != nil {
		qr.reset()
		mysqlQueryResultPool.Put(qr)
	}
}

// reset 重置 QueryResult 状态（用于对象池复用）
func (qr *MySQLQueryResult) reset() {
	qr.plugin = nil
	qr.ctx = nil
	qr.query = ""
	qr.args = qr.args[:0]
	qr.joins = qr.joins[:0]
	qr.wheres = qr.wheres[:0]
	qr.groups = qr.groups[:0]
	qr.havings = qr.havings[:0]
	qr.orders = qr.orders[:0]
	qr.limit = 0
	qr.offset = 0
	qr.err = nil
	qr.preQuery = ""
	qr.preArgs = nil
	qr.dirty = false
}

// Start 启动插件，建立数据库连接
func (p *MySQLPlugin) Start(ctx context.Context) error {
	// 构建 DSN 连接字符串
	dsn := buildDSN(&p.config)

	// 连接数据库（Start 由调用方保证单次执行，无需持锁）
	db, err := sqlx.Connect("mysql", dsn)
	if err != nil {
		return fmt.Errorf("mysql connect failed: %w", err)
	}

	// 配置连接池参数
	db.SetMaxOpenConns(p.config.PoolSize)
	db.SetMaxIdleConns(p.config.MaxIdleConns)
	db.SetConnMaxLifetime(p.config.MaxLifetime)
	db.SetConnMaxIdleTime(p.config.MaxIdleTime)

	// Ping 验证连接
	if err := db.Ping(); err != nil {
		db.Close()
		return fmt.Errorf("mysql ping failed: %w", err)
	}

	// 原子发布 db 句柄
	p.db.Store(db)

	// 保护低频写入字段（state / queryLogger）
	p.mu.Lock()
	loggerConfig := &mysqlLoggerConfig{
		enabled:       p.config.EnableQueryLog,
		slowThreshold: p.config.SlowThreshold,
	}
	p.queryLogger = NewQueryLogger(loggerConfig)
	p.state = mysqlPluginStateRunning
	p.mu.Unlock()

	// 监听上下文取消信号
	// 通过 done 通道允许 Stop() 在 ctx 取消前主动结束此 goroutine，避免泄漏
	go func() {
		select {
		case <-ctx.Done():
		case <-p.done:
		}
		p.stopOnce.Do(func() {
			close(p.stopCh)
		})
	}()

	logger.Info("[MySQL] connected to %s/%s", p.config.Addr, p.config.DBName)
	return nil
}

// Stop 停止插件，关闭数据库连接
func (p *MySQLPlugin) Stop(ctx context.Context) error {
	// 初始化 stopCh / done 如果尚未初始化（防御性编程）
	if p.stopCh == nil {
		p.stopCh = make(chan struct{})
	}
	if p.done == nil {
		p.done = make(chan struct{})
	}

	p.stopOnce.Do(func() {
		close(p.done)
		close(p.stopCh)
	})

	// 原子取出 db 并置空
	db := p.db.Swap(nil)
	if db != nil {
		if err := db.Close(); err != nil {
			return fmt.Errorf("mysql close failed: %w", err)
		}
	}

	p.mu.Lock()
	p.state = mysqlPluginStateStopped
	p.mu.Unlock()

	logger.Info("[MySQL] disconnected from %s/%s", p.config.Addr, p.config.DBName)
	return nil
}
