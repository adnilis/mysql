package plugins

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"time"
)

// logQ 内部助手：组装 "[OP] query" 前缀，统一通过 LogQuery 落盘
// 调用方传入 op 形如 "INSERT" / "UPDATE" / "DELETE" / "EXEC" / "COUNT" / "EXISTS"
func (p *MySQLPlugin) logQ(ctx context.Context, op, query string, duration time.Duration, rows int64, args ...any) {
	if p.queryLogger == nil {
		return
	}
	p.queryLogger.LogQuery(ctx, "["+op+"] "+query, duration, rows, args...)
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
		return ErrModelNotFound
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
