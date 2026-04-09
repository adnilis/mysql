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
	p.mu.RLock()
	db := p.db
	p.mu.RUnlock()

	if db == nil {
		return nil, ErrMySQLNotEnabled
	}

	if len(models) == 0 {
		return []int64{}, nil
	}

	if batchSize <= 0 {
		batchSize = 100 // 默认批次大小
	}

	var allIDs []int64

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

	// 构建 VALUES 部分
	var valueStrings []string
	valueArgs := make([]interface{}, 0, len(batch)*len(scanner.meta.columns))

	for _, model := range batch {
		modelScanner := newFieldScanner(model)
		fields := modelScanner.dbFields()

		var valuePlaceholders []string
		for _, f := range fields {
			valuePlaceholders = append(valuePlaceholders, "?")
			valueArgs = append(valueArgs, f.value)
		}
		valueStrings = append(valueStrings, fmt.Sprintf("(%s)", strings.Join(valuePlaceholders, ", ")))
	}

	// 构建完整的 INSERT 语句
	var builder strings.Builder
	builder.Grow(32 + len(scanner.table) + len(scanner.meta.columns)*8 + len(valueStrings)*10)
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
	for i, vs := range valueStrings {
		if i > 0 {
			builder.WriteString(", ")
		}
		builder.WriteString(vs)
	}

	query := builder.String()

	// 执行插入
	start := time.Now()
	result, err := db.ExecContext(ctx, query, valueArgs...)
	duration := time.Since(start)

	if err != nil {
		_ = duration
		return nil, wrapMySQLError(scanner.table, "batch insert", err)
	}

	// 获取插入的 ID
	lastID, err := result.LastInsertId()
	if err != nil {
		_ = duration
		return nil, wrapMySQLError(scanner.table, "batch insert", fmt.Errorf("failed to get last insert id: %w", err))
	}

	// 生成所有插入的 ID（假设自增 ID 连续）
	ids := make([]int64, len(batch))
	for i := range ids {
		ids[i] = lastID + int64(i)
	}

	_ = duration
	return ids, nil
}
