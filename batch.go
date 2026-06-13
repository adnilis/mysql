package plugins

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
)

// BatchInsert 批量插入记录
// 使用 VALUES (),(),() 语法，单次插入多条记录
// models: 要插入的模型列表
// batchSize: 每批次的记录数（建议 100-500）
// 返回所有插入记录的 ID 列表
func (p *MySQLPlugin) BatchInsert(ctx context.Context, models []IModel, batchSize int) ([]int64, error) {
	db, err := p.getDB()
	if err != nil {
		return nil, err
	}

	if len(models) == 0 {
		return []int64{}, nil
	}

	if batchSize <= 0 {
		batchSize = 100 // 默认批次大小
	}

	allIDs := make([]int64, 0, len(models))

	// 分批处理
	for i := 0; i < len(models); i += batchSize {
		end := i + batchSize
		if end > len(models) {
			end = len(models)
		}
		batch := models[i:end]

		ids, err := p.batchInsertSingle(ctx, db, batch)
		if err != nil {
			return allIDs, err
		}
		allIDs = append(allIDs, ids...)
	}

	return allIDs, nil
}

// batchInsertSingle 单批次批量插入（内部方法）
func (p *MySQLPlugin) batchInsertSingle(ctx context.Context, db *sqlx.DB, batch []IModel) ([]int64, error) {
	if len(batch) == 0 {
		return []int64{}, nil
	}

	scanner := newFieldScanner(batch[0])
	if scanner.meta == nil || len(scanner.meta.columns) == 0 {
		return nil, fmt.Errorf("no columns to insert for batch")
	}

	columnCount := len(scanner.meta.columns)
	valueArgs := make([]interface{}, 0, len(batch)*columnCount)

	// 预构建单行占位符，避免每行重复分配
	rowPlaceholder := "(" + strings.TrimRight(strings.Repeat("?,", columnCount), ",") + ")"

	for _, model := range batch {
		modelScanner := newFieldScanner(model)
		fields := modelScanner.dbFields()

		for _, f := range fields {
			valueArgs = append(valueArgs, f.value)
		}
	}

	// 构建完整的 INSERT 语句
	var builder strings.Builder
	builder.Grow(32 + len(scanner.table) + len(scanner.meta.columns)*8 + len(batch)*len(rowPlaceholder))
	builder.WriteString("INSERT INTO ")
	builder.WriteString(scanner.table)
	builder.WriteString(" (")
	for i, col := range scanner.meta.columns {
		if i > 0 {
			builder.WriteString(", ")
		}
		builder.WriteString(col)
	}
	builder.WriteString(") VALUES ")
	for i := range batch {
		if i > 0 {
			builder.WriteString(", ")
		}
		builder.WriteString(rowPlaceholder)
	}

	query := builder.String()

	// 执行插入
	start := time.Now()
	result, err := db.ExecContext(ctx, query, valueArgs...)
	duration := time.Since(start)

	if err != nil {
		p.queryLogger.LogError(ctx, query, duration, err, valueArgs...)
		return nil, wrapMySQLError(scanner.table, "batch insert", err)
	}

	// 获取插入的 ID
	lastID, err := result.LastInsertId()
	if err != nil {
		p.queryLogger.LogError(ctx, query, duration, err, valueArgs...)
		return nil, wrapMySQLError(scanner.table, "batch insert", fmt.Errorf("failed to get last insert id: %w", err))
	}

	// 生成所有插入的 ID
	// 注意：此处仅在 innodb_autoinc_lock_mode=1（InnoDB 默认）且本次事务/语句内
	// 没有并发 INSERT 时，`lastID + i` 才与实际分配的 ID 一一对应。
	// 在 innodb_autoinc_lock_mode=2（高并发模式）下 LAST_INSERT_ID 返回的是
	// 第一个分配的 ID，但分配可能不连续；调用方若依赖确切 ID 应在表上使用
	// 显式主键（如 UUID）而非自增列。
	ids := make([]int64, len(batch))
	for i := range ids {
		ids[i] = lastID + int64(i)
	}

	p.logQ(ctx, "BATCH_INSERT", query, duration, int64(len(batch)), valueArgs...)
	return ids, nil
}
