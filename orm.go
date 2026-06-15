package plugins

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"time"
)

// logQ 内部助手：组装 "[OP] query" 前缀，统一通过 LogQuery 落盘
// 调用方传入 op 形如 "INSERT" / "UPDATE" / "DELETE" / "EXEC" / "COUNT" / "EXISTS"
// R06:同步更新内存级指标(query / affected)
// R13:同时记录到 query duration 直方图
func (p *MySQLPlugin) logQ(ctx context.Context, op, query string, duration time.Duration, rows int64, args ...any) {
	if p.queryLogger != nil {
		p.queryLogger.LogQuery(ctx, "["+op+"] "+query, duration, rows, args...)
	}
	// 内存级指标:始终更新(无论 queryLogger 是否启用,这样 Stats() 才有数据)
	switch op {
	case "SELECT", "FIND", "FIRST", "TAKE", "PLUCK", "DISTINCT", "GET":
		p.metricRowsRead.Add(rows)
	default:
		p.metricRowsAffected.Add(rows)
	}
	p.metricQueryTotal.Add(1)
	if ql := p.queryLogger; ql != nil && ql.slowEnabled && ql.config.SlowThreshold() > 0 && duration.Milliseconds() >= ql.config.SlowThreshold() {
		p.metricQuerySlow.Add(1)
	}
	// R13:每次 query 记入直方图(无论 op)
	queryDuration.observe(duration)
}

// Begin 开启事务
// 返回 Transaction 对象，用于执行事务操作
func (p *MySQLPlugin) Begin() (*MySQLTransaction, error) {
	db, err := p.getDB()
	if err != nil {
		return nil, err
	}

	tx, err := db.Beginx()
	if err != nil {
		return nil, fmt.Errorf("begin transaction failed: %w", err)
	}

	return &MySQLTransaction{
		plugin: p,
		tx:     tx,
	}, nil
}

// Query 构建链式查询
// 返回 QueryResult 对象，支持链式调用
func (p *MySQLPlugin) Query(ctx context.Context, query string, args ...any) *MySQLQueryResult {
	qr := acquireMySQLQueryResult()
	qr.plugin = p
	qr.ctx = ctx
	qr.query = query
	qr.args = args
	return qr
}

// Table 指定要查询的表名
// 用法：orm.Table("users").Where("age > ?", 18).Find(&users)
func (p *MySQLPlugin) Table(tableName string) *MySQLQueryResult {
	qr := acquireMySQLQueryResult()
	qr.plugin = p
	qr.ctx = context.Background()

	// 验证表名安全性
	if !isValidIdentifier(tableName) {
		qr.err = ErrInvalidModel
		return qr
	}

	qr.query = "SELECT * FROM " + tableName
	return qr
}

// Model 根据模型自动推断表名
// 用法：orm.Model(&User{}).Where("age > ?", 18).Find(&users)
func (p *MySQLPlugin) Model(model IModel) *MySQLQueryResult {
	qr := acquireMySQLQueryResult()
	if model == nil {
		qr.err = ErrInvalidModel
		return qr
	}

	tableName := model.TableName()
	// 验证表名安全性
	if !isValidIdentifier(tableName) {
		qr.err = ErrInvalidModel
		return qr
	}

	qr.plugin = p
	qr.ctx = context.Background()
	qr.query = "SELECT * FROM " + tableName
	return qr
}

// Insert 插入单条记录
// 返回插入记录的 ID（自增主键）
func (p *MySQLPlugin) Insert(ctx context.Context, model IModel) (int64, error) {
	db, err := p.getDB()
	if err != nil {
		return 0, err
	}

	scanner := newFieldScanner(model)
	query, values, err := scanner.buildInsertSQL()
	if err != nil {
		return 0, wrapMySQLError(scanner.table, "insert", err)
	}
	// values 来自 valsPool,ExecContext 后归还
	defer valsPool.Put(&values)

	start := time.Now()
	result, err := db.ExecContext(ctx, query, values...)
	duration := time.Since(start)

	if err != nil {
		p.queryLogger.LogError(ctx, query, duration, err, values...)
		return 0, wrapMySQLError(scanner.table, "insert", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		p.queryLogger.LogError(ctx, query, duration, err, values...)
		return 0, wrapMySQLError(scanner.table, "insert", fmt.Errorf("failed to get last insert id: %w", err))
	}

	p.logQ(ctx, "INSERT", query, duration, 1, values...)
	return id, nil
}

// Update 更新记录（带 WHERE 条件）
// where: WHERE 条件字符串（不包含 WHERE 关键字）
// args: WHERE 条件的参数
func (p *MySQLPlugin) Update(ctx context.Context, model IModel, where string, args ...any) (int64, error) {
	db, err := p.getDB()
	if err != nil {
		return 0, err
	}

	scanner := newFieldScanner(model)
	query, values, err := scanner.buildUpdateSQL()
	if err != nil {
		return 0, wrapMySQLError(scanner.table, "update", err)
	}
	defer valsPool.Put(&values)

	query += " WHERE " + where
	// 复制以避免修改调用方传入的 args 底层数组
	combined := make([]any, 0, len(values)+len(args))
	combined = append(combined, values...)
	combined = append(combined, args...)

	start := time.Now()
	result, err := db.ExecContext(ctx, query, combined...)
	duration := time.Since(start)

	if err != nil {
		p.queryLogger.LogError(ctx, query, duration, err, combined...)
		return 0, wrapMySQLError(scanner.table, "update", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		p.queryLogger.LogError(ctx, query, duration, err, combined...)
		return 0, wrapMySQLError(scanner.table, "update", fmt.Errorf("failed to get rows affected: %w", err))
	}

	p.logQ(ctx, "UPDATE", query, duration, rowsAffected, combined...)
	return rowsAffected, nil
}

// Delete 删除记录（带 WHERE 条件）
// where: WHERE 条件字符串（不包含 WHERE 关键字）
// args: WHERE 条件的参数
func (p *MySQLPlugin) Delete(ctx context.Context, model IModel, where string, args ...any) (int64, error) {
	db, err := p.getDB()
	if err != nil {
		return 0, err
	}

	scanner := newFieldScanner(model)
	query, values, err := scanner.buildDeleteSQL(where, args...)
	if err != nil {
		return 0, wrapMySQLError(scanner.table, "delete", err)
	}

	start := time.Now()
	result, err := db.ExecContext(ctx, query, values...)
	duration := time.Since(start)

	if err != nil {
		p.queryLogger.LogError(ctx, query, duration, err, values...)
		return 0, wrapMySQLError(scanner.table, "delete", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		p.queryLogger.LogError(ctx, query, duration, err, values...)
		return 0, wrapMySQLError(scanner.table, "delete", fmt.Errorf("failed to get rows affected: %w", err))
	}

	p.logQ(ctx, "DELETE", query, duration, rowsAffected, values...)
	return rowsAffected, nil
}

// GetByID 根据主键获取模型
// 如果记录不存在，返回 ErrModelNotFound
func (p *MySQLPlugin) GetByID(ctx context.Context, model IModel, id any) error {
	db, err := p.getDB()
	if err != nil {
		return err
	}

	scanner := newFieldScanner(model)
	query := scanner.buildSelectByIDSQL()
	if query == "" {
		return wrapMySQLError(scanner.table, "select", ErrInvalidModel)
	}

	start := time.Now()
	err = db.GetContext(ctx, model, query, id)
	duration := time.Since(start)

	if err == sql.ErrNoRows {
		p.queryLogger.LogError(ctx, query, duration, ErrModelNotFound, id)
		return wrapMySQLError(scanner.table, "select", ErrModelNotFound)
	}

	if err != nil {
		p.queryLogger.LogError(ctx, query, duration, err, id)
		return wrapMySQLError(scanner.table, "select", err)
	}

	p.logQ(ctx, "SELECT", query, duration, 1, id)
	return nil
}

// UpdateByID 根据主键更新模型
// 返回影响的行数
func (p *MySQLPlugin) UpdateByID(ctx context.Context, model IModel, id any) (int64, error) {
	db, err := p.getDB()
	if err != nil {
		return 0, err
	}

	scanner := newFieldScanner(model)
	query, values, err := scanner.buildUpdateByIDSQL(id)
	if err != nil {
		return 0, wrapMySQLError(scanner.table, "update", err)
	}
	defer valsPool.Put(&values)

	start := time.Now()
	result, err := db.ExecContext(ctx, query, values...)
	duration := time.Since(start)

	if err != nil {
		p.queryLogger.LogError(ctx, query, duration, err, values...)
		return 0, wrapMySQLError(scanner.table, "update", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		p.queryLogger.LogError(ctx, query, duration, err, values...)
		return 0, wrapMySQLError(scanner.table, "update", fmt.Errorf("failed to get rows affected: %w", err))
	}

	p.logQ(ctx, "UPDATE", query, duration, rowsAffected, values...)
	return rowsAffected, nil
}

// DeleteByID 根据主键删除模型
// 注意：主键列名取自 db:"id" 标签，不再硬编码为 "id"
// 返回影响的行数
func (p *MySQLPlugin) DeleteByID(ctx context.Context, model IModel, id any) (int64, error) {
	db, err := p.getDB()
	if err != nil {
		return 0, err
	}

	scanner := newFieldScanner(model)
	query, values, err := scanner.buildDeleteByIDSQL(id)
	if err != nil {
		return 0, wrapMySQLError(scanner.table, "delete", err)
	}

	start := time.Now()
	result, err := db.ExecContext(ctx, query, values...)
	duration := time.Since(start)

	if err != nil {
		p.queryLogger.LogError(ctx, query, duration, err, values...)
		return 0, wrapMySQLError(scanner.table, "delete", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		p.queryLogger.LogError(ctx, query, duration, err, values...)
		return 0, wrapMySQLError(scanner.table, "delete", fmt.Errorf("failed to get rows affected: %w", err))
	}

	p.logQ(ctx, "DELETE", query, duration, rowsAffected, values...)
	return rowsAffected, nil
}

// Select 执行 SELECT 查询（到切片）
// dest: 目标切片（如 &[]User{}）
func (p *MySQLPlugin) Select(ctx context.Context, dest any, query string, args ...any) error {
	if dest == nil {
		return ErrInvalidModel
	}

	db, err := p.getDB()
	if err != nil {
		return err
	}

	start := time.Now()
	err = db.SelectContext(ctx, dest, query, args...)
	duration := time.Since(start)

	if err != nil {
		p.queryLogger.LogError(ctx, query, duration, err, args...)
		return wrapMySQLError("", "select", err)
	}

	resultLen := 0
	if v := reflect.ValueOf(dest); v.Kind() == reflect.Pointer && v.Elem().Kind() == reflect.Slice {
		resultLen = v.Elem().Len()
	}
	p.logQ(ctx, "SELECT", query, duration, int64(resultLen), args...)
	return nil
}

// Get 执行获取单条记录的查询
// dest: 目标结构体（如 &User{}）
// 如果记录不存在，返回 ErrModelNotFound
func (p *MySQLPlugin) Get(ctx context.Context, dest any, query string, args ...any) error {
	if dest == nil {
		return ErrInvalidModel
	}

	db, err := p.getDB()
	if err != nil {
		return err
	}

	start := time.Now()
	err = db.GetContext(ctx, dest, query, args...)
	duration := time.Since(start)

	if err == sql.ErrNoRows {
		p.queryLogger.LogError(ctx, query, duration, ErrModelNotFound, args...)
		return wrapMySQLError("", "get", ErrModelNotFound)
	}

	if err != nil {
		p.queryLogger.LogError(ctx, query, duration, err, args...)
		return wrapMySQLError("", "get", err)
	}

	p.logQ(ctx, "SELECT", query, duration, 1, args...)
	return nil
}

// Exec 执行 SQL 语句（无返回值）
// 用于执行 DDL 语句或不需要返回结果的 DML 语句
// 返回影响的行数
func (p *MySQLPlugin) Exec(ctx context.Context, query string, args ...any) (int64, error) {
	db, err := p.getDB()
	if err != nil {
		return 0, err
	}

	start := time.Now()
	result, err := db.ExecContext(ctx, query, args...)
	duration := time.Since(start)

	if err != nil {
		p.queryLogger.LogError(ctx, query, duration, err, args...)
		return 0, wrapMySQLError("", "exec", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		p.queryLogger.LogError(ctx, query, duration, err, args...)
		return 0, wrapMySQLError("", "exec", fmt.Errorf("failed to get rows affected: %w", err))
	}

	p.logQ(ctx, "EXEC", query, duration, rowsAffected, args...)
	return rowsAffected, nil
}

// ExecReturningID 执行 INSERT 语句并返回插入的 ID
// 用于执行 INSERT 语句后需要获取自增主键的场景
// 返回插入记录的 ID
func (p *MySQLPlugin) ExecReturningID(ctx context.Context, query string, args ...any) (int64, error) {
	db, err := p.getDB()
	if err != nil {
		return 0, err
	}

	start := time.Now()
	result, err := db.ExecContext(ctx, query, args...)
	duration := time.Since(start)

	if err != nil {
		p.queryLogger.LogError(ctx, query, duration, err, args...)
		return 0, wrapMySQLError("", "exec", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		p.queryLogger.LogError(ctx, query, duration, err, args...)
		return 0, wrapMySQLError("", "exec", fmt.Errorf("failed to get last insert id: %w", err))
	}

	p.logQ(ctx, "INSERT", query, duration, 1, args...)
	return id, nil
}

// Count 计数
// table: 表名（必须是合法 SQL 标识符，否则返回 ErrInvalidModel）
// where: WHERE 条件（可选，为空则统计全表）
// args: WHERE 条件的参数
func (p *MySQLPlugin) Count(ctx context.Context, table string, where string, args ...any) (int64, error) {
	if !isValidIdentifier(table) {
		return 0, ErrInvalidModel
	}

	db, err := p.getDB()
	if err != nil {
		return 0, err
	}

	query := "SELECT COUNT(*) as count FROM " + table
	if where != "" {
		query += " WHERE " + where
	}

	var result struct {
		Count int64 `db:"count"`
	}

	start := time.Now()
	err = db.GetContext(ctx, &result, query, args...)
	duration := time.Since(start)

	if err != nil {
		p.queryLogger.LogError(ctx, query, duration, err, args...)
		return 0, wrapMySQLError(table, "count", err)
	}

	p.logQ(ctx, "COUNT", query, duration, 1, args...)
	return result.Count, nil
}

// Exists 检查记录是否存在
// table: 表名
// where: WHERE 条件
// args: WHERE 条件的参数
func (p *MySQLPlugin) Exists(ctx context.Context, table string, where string, args ...any) (bool, error) {
	count, err := p.Count(ctx, table, where, args...)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// Create GORM 风格创建记录
// 用法：orm.Create(ctx, &User{Name: "John", Age: 25})
func (p *MySQLPlugin) Create(ctx context.Context, model IModel) error {
	_, err := p.Insert(ctx, model)
	return err
}

// Save GORM 风格保存记录：如果主键非零则更新，否则插入
// 用法：orm.Save(ctx, &User{ID: 1, Name: "Updated"})
// 注意：主键由 scanner 通过 db:"id" 标签解析；模型必须带该标签否则返回 ErrInvalidModel
func (p *MySQLPlugin) Save(ctx context.Context, model IModel) error {
	if model == nil {
		return ErrInvalidModel
	}

	val := reflect.ValueOf(model)
	if val.Kind() == reflect.Pointer {
		if val.IsNil() {
			return ErrInvalidModel
		}
	} else {
		return ErrInvalidModel
	}

	scanner := newFieldScanner(model)
	_, idVal, ok := scanner.primaryKey()
	if !ok {
		return ErrInvalidModel
	}

	// 通过反射判断主键值是否为零值
	if !reflect.ValueOf(idVal).IsZero() {
		_, err := p.UpdateByID(ctx, model, idVal)
		return err
	}

	_, err := p.Insert(ctx, model)
	return err
}

// First GORM 风格获取第一条记录
// 用法：var user User; orm.First(ctx, &user, 1)
// 主键列名通过 IModel + db:"id" 标签解析；找不到则回退为 "id"
func (p *MySQLPlugin) First(ctx context.Context, dest any, id any) error {
	tableName := getTableNameFromDest(dest)
	if tableName == "" {
		return wrapMySQLError("", "first", fmt.Errorf("cannot infer table name from destination"))
	}

	// 尝试从 IModel 提取主键列名
	pkCol := "id"
	if model, ok := dest.(IModel); ok {
		scanner := newFieldScanner(model)
		if col, _, has := scanner.primaryKey(); has {
			pkCol = col
		}
	}

	query := fmt.Sprintf("SELECT * FROM %s WHERE %s = ? LIMIT 1", tableName, pkCol)
	return p.Get(ctx, dest, query, id)
}

// Find GORM 风格查找多条记录
// 用法：var users []User; orm.Find(ctx, &users, "age > ?", 18)
func (p *MySQLPlugin) Find(ctx context.Context, dest any, query string, args ...any) error {
	return p.Select(ctx, dest, query, args...)
}

// BulkUpdate 单条 SQL 批量更新(R08):
//
//	UPDATE t SET col = CASE pk
//	  WHEN ? THEN ?
//	  WHEN ? THEN ?
//	  ...
//	END WHERE pk IN (?, ?, ...)
//
// 适用场景:批量改一列不同值(库存批量扣减、批量状态修改等),
// 替代 per-row UPDATE 循环 — 单条 SQL 单次网络往返,N 倍延迟缩减为 1。
//
// 参数:
//   - table  : 表名
//   - pkCol  : 主键列名(必须有索引)
//   - ids    : 主键值列表(必须与 values 等长)
//   - col    : 要更新的列名
//   - values : 每个主键对应的新值(必须与 ids 等长)
//
// 限制:MySQL CASE WHEN 默认 16 层限制;values 列表超过 16 时,自动分片
// 多条 UPDATE(单条 IN 列表不超 1000 个值,避免 max_allowed_packet)。
//
// 返回:总受影响行数(累加各分片)
func (p *MySQLPlugin) BulkUpdate(ctx context.Context, table, pkCol string, ids []any, col string, values []any) (int64, error) {
	if !isValidIdentifier(table) {
		return 0, wrapMySQLError(table, "bulk update", ErrInvalidModel)
	}
	if !isValidIdentifier(pkCol) {
		return 0, wrapMySQLError(table, "bulk update", fmt.Errorf("invalid pk column: %s", pkCol))
	}
	if !isValidIdentifier(col) {
		return 0, wrapMySQLError(table, "bulk update", fmt.Errorf("invalid update column: %s", col))
	}
	if len(ids) != len(values) {
		return 0, wrapMySQLError(table, "bulk update",
			fmt.Errorf("ids and values length mismatch: %d vs %d", len(ids), len(values)))
	}
	if len(ids) == 0 {
		return 0, nil
	}
	if col == pkCol {
		return 0, wrapMySQLError(table, "bulk update", fmt.Errorf("update column equals pk column"))
	}

	db, err := p.getDB()
	if err != nil {
		return 0, err
	}

	// 自动分片:每片 CASE WHEN 不超 16 个,IN 不超 256 个
	const caseChunk = 16
	const inChunk = 256
	var totalAffected int64

	for start := 0; start < len(ids); start += caseChunk {
		caseEnd := start + caseChunk
		if caseEnd > len(ids) {
			caseEnd = len(ids)
		}
		// 把这一片拆成 IN 子片
		for inStart := start; inStart < caseEnd; inStart += inChunk {
			inEnd := inStart + inChunk
			if inEnd > caseEnd {
				inEnd = caseEnd
			}
			chunkIDs := ids[inStart:inEnd]
			chunkVals := values[inStart:inEnd]

			// 构造 SQL
			var sb strings.Builder
			sb.Grow(64 + len(table)*2 + len(pkCol) + len(col) + len(chunkIDs)*32)
			sb.WriteString("UPDATE ")
			sb.WriteString(table)
			sb.WriteString(" SET ")
			sb.WriteString(col)
			sb.WriteString(" = CASE ")
			sb.WriteString(pkCol)
			for range chunkIDs {
				sb.WriteString(" WHEN ? THEN ?")
			}
			sb.WriteString(" END WHERE ")
			sb.WriteString(pkCol)
			sb.WriteString(" IN (")
			for i := range chunkIDs {
				if i > 0 {
					sb.WriteString(",")
				}
				sb.WriteString("?")
			}
			sb.WriteString(")")
			query := sb.String()

			// 参数顺序:VALUES(p1,v1,p2,v2,...) + IN(p1,p2,...)
			args := make([]any, 0, len(chunkIDs)*3)
			for i := range chunkIDs {
				args = append(args, chunkIDs[i], chunkVals[i])
			}
			args = append(args, chunkIDs...)

			t := time.Now()
			result, err := db.ExecContext(ctx, query, args...)
			duration := time.Since(t)
			if err != nil {
				p.queryLogger.LogError(ctx, query, duration, err, args...)
				return totalAffected, wrapMySQLError(table, "bulk update", err)
			}
			affected, _ := result.RowsAffected()
			totalAffected += affected
			p.logQ(ctx, "BULK_UPDATE", query, duration, affected, args...)
		}
	}
	return totalAffected, nil
}

// SaveOnConflict IModel 版 upsert(MySQL INSERT ... ON DUPLICATE KEY UPDATE)
//
// 适用场景:计数器/用户元数据/配置 等需要"有则更新、无则插入"的单条写入,
// 替代 DAO 中先 SELECT 检查再 INSERT/UPDATE 的两段式样板。
//
// 参数:
//   - model       : 实现 IModel 的 Go 指针
//   - conflictCols: 触发 ON DUPLICATE KEY 的列(必须有 UNIQUE/PRIMARY 约束);
//     为空时使用 db:"<col>,pk" 标记的列;无标记则用 "id" 兜底
//
// 返回:sql.Result.RowsAffected(1=插入/2=更新/0=无变化)
func (p *MySQLPlugin) SaveOnConflict(ctx context.Context, model IModel, conflictCols ...string) (int64, error) {
	if model == nil {
		return 0, ErrInvalidModel
	}
	scanner := newFieldScanner(model)
	if scanner.table == "" {
		return 0, wrapMySQLError("", "save on conflict", ErrInvalidModel)
	}
	if !isValidIdentifier(scanner.table) {
		return 0, wrapMySQLError(scanner.table, "save on conflict", ErrInvalidModel)
	}
	if scanner.meta == nil || len(scanner.meta.columns) == 0 {
		return 0, wrapMySQLError(scanner.table, "save on conflict", fmt.Errorf("no columns for model"))
	}

	// 确定 conflict 列:参数优先,否则用 meta.pkColumn,否则 "id"
	pk := scanner.meta.pkColumn
	if pk == "" {
		pk = "id"
	}
	if len(conflictCols) == 0 {
		conflictCols = []string{pk}
	}
	// 校验
	conflictSet := make(map[string]bool, len(conflictCols))
	for _, c := range conflictCols {
		if !isValidIdentifier(c) {
			return 0, wrapMySQLError(scanner.table, "save on conflict", fmt.Errorf("invalid conflict column: %s", c))
		}
		conflictSet[c] = true
	}

	// 构造 INSERT INTO t (cols) VALUES (?,?,?) ON DUPLICATE KEY UPDATE col = VALUES(col), ...
	// update 列 = 所有列 - conflictCols
	var sb strings.Builder
	sb.Grow(64 + len(scanner.table) + len(scanner.meta.columns)*12 + len(conflictCols)*8)
	sb.WriteString(scanner.meta.insertSQL)
	sb.WriteString(" ON DUPLICATE KEY UPDATE ")
	first := true
	for _, col := range scanner.meta.columns {
		if conflictSet[col] {
			continue
		}
		if !first {
			sb.WriteString(", ")
		}
		first = false
		sb.WriteString(col)
		sb.WriteString(" = VALUES(")
		sb.WriteString(col)
		sb.WriteString(")")
	}
	if first {
		// 所有列都是 conflict 列,等价于 INSERT IGNORE
		sb.Reset()
		sb.WriteString(strings.Replace(scanner.meta.insertSQL, "INSERT INTO", "INSERT IGNORE INTO", 1))
	}
	query := sb.String()

	// 取 vals
	query2, values, err := scanner.buildInsertSQL()
	if err != nil {
		return 0, wrapMySQLError(scanner.table, "save on conflict", err)
	}
	defer valsPool.Put(&values)
	_ = query2

	db, err := p.getDB()
	if err != nil {
		return 0, err
	}

	start := time.Now()
	result, err := db.ExecContext(ctx, query, values...)
	duration := time.Since(start)
	if err != nil {
		p.queryLogger.LogError(ctx, query, duration, err, values...)
		return 0, wrapMySQLError(scanner.table, "save on conflict", err)
	}
	affected, _ := result.RowsAffected()
	p.logQ(ctx, "SAVE_ON_CONFLICT", query, duration, affected, values...)
	return affected, nil
}

// Upsert 通用 upsert(MySQL INSERT ... ON DUPLICATE KEY UPDATE)
//
// 适用场景:配置表/计数器表/用户元数据 等需要"有则更新、无则插入"的高频写入;
// 替代 DAO 中先 SELECT 检查再 INSERT/UPDATE 的两段式样板。
//
// 参数:
//   - table    : 表名(已通过 isValidIdentifier 校验)
//   - columns  : 列名切片
//   - rows     : 每行参数,每行长度必须 == len(columns)
//   - updateCols: 冲突时更新的列(空切片 = 更新除 PK 外所有列)
//   - chunkSize: 每条 INSERT 的行数(0 = 默认 200)
//
// 返回:总影响行数(累加各 chunk 的 RowsAffected;含 1=插入/2=更新的语义值)
//
// 限制:依赖表上 UNIQUE/PRIMARY KEY 约束触发 ON DUPLICATE KEY;
// 单条 INSERT 体积受 max_allowed_packet 约束,与 BatchExec 同。
func (p *MySQLPlugin) Upsert(ctx context.Context, table string, columns []string, rows [][]any, updateCols []string, chunkSize int) (int64, error) {
	if !isValidIdentifier(table) {
		return 0, wrapMySQLError(table, "upsert", ErrInvalidModel)
	}
	if len(columns) == 0 {
		return 0, wrapMySQLError(table, "upsert", fmt.Errorf("columns must not be empty"))
	}
	if len(rows) == 0 {
		return 0, nil
	}
	if chunkSize <= 0 {
		chunkSize = 200
	}

	// 确定要更新的列(空 = 默认所有非 PK 列;这里简化:若 updateCols 为空则全列更新)
	updateClause := ""
	if len(updateCols) > 0 {
		var sb strings.Builder
		for i, col := range updateCols {
			if !isValidIdentifier(col) {
				return 0, wrapMySQLError(table, "upsert", fmt.Errorf("invalid update column: %s", col))
			}
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(col)
			sb.WriteString(" = VALUES(")
			sb.WriteString(col)
			sb.WriteString(")")
		}
		updateClause = sb.String()
	} else {
		// 默认更新除 PK 外的所有列
		var sb strings.Builder
		first := true
		for _, col := range columns {
			if !isValidIdentifier(col) {
				return 0, wrapMySQLError(table, "upsert", fmt.Errorf("invalid column: %s", col))
			}
			// 跳过可能的 PK 列(id / *id)
			if col == "id" || col == "ID" {
				continue
			}
			if !first {
				sb.WriteString(", ")
			}
			first = false
			sb.WriteString(col)
			sb.WriteString(" = VALUES(")
			sb.WriteString(col)
			sb.WriteString(")")
		}
		updateClause = sb.String()
	}

	db, err := p.getDB()
	if err != nil {
		return 0, err
	}

	rowPlaceholder := "(" + strings.TrimRight(strings.Repeat("?,", len(columns)), ",") + ")"
	colList := strings.Join(columns, ", ")

	var totalAffected int64
	for start := 0; start < len(rows); start += chunkSize {
		end := start + chunkSize
		if end > len(rows) {
			end = len(rows)
		}
		chunk := rows[start:end]

		for i, row := range chunk {
			if len(row) != len(columns) {
				return totalAffected, wrapMySQLError(table, "upsert",
					fmt.Errorf("row %d: expected %d columns, got %d", i, len(columns), len(row)))
			}
		}

		var sb strings.Builder
		sb.Grow(40 + len(table) + len(colList) + len(chunk)*(len(rowPlaceholder)+2) + len(updateClause))
		sb.WriteString("INSERT INTO ")
		sb.WriteString(table)
		sb.WriteString(" (")
		sb.WriteString(colList)
		sb.WriteString(") VALUES ")
		for i := range chunk {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(rowPlaceholder)
		}
		sb.WriteString(" ON DUPLICATE KEY UPDATE ")
		sb.WriteString(updateClause)
		query := sb.String()

		args := make([]any, 0, len(chunk)*len(columns))
		for _, row := range chunk {
			args = append(args, row...)
		}

		t := time.Now()
		result, err := db.ExecContext(ctx, query, args...)
		duration := time.Since(t)

		if err != nil {
			p.queryLogger.LogError(ctx, query, duration, err, args...)
			return totalAffected, wrapMySQLError(table, "upsert", err)
		}
		affected, _ := result.RowsAffected()
		totalAffected += affected
		p.logQ(ctx, "UPSERT", query, duration, affected, args...)
	}

	return totalAffected, nil
}
