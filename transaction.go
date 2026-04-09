package plugins

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
)

// MySQLTransaction MySQL 事务包装器
type MySQLTransaction struct {
	plugin *MySQLPlugin // 所属插件
	tx     *sqlx.Tx     // 底层事务
}

// Commit 提交事务
func (t *MySQLTransaction) Commit() error {
	start := time.Now()
	err := t.tx.Commit()
	duration := time.Since(start)

	if err != nil {
		return fmt.Errorf("commit failed: %w", err)
	}

	_ = duration
	return nil
}

// Rollback 回滚事务
func (t *MySQLTransaction) Rollback() error {
	start := time.Now()
	err := t.tx.Rollback()
	duration := time.Since(start)

	if err != nil && err != sql.ErrTxDone {
		_ = duration
		return fmt.Errorf("rollback failed: %w", err)
	}

	_ = duration
	return nil
}

// Get 在事务中执行单行查询
func (t *MySQLTransaction) Get(ctx context.Context, dest interface{}, query string, args ...interface{}) error {
	return t.tx.GetContext(ctx, dest, query, args...)
}

// Select 在事务中执行多行查询
func (t *MySQLTransaction) Select(ctx context.Context, dest interface{}, query string, args ...interface{}) error {
	return t.tx.SelectContext(ctx, dest, query, args...)
}

// Exec 在事务中执行 SQL 语句
func (t *MySQLTransaction) Exec(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	return t.tx.ExecContext(ctx, query, args...)
}
