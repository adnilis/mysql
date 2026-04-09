package plugins

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// MySQLQueryResult 链式查询结果
// 支持 GORM 风格的链式调用方法
type MySQLQueryResult struct {
	plugin  *MySQLPlugin    // 所属插件
	ctx     context.Context // 上下文
	query   string          // 查询语句
	args    []interface{}   // 查询参数
	joins   []joinClause    // JOIN 子句
	wheres  []whereClause   // WHERE 子句
	groups  []string        // GROUP BY 子句
	havings []havingClause  // HAVING 子句
	orders  []string        // ORDER BY 子句
	limit   int             // LIMIT
	offset  int             // OFFSET
	err     error           // 错误信息
	// 缓存已构建的查询，避免重复构建
	preQuery string        // 缓存的查询语句
	preArgs  []interface{} // 缓存的参数列表
	dirty    bool          // 是否需要重新构建
}

// joinClause JOIN 子句结构
type joinClause struct {
	joinType string        // JOIN 类型：INNER、LEFT、RIGHT、FULL
	table    string        // 表名
	on       string        // ON 条件
	args     []interface{} // ON 条件的参数
}

// whereClause WHERE 子句结构
type whereClause struct {
	condition string        // 条件表达式
	args      []interface{} // 条件参数
}

// havingClause HAVING 子句结构
type havingClause struct {
	condition string        // 条件表达式
	args      []interface{} // 条件参数
}

// SQL 关键词常量
const (
	sqlSelect  = "SELECT"
	sqlFrom    = "FROM"
	sqlWhere   = "WHERE"
	sqlGroupBy = "GROUP BY"
	sqlHaving  = "HAVING"
	sqlOrderBy = "ORDER BY"
	sqlLimit   = "LIMIT"
	sqlOffset  = "OFFSET"
)

// Select 指定要查询的字段
// fields: 要查询的字段列表，支持 "field1, field2" 或 "field1", "field2"
// 默认查询所有字段 (*)，调用此方法后可指定具体字段
func (qr *MySQLQueryResult) Select(fields ...string) *MySQLQueryResult {
	if qr.err != nil {
		return qr
	}
	if len(fields) == 0 {
		return qr
	}

	selected := strings.Join(fields, ", ")

	// 替换 SELECT * 或 SELECT id, ... 为 SELECT fields...
	queryUpper := strings.ToUpper(qr.query)
	if strings.Contains(queryUpper, "SELECT *") {
		qr.query = strings.Replace(qr.query, "*", selected, 1)
	} else if strings.Contains(queryUpper, "SELECT ") {
		// 如果已有 SELECT 子句，替换其后的内容直到 FROM
		selectPos := strings.Index(queryUpper, "SELECT ")
		fromPos := strings.Index(queryUpper, " "+sqlFrom+" ")
		if fromPos >= 0 && selectPos >= 0 {
			qr.query = qr.query[:selectPos+7] + selected + qr.query[fromPos:]
		}
	}
	qr.dirty = true

	return qr
}

// Join 添加 JOIN 子句（通用方法）
// joinType: "INNER", "LEFT", "RIGHT", "FULL"
// table: 要 JOIN 的表名
// on: ON 条件
// args: ON 条件的参数
func (qr *MySQLQueryResult) Join(joinType, table, on string, args ...interface{}) *MySQLQueryResult {
	if qr.err != nil {
		return qr
	}
	qr.joins = append(qr.joins, joinClause{joinType: joinType, table: table, on: on, args: args})
	qr.dirty = true
	return qr
}

// InnerJoin 添加 INNER JOIN 子句
func (qr *MySQLQueryResult) InnerJoin(table, on string, args ...interface{}) *MySQLQueryResult {
	return qr.Join("INNER JOIN", table, on, args...)
}

// LeftJoin 添加 LEFT JOIN 子句
func (qr *MySQLQueryResult) LeftJoin(table, on string, args ...interface{}) *MySQLQueryResult {
	return qr.Join("LEFT JOIN", table, on, args...)
}

// RightJoin 添加 RIGHT JOIN 子句
func (qr *MySQLQueryResult) RightJoin(table, on string, args ...interface{}) *MySQLQueryResult {
	return qr.Join("RIGHT JOIN", table, on, args...)
}

// Where 添加 WHERE 条件
func (qr *MySQLQueryResult) Where(condition string, args ...interface{}) *MySQLQueryResult {
	if qr.err != nil {
		return qr
	}
	qr.wheres = append(qr.wheres, whereClause{condition: condition, args: args})
	qr.dirty = true
	return qr
}

// Or 添加 OR 条件
func (qr *MySQLQueryResult) Or(condition string, args ...interface{}) *MySQLQueryResult {
	if qr.err != nil {
		return qr
	}
	qr.wheres = append(qr.wheres, whereClause{condition: "OR " + condition, args: args})
	qr.dirty = true
	return qr
}

// Not 添加 NOT 条件
func (qr *MySQLQueryResult) Not(condition string, args ...interface{}) *MySQLQueryResult {
	if qr.err != nil {
		return qr
	}
	qr.wheres = append(qr.wheres, whereClause{condition: "NOT " + condition, args: args})
	qr.dirty = true
	return qr
}

// Group 添加 GROUP BY 子句
func (qr *MySQLQueryResult) Group(fields ...string) *MySQLQueryResult {
	if qr.err != nil {
		return qr
	}
	qr.groups = append(qr.groups, fields...)
	qr.dirty = true
	return qr
}

// Having 添加 HAVING 条件
func (qr *MySQLQueryResult) Having(condition string, args ...interface{}) *MySQLQueryResult {
	if qr.err != nil {
		return qr
	}
	qr.havings = append(qr.havings, havingClause{condition: condition, args: args})
	qr.dirty = true
	return qr
}

// Order 添加 ORDER BY 子句
func (qr *MySQLQueryResult) Order(field string) *MySQLQueryResult {
	if qr.err != nil {
		return qr
	}
	qr.orders = append(qr.orders, field)
	qr.dirty = true
	return qr
}

// Asc 添加 ASC 排序
func (qr *MySQLQueryResult) Asc(fields ...string) *MySQLQueryResult {
	for _, f := range fields {
		qr.orders = append(qr.orders, f+" ASC")
	}
	qr.dirty = true
	return qr
}

// Desc 添加 DESC 排序
func (qr *MySQLQueryResult) Desc(fields ...string) *MySQLQueryResult {
	for _, f := range fields {
		qr.orders = append(qr.orders, f+" DESC")
	}
	qr.dirty = true
	return qr
}

// Limit 限制返回行数
func (qr *MySQLQueryResult) Limit(limit int) *MySQLQueryResult {
	if qr.err != nil || limit <= 0 {
		return qr
	}
	qr.limit = limit
	qr.dirty = true
	return qr
}

// Offset 设置偏移量
func (qr *MySQLQueryResult) Offset(offset int) *MySQLQueryResult {
	if qr.err != nil || offset < 0 {
		return qr
	}
	qr.offset = offset
	qr.dirty = true
	return qr
}

// First 扫描第一条结果到目标结构
func (qr *MySQLQueryResult) First(dest interface{}) error {
	if qr.err != nil {
		return qr.err
	}
	defer releaseMySQLQueryResult(qr)

	query, args := qr.buildQuery()
	if !strings.Contains(strings.ToUpper(query), sqlLimit) {
		query += " " + sqlLimit + " 1"
	}

	qr.plugin.mu.RLock()
	db := qr.plugin.db
	qr.plugin.mu.RUnlock()

	if db == nil {
		return ErrMySQLNotEnabled
	}

	start := time.Now()
	err := db.GetContext(qr.ctx, dest, query, args...)
	duration := time.Since(start)

	if err == sql.ErrNoRows {
		_ = duration
		return ErrModelNotFound
	}

	if err != nil {
		_ = duration
		return fmt.Errorf("query first failed: %w", err)
	}

	_ = duration
	return nil
}

// Find 扫描所有结果到目标切片
func (qr *MySQLQueryResult) Find(dest interface{}) error {
	if qr.err != nil {
		return qr.err
	}
	defer releaseMySQLQueryResult(qr)

	query, args := qr.buildQuery()

	qr.plugin.mu.RLock()
	db := qr.plugin.db
	qr.plugin.mu.RUnlock()

	if db == nil {
		return ErrMySQLNotEnabled
	}

	start := time.Now()
	err := db.SelectContext(qr.ctx, dest, query, args...)
	duration := time.Since(start)

	if err != nil {
		_ = duration
		return fmt.Errorf("query find failed: %w", err)
	}

	_ = duration
	return nil
}

// Count 统计结果数量
func (qr *MySQLQueryResult) Count(count *int64) error {
	if qr.err != nil {
		return qr.err
	}
	defer releaseMySQLQueryResult(qr)

	var sb strings.Builder
	sb.Grow(len(qr.query) + 40)
	sb.WriteString("SELECT COUNT(*) FROM (")
	sb.WriteString(qr.query)
	sb.WriteString(") AS count_table")

	countQuery := sb.String()

	qr.plugin.mu.RLock()
	db := qr.plugin.db
	qr.plugin.mu.RUnlock()

	if db == nil {
		return ErrMySQLNotEnabled
	}

	start := time.Now()
	err := db.GetContext(qr.ctx, count, countQuery)
	duration := time.Since(start)

	if err != nil {
		_ = duration
		return fmt.Errorf("count failed: %w", err)
	}

	_ = duration
	return nil
}

// Update 更新记录（链式调用）
func (qr *MySQLQueryResult) Update(column string, value interface{}) error {
	if qr.err != nil {
		return qr.err
	}
	defer releaseMySQLQueryResult(qr)

	query, args := qr.buildQuery()

	queryUpper := strings.ToUpper(query)
	if strings.Contains(queryUpper, "SELECT * FROM") {
		query = strings.Replace(query, "SELECT * FROM", "UPDATE ", 1)
	}

	wherePos := strings.Index(queryUpper, " WHERE ")
	if wherePos < 0 {
		return fmt.Errorf("update requires WHERE condition")
	}

	var sb strings.Builder
	sb.Grow(wherePos + len(column) + 20)
	sb.WriteString(query[:wherePos])
	sb.WriteString(" SET ")
	sb.WriteString(column)
	sb.WriteString(" = ?")

	query = sb.String()

	totalArgs := len(args) + 1
	newArgs := make([]interface{}, 0, totalArgs)
	newArgs = append(newArgs, value)
	newArgs = append(newArgs, args...)

	qr.plugin.mu.RLock()
	db := qr.plugin.db
	qr.plugin.mu.RUnlock()

	if db == nil {
		return ErrMySQLNotEnabled
	}

	start := time.Now()
	result, err := db.ExecContext(qr.ctx, query, newArgs...)
	duration := time.Since(start)

	if err != nil {
		_ = duration
		return fmt.Errorf("update failed: %w", err)
	}

	// 忽略 RowsAffected 错误，因为某些驱动可能不支持
	_, _ = result.RowsAffected()
	_ = duration
	return nil
}

// Delete 删除记录（链式调用）
func (qr *MySQLQueryResult) Delete() error {
	if qr.err != nil {
		return qr.err
	}
	defer releaseMySQLQueryResult(qr)

	query, args := qr.buildQuery()

	queryUpper := strings.ToUpper(query)
	if strings.Contains(queryUpper, "SELECT * FROM") {
		query = strings.Replace(query, "SELECT * FROM", "DELETE FROM", 1)
	}

	qr.plugin.mu.RLock()
	db := qr.plugin.db
	qr.plugin.mu.RUnlock()

	if db == nil {
		return ErrMySQLNotEnabled
	}

	start := time.Now()
	result, err := db.ExecContext(qr.ctx, query, args...)
	duration := time.Since(start)

	if err != nil {
		_ = duration
		return fmt.Errorf("delete failed: %w", err)
	}

	// 忽略 RowsAffected 错误，因为某些驱动可能不支持
	_, _ = result.RowsAffected()
	_ = duration
	return nil
}

// Exec 执行原始 SQL 语句（链式调用）
// 返回 sql.Result，可获取 LastInsertId 和 RowsAffected
func (qr *MySQLQueryResult) Exec() (sql.Result, error) {
	if qr.err != nil {
		return nil, qr.err
	}
	defer releaseMySQLQueryResult(qr)

	query, args := qr.buildQuery()

	qr.plugin.mu.RLock()
	db := qr.plugin.db
	qr.plugin.mu.RUnlock()

	if db == nil {
		return nil, ErrMySQLNotEnabled
	}

	start := time.Now()
	result, err := db.ExecContext(qr.ctx, query, args...)
	duration := time.Since(start)

	if err != nil {
		_ = duration
		return nil, fmt.Errorf("exec failed: %w", err)
	}

	_ = duration
	return result, nil
}

// Distinct 去重查询
// 用法：orm.Table("users").Distinct("name", "age").Find(&results)
func (qr *MySQLQueryResult) Distinct(fields ...string) *MySQLQueryResult {
	if qr.err != nil {
		return qr
	}
	if len(fields) == 0 {
		return qr
	}

	selected := strings.Join(fields, ", ")

	// 替换 SELECT * 为 SELECT DISTINCT fields
	queryUpper := strings.ToUpper(qr.query)
	if strings.Contains(queryUpper, "SELECT *") {
		qr.query = strings.Replace(qr.query, "SELECT *", "SELECT DISTINCT "+selected, 1)
	} else if strings.Contains(queryUpper, "SELECT ") {
		selectPos := strings.Index(queryUpper, "SELECT ")
		fromPos := strings.Index(queryUpper, " "+sqlFrom+" ")
		if fromPos >= 0 && selectPos >= 0 {
			qr.query = qr.query[:selectPos+7] + "DISTINCT " + selected + qr.query[fromPos:]
		}
	}

	return qr
}

// Take 获取任意一条记录（不添加 LIMIT 1）
// 用法：orm.Table("users").Where("status = ?", 1).Take(&user)
func (qr *MySQLQueryResult) Take(dest interface{}) error {
	if qr.err != nil {
		return qr.err
	}
	defer releaseMySQLQueryResult(qr)

	query, args := qr.buildQuery()

	qr.plugin.mu.RLock()
	db := qr.plugin.db
	qr.plugin.mu.RUnlock()

	if db == nil {
		return ErrMySQLNotEnabled
	}

	start := time.Now()
	err := db.GetContext(qr.ctx, dest, query, args...)
	duration := time.Since(start)

	if err == sql.ErrNoRows {
		_ = duration
		return ErrModelNotFound
	}

	if err != nil {
		_ = duration
		return fmt.Errorf("query take failed: %w", err)
	}

	_ = duration
	return nil
}

// Pluck 查询单列到切片
// 用法：orm.Table("users").Where("age > ?", 18).Pluck("name", &names)
func (qr *MySQLQueryResult) Pluck(field string, dest interface{}) error {
	if qr.err != nil {
		return qr.err
	}
	defer releaseMySQLQueryResult(qr)

	// 构建只查询该字段的 SQL
	query, args := qr.buildQuery()

	queryUpper := strings.ToUpper(query)
	fromPos := strings.Index(queryUpper, " "+sqlFrom+" ")
	if fromPos < 0 {
		return fmt.Errorf("pluck requires FROM clause")
	}

	// 替换 SELECT ... FROM 为 SELECT field FROM
	selectPos := strings.Index(queryUpper, "SELECT ")
	if selectPos >= 0 {
		qr.query = qr.query[:selectPos+7] + field + qr.query[fromPos:]
	}

	qr.plugin.mu.RLock()
	db := qr.plugin.db
	qr.plugin.mu.RUnlock()

	if db == nil {
		return ErrMySQLNotEnabled
	}

	start := time.Now()
	err := db.SelectContext(qr.ctx, dest, qr.query, args...)
	duration := time.Since(start)

	if err != nil {
		_ = duration
		return fmt.Errorf("pluck failed: %w", err)
	}

	_ = duration
	return nil
}
