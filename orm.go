package plugins

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"time"
)

// Begin 开启事务
// 返回 Transaction 对象，用于执行事务操作
func (p *MySQLPlugin) Begin() (*MySQLTransaction, error) {
	p.mu.RLock()
	db := p.db
	p.mu.RUnlock()

	if db == nil {
		return nil, ErrMySQLNotEnabled
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
func (p *MySQLPlugin) Query(ctx context.Context, query string, args ...interface{}) *MySQLQueryResult {
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

	var sb strings.Builder
	sb.Grow(len(tableName) + 20)
	sb.WriteString("SELECT * FROM ")
	sb.WriteString(tableName)
	qr.query = sb.String()
	return qr
}

// Model 根据模型自动推断表名
// 用法：orm.Model(&User{}).Where("age > ?", 18).Find(&users)
func (p *MySQLPlugin) Model(model IModel) *MySQLQueryResult {
	if model == nil {
		qr := acquireMySQLQueryResult()
		qr.err = ErrInvalidModel
		return qr
	}

	tableName := model.TableName()

	// 验证表名安全性
	if !isValidIdentifier(tableName) {
		qr := acquireMySQLQueryResult()
		qr.err = ErrInvalidModel
		return qr
	}

	qr := acquireMySQLQueryResult()
	qr.plugin = p
	qr.ctx = context.Background()
	var sb strings.Builder
	sb.Grow(len(tableName) + 20)
	sb.WriteString("SELECT * FROM ")
	sb.WriteString(tableName)
	qr.query = sb.String()
	return qr
}

// Insert 插入单条记录
// 返回插入记录的 ID（自增主键）
func (p *MySQLPlugin) Insert(ctx context.Context, model IModel) (int64, error) {
	p.mu.RLock()
	db := p.db
	p.mu.RUnlock()

	if db == nil {
		return 0, ErrMySQLNotEnabled
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

	p.queryLogger.LogOperation(ctx, "INSERT", scanner.table, duration, 1, query, values...)
	return id, nil
}

// Update 更新记录（带 WHERE 条件）
// where: WHERE 条件字符串（不包含 WHERE 关键字）
// args: WHERE 条件的参数
func (p *MySQLPlugin) Update(ctx context.Context, model IModel, where string, args ...interface{}) (int64, error) {
	p.mu.RLock()
	db := p.db
	p.mu.RUnlock()

	if db == nil {
		return 0, ErrMySQLNotEnabled
	}

	scanner := newFieldScanner(model)
	query, values, err := scanner.buildUpdateSQL()
	if err != nil {
		return 0, wrapMySQLError(scanner.table, "update", err)
	}

	query += " WHERE " + where
	values = append(values, args...)

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

	p.queryLogger.LogOperation(ctx, "UPDATE", scanner.table, duration, rowsAffected, query, values...)
	return rowsAffected, nil
}

// Delete 删除记录（带 WHERE 条件）
// where: WHERE 条件字符串（不包含 WHERE 关键字）
// args: WHERE 条件的参数
func (p *MySQLPlugin) Delete(ctx context.Context, model IModel, where string, args ...interface{}) (int64, error) {
	p.mu.RLock()
	db := p.db
	p.mu.RUnlock()

	if db == nil {
		return 0, ErrMySQLNotEnabled
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

	p.queryLogger.LogOperation(ctx, "DELETE", scanner.table, duration, rowsAffected, query, values...)
	return rowsAffected, nil
}

// GetByID 根据 ID 获取模型
// 如果记录不存在，返回 ErrModelNotFound
func (p *MySQLPlugin) GetByID(ctx context.Context, model IModel, id interface{}) error {
	p.mu.RLock()
	db := p.db
	p.mu.RUnlock()

	if db == nil {
		return ErrMySQLNotEnabled
	}

	scanner := newFieldScanner(model)
	query := scanner.buildSelectByIDSQL()

	start := time.Now()
	err := db.GetContext(ctx, model, query, id)
	duration := time.Since(start)

	if err == sql.ErrNoRows {
		p.queryLogger.LogError(ctx, query, duration, ErrModelNotFound, id)
		return wrapMySQLError(scanner.table, "select", ErrModelNotFound)
	}

	if err != nil {
		p.queryLogger.LogError(ctx, query, duration, err, id)
		return wrapMySQLError(scanner.table, "select", err)
	}

	p.queryLogger.LogQuery(ctx, query, duration, 1, id)
	return nil
}

// UpdateByID 根据 ID 更新模型
// 返回影响的行数
func (p *MySQLPlugin) UpdateByID(ctx context.Context, model IModel, id interface{}) (int64, error) {
	p.mu.RLock()
	db := p.db
	p.mu.RUnlock()

	if db == nil {
		return 0, ErrMySQLNotEnabled
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

	p.queryLogger.LogOperation(ctx, "UPDATE", scanner.table, duration, rowsAffected, query, values...)
	return rowsAffected, nil
}

// DeleteByID 根据 ID 删除模型
// 返回影响的行数
func (p *MySQLPlugin) DeleteByID(ctx context.Context, model IModel, id interface{}) (int64, error) {
	p.mu.RLock()
	db := p.db
	p.mu.RUnlock()

	if db == nil {
		return 0, ErrMySQLNotEnabled
	}

	scanner := newFieldScanner(model)
	query := fmt.Sprintf("DELETE FROM %s WHERE id = ?", scanner.table)

	start := time.Now()
	result, err := db.ExecContext(ctx, query, id)
	duration := time.Since(start)

	if err != nil {
		p.queryLogger.LogError(ctx, query, duration, err, id)
		return 0, wrapMySQLError(scanner.table, "delete", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		p.queryLogger.LogError(ctx, query, duration, err, id)
		return 0, wrapMySQLError(scanner.table, "delete", fmt.Errorf("failed to get rows affected: %w", err))
	}

	p.queryLogger.LogOperation(ctx, "DELETE", scanner.table, duration, rowsAffected, query, id)
	return rowsAffected, nil
}

// Select 执行 SELECT 查询（到切片）
// dest: 目标切片（如 &[]User{}）
func (p *MySQLPlugin) Select(ctx context.Context, dest interface{}, query string, args ...interface{}) error {
	p.mu.RLock()
	db := p.db
	p.mu.RUnlock()

	if db == nil {
		return ErrMySQLNotEnabled
	}

	start := time.Now()
	err := db.SelectContext(ctx, dest, query, args...)
	duration := time.Since(start)

	if err != nil {
		p.queryLogger.LogError(ctx, query, duration, err, args...)
		return fmt.Errorf("select failed: %w", err)
	}

	p.queryLogger.LogQuery(ctx, query, duration, 0, args...)
	return nil
}

// Get 执行获取单条记录的查询
// dest: 目标结构体（如 &User{}）
// 如果记录不存在，返回 ErrModelNotFound
func (p *MySQLPlugin) Get(ctx context.Context, dest interface{}, query string, args ...interface{}) error {
	p.mu.RLock()
	db := p.db
	p.mu.RUnlock()

	if db == nil {
		return ErrMySQLNotEnabled
	}

	start := time.Now()
	err := db.GetContext(ctx, dest, query, args...)
	duration := time.Since(start)

	if err == sql.ErrNoRows {
		p.queryLogger.LogError(ctx, query, duration, ErrModelNotFound, args...)
		return ErrModelNotFound
	}

	if err != nil {
		p.queryLogger.LogError(ctx, query, duration, err, args...)
		return fmt.Errorf("get failed: %w", err)
	}

	p.queryLogger.LogQuery(ctx, query, duration, 1, args...)
	return nil
}

// Exec 执行 SQL 语句（无返回值）
// 用于执行 DDL 语句或不需要返回结果的 DML 语句
// 返回影响的行数
func (p *MySQLPlugin) Exec(ctx context.Context, query string, args ...interface{}) (int64, error) {
	p.mu.RLock()
	db := p.db
	p.mu.RUnlock()

	if db == nil {
		return 0, ErrMySQLNotEnabled
	}

	start := time.Now()
	result, err := db.ExecContext(ctx, query, args...)
	duration := time.Since(start)

	if err != nil {
		_ = duration
		return 0, fmt.Errorf("exec failed: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		_ = duration
		return 0, fmt.Errorf("failed to get rows affected: %w", err)
	}

	_ = duration
	return rowsAffected, nil
}

// ExecReturningID 执行 INSERT 语句并返回插入的 ID
// 用于执行 INSERT 语句后需要获取自增主键的场景
// 返回插入记录的 ID
func (p *MySQLPlugin) ExecReturningID(ctx context.Context, query string, args ...interface{}) (int64, error) {
	p.mu.RLock()
	db := p.db
	p.mu.RUnlock()

	if db == nil {
		return 0, ErrMySQLNotEnabled
	}

	start := time.Now()
	result, err := db.ExecContext(ctx, query, args...)
	duration := time.Since(start)

	if err != nil {
		_ = duration
		return 0, fmt.Errorf("exec returning id failed: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		_ = duration
		return 0, fmt.Errorf("failed to get last insert id: %w", err)
	}

	_ = duration
	return id, nil
}

// Count 计数
// table: 表名
// where: WHERE 条件（可选，为空则统计全表）
// args: WHERE 条件的参数
func (p *MySQLPlugin) Count(ctx context.Context, table string, where string, args ...interface{}) (int64, error) {
	p.mu.RLock()
	db := p.db
	p.mu.RUnlock()

	if db == nil {
		return 0, ErrMySQLNotEnabled
	}

	query := fmt.Sprintf("SELECT COUNT(*) as count FROM %s", table)
	if where != "" {
		query += " WHERE " + where
	}

	var result struct {
		Count int64 `db:"count"`
	}

	err := db.GetContext(ctx, &result, query, args...)
	if err != nil {
		return 0, fmt.Errorf("count failed: %w", err)
	}

	return result.Count, nil
}

// Exists 检查记录是否存在
// table: 表名
// where: WHERE 条件
// args: WHERE 条件的参数
func (p *MySQLPlugin) Exists(ctx context.Context, table string, where string, args ...interface{}) (bool, error) {
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

// Save GORM 风格保存记录：如果 ID 存在则更新，否则插入
// 用法：orm.Save(ctx, &User{ID: 1, Name: "Updated"})
func (p *MySQLPlugin) Save(ctx context.Context, model IModel) error {
	if model == nil {
		return fmt.Errorf("model cannot be nil")
	}

	val := reflect.ValueOf(model)
	if val.Kind() == reflect.Ptr {
		if val.IsNil() {
			return fmt.Errorf("model cannot be nil pointer")
		}
		val = val.Elem()
	} else {
		return fmt.Errorf("model must be a pointer")
	}

	// 检查 ID 字段
	idField := val.FieldByName("ID")
	if idField.IsValid() && !idField.IsZero() {
		_, err := p.UpdateByID(ctx, model, idField.Interface())
		return err
	}

	_, err := p.Insert(ctx, model)
	return err
}

// First GORM 风格获取第一条记录
// 用法：var user User; orm.First(ctx, &user, 1)
func (p *MySQLPlugin) First(ctx context.Context, dest interface{}, id interface{}) error {
	tableName := getTableNameFromDest(dest)
	if tableName == "" {
		return fmt.Errorf("cannot infer table name from destination")
	}

	query := fmt.Sprintf("SELECT * FROM %s WHERE id = ? LIMIT 1", tableName)
	return p.Get(ctx, dest, query, id)
}

// Find GORM 风格查找多条记录
// 用法：var users []User; orm.Find(ctx, &users, "age > ?", 18)
func (p *MySQLPlugin) Find(ctx context.Context, dest interface{}, query string, args ...interface{}) error {
	return p.Select(ctx, dest, query, args...)
}
