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
		t.plugin.queryLogger.LogError(context.Background(), "COMMIT", duration, err)
		return fmt.Errorf("commit failed: %w", err)
	}

	t.plugin.queryLogger.LogTransaction(context.Background(), "COMMIT", duration)
	return nil
}

// Rollback 回滚事务
func (t *MySQLTransaction) Rollback() error {
	start := time.Now()
	err := t.tx.Rollback()
	duration := time.Since(start)

	if err != nil && err != sql.ErrTxDone {
		t.plugin.queryLogger.LogError(context.Background(), "ROLLBACK", duration, err)
		return fmt.Errorf("rollback failed: %w", err)
	}

	t.plugin.queryLogger.LogTransaction(context.Background(), "ROLLBACK", duration)
	return nil
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
	t.plugin.queryLogger.LogOperation(ctx, "EXEC", "", duration, rowsAffected, query, args...)
	return result, nil
}
