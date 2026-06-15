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

// BatchInsertOnConflict IModel 版批量 upsert(R09)
//
// 与 BatchInsert 的区别:每批追加 ON DUPLICATE KEY UPDATE 子句,冲突时按
// conflictCols 触发并更新其他列。所有 models 必须实现相同 IModel 类型以确保
// SQL 模板一致(取第一批第一个的 scanner)。
//
// 参数:
//   - models      : 要插入/更新的模型列表
//   - batchSize   : 每批的行数(0 = 默认 200)
//   - conflictCols: 触发 ON DUPLICATE KEY 的列(可空;为空时使用 db:"<col>,pk" 标记)
//
// 返回:每批 FirstInsertID 的列表(1=插入 / 2=更新 / 0=无变化;语义由 MySQL 返回)
func (p *MySQLPlugin) BatchInsertOnConflict(ctx context.Context, models []IModel, batchSize int, conflictCols ...string) ([]int64, error) {
	if len(models) == 0 {
		return []int64{}, nil
	}
	if batchSize <= 0 {
		batchSize = 200
	}

	db, err := p.getDB()
	if err != nil {
		return nil, err
	}

	// 用第一批第一个 model 解析表结构(所有 model 类型必须相同)
	scanner := newFieldScanner(models[0])
	if scanner.meta == nil || len(scanner.meta.columns) == 0 {
		return nil, wrapMySQLError(scanner.table, "batch insert on conflict", fmt.Errorf("no columns"))
	}

	// 解析 conflict 列集合
	pk := scanner.meta.pkColumn
	if pk == "" {
		pk = "id"
	}
	if len(conflictCols) == 0 {
		conflictCols = []string{pk}
	}
	conflictSet := make(map[string]bool, len(conflictCols))
	for _, c := range conflictCols {
		if !isValidIdentifier(c) {
			return nil, wrapMySQLError(scanner.table, "batch insert on conflict", fmt.Errorf("invalid conflict col: %s", c))
		}
		conflictSet[c] = true
	}

	// 预拼 update 子句(只在 conflict 列之外)
	var updateClause strings.Builder
	updateClause.WriteString(" ON DUPLICATE KEY UPDATE ")
	first := true
	for _, col := range scanner.meta.columns {
		if conflictSet[col] {
			continue
		}
		if !first {
			updateClause.WriteString(", ")
		}
		first = false
		updateClause.WriteString(col)
		updateClause.WriteString(" = VALUES(")
		updateClause.WriteString(col)
		updateClause.WriteString(")")
	}
	updateSQL := updateClause.String()

	// 预拼单行占位符
	rowPlaceholder := "(" + strings.TrimRight(strings.Repeat("?,", len(scanner.meta.columns)), ",") + ")"

	allIDs := make([]int64, 0, len(models))
	for start := 0; start < len(models); start += batchSize {
		end := start + batchSize
		if end > len(models) {
			end = len(models)
		}
		batch := models[start:end]

		// 构造单批 SQL
		var sb strings.Builder
		sb.Grow(32 + len(scanner.table) + len(scanner.meta.columns)*8 + len(batch)*(len(rowPlaceholder)+2) + len(updateSQL))
		sb.WriteString("INSERT INTO ")
		sb.WriteString(scanner.table)
		sb.WriteString(" (")
		for i, col := range scanner.meta.columns {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(col)
		}
		sb.WriteString(") VALUES ")
		for i := range batch {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(rowPlaceholder)
		}
		sb.WriteString(updateSQL)
		query := sb.String()

		// 展开所有 model 字段值到一维 args
		var values []any
		for _, m := range batch {
			fs := newFieldScanner(m)
			fsVals := fs.dbFields()
			if values == nil {
				values = make([]any, 0, len(fsVals)*len(batch))
			}
			for _, fv := range fsVals {
				values = append(values, fv.value)
			}
		}
		if values == nil {
			values = []any{}
		}

		t := time.Now()
		result, err := db.ExecContext(ctx, query, values...)
		duration := time.Since(t)
		if err != nil {
			p.queryLogger.LogError(ctx, query, duration, err, values...)
			return allIDs, wrapMySQLError(scanner.table, "batch insert on conflict", err)
		}
		// 此函数不返回每行 ID(因 ON DUPLICATE 不连续),只累计 RowsAffected
		affected, _ := result.RowsAffected()
		allIDs = append(allIDs, affected)
		p.logQ(ctx, "BATCH_INSERT_ON_CONFLICT", query, duration, affected, values...)
	}
	return allIDs, nil
}

// BatchExec 通用多行 INSERT 助手(接受 [][]any 而非 []IModel)
//
// 适用场景:mail DAO 等无 IModel 的多行写入,或动态列名/动态行数据的批量插入。
// 替代调用方手写 "INSERT ... VALUES (?,?,?),(?,?,?)" 样板。
//
// 参数:
//   - table   : 表名(已通过 isValidIdentifier 校验)
//   - columns : 列名切片
//   - rows    : 每行的参数切片,每行长度必须 == len(columns)
//   - chunkSize: 每条 INSERT 语句的最大行数(0 → 默认 200)
//
// 返回:总受影响行数(累加各 chunk 的 RowsAffected)
//
// 限制:
//   - 单条 INSERT 体积受 MySQL max_allowed_packet 约束(默认 16MB),
//     chunkSize=200 × 10 列 × 平均 50 字节 ≈ 100KB,远低于上限
//   - 不在事务内,失败可能部分写入(若需原子性,调用方应包在 RunInTransaction 中)
func (p *MySQLPlugin) BatchExec(ctx context.Context, table string, columns []string, rows [][]any, chunkSize int) (int64, error) {
	if !isValidIdentifier(table) {
		return 0, wrapMySQLError(table, "batch exec", ErrInvalidModel)
	}
	if len(columns) == 0 {
		return 0, wrapMySQLError(table, "batch exec", fmt.Errorf("columns must not be empty"))
	}
	if len(rows) == 0 {
		return 0, nil
	}
	if chunkSize <= 0 {
		chunkSize = 200
	}

	db, err := p.getDB()
	if err != nil {
		return 0, err
	}

	// 预拼单行占位符:(?,?,?,...)
	rowPlaceholder := "(" + strings.TrimRight(strings.Repeat("?,", len(columns)), ",") + ")"

	var totalAffected int64
	for start := 0; start < len(rows); start += chunkSize {
		end := start + chunkSize
		if end > len(rows) {
			end = len(rows)
		}
		chunk := rows[start:end]

		// 校验每行列数
		for i, row := range chunk {
			if len(row) != len(columns) {
				return totalAffected, wrapMySQLError(table, "batch exec",
					fmt.Errorf("row %d: expected %d columns, got %d", i, len(columns), len(row)))
			}
		}

		// 构建 INSERT INTO t (c1,c2) VALUES (?,?),(?,?),(?,?)
		var sb strings.Builder
		sb.Grow(32 + len(table) + len(columns)*8 + len(chunk)*(len(rowPlaceholder)+2))
		sb.WriteString("INSERT INTO ")
		sb.WriteString(table)
		sb.WriteString(" (")
		for i, col := range columns {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(col)
		}
		sb.WriteString(") VALUES ")
		for i := range chunk {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(rowPlaceholder)
		}
		query := sb.String()

		// 展开所有参数到一维切片
		args := make([]any, 0, len(chunk)*len(columns))
		for _, row := range chunk {
			args = append(args, row...)
		}

		// 执行
		t := time.Now()
		result, err := db.ExecContext(ctx, query, args...)
		duration := time.Since(t)

		if err != nil {
			p.queryLogger.LogError(ctx, query, duration, err, args...)
			return totalAffected, wrapMySQLError(table, "batch exec", err)
		}
		affected, _ := result.RowsAffected()
		totalAffected += affected
		p.logQ(ctx, "BATCH_EXEC", query, duration, affected, args...)
	}

	return totalAffected, nil
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
