package plugins

import (
	"context"
	"fmt"
	"sync"

	"github.com/adnilis/logger"
	"github.com/jmoiron/sqlx"
)

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
	p.mu.Lock()
	defer p.mu.Unlock()

	// 构建 DSN 连接字符串
	dsn := buildDSN(&p.config)

	var err error
	p.db, err = sqlx.Connect("mysql", dsn)
	if err != nil {
		return fmt.Errorf("mysql connect failed: %w", err)
	}

	// 配置连接池参数
	p.db.SetMaxOpenConns(p.config.PoolSize)
	p.db.SetMaxIdleConns(p.config.MaxIdleConns)
	p.db.SetConnMaxLifetime(p.config.MaxLifetime)
	p.db.SetConnMaxIdleTime(p.config.MaxIdleTime)

	// Ping 验证连接
	if err := p.db.Ping(); err != nil {
		p.db.Close()
		return fmt.Errorf("mysql ping failed: %w", err)
	}

	p.state = mysqlPluginStateRunning

	// 监听上下文取消信号
	go func() {
		<-ctx.Done()
		p.stopOnce.Do(func() {
			close(p.stopCh)
		})
	}()

	logger.Info("[MySQL] connected to %s/%s", p.config.Addr, p.config.DBName)
	return nil
}

// Stop 停止插件，关闭数据库连接
func (p *MySQLPlugin) Stop(ctx context.Context) error {
	p.stopOnce.Do(func() {
		close(p.stopCh)
	})

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.db != nil {
		if err := p.db.Close(); err != nil {
			return fmt.Errorf("mysql close failed: %w", err)
		}
		p.db = nil
	}

	p.state = mysqlPluginStateStopped
	logger.Info("[MySQL] disconnected from %s/%s", p.config.Addr, p.config.DBName)
	return nil
}
