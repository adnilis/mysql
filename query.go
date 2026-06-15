package plugins

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
)

// MySQLQueryResult 链式查询结果
// 支持 GORM 风格的链式调用方法
type MySQLQueryResult struct {
	plugin  *MySQLPlugin    // 所属插件
	ctx     context.Context // 上下文
	query   string          // 查询语句
	args    []any           // 查询参数
	joins   []joinClause    // JOIN 子句
	wheres  []whereClause   // WHERE 子句
	groups  []string        // GROUP BY 子句
	havings []havingClause  // HAVING 子句
	orders  []string        // ORDER BY 子句
	limit   int             // LIMIT
	offset  int             // OFFSET
	err     error           // 错误信息(由 Table/Model 入口在标识符非法时设置,后续链式调用早退)
	// 缓存已构建的查询，避免重复构建
	preQuery string // 缓存的查询语句
	preArgs  []any  // 缓存的参数列表
	dirty    bool   // 是否需要重新构建
	// 复用 scratch 缓冲(由对象池归还时 reset 截断),消除 buildQuery 每次 make
	scratchEdits []edit             // buildQuery 内部 edit 列表缓冲
	scratchArgs  []any              // buildQuery 内部 allArgs 缓冲
	cancel       context.CancelFunc // WithTimeout 注入,reset 时回收避免泄漏
}

// joinClause JOIN 子句结构
type joinClause struct {
	joinType string // JOIN 类型：INNER、LEFT、RIGHT、FULL
	table    string // 表名
	on       string // ON 条件
	args     []any  // ON 条件的参数
}

// whereOp 标记 where 条件的前缀运算符(R04 性能优化:位标志替代 strings.HasPrefix)
type whereOp uint8

const (
	whereOpNone whereOp = iota // 普通 AND(默认)
	whereOpOr                  // OR 前缀
	whereOpNot                 // NOT 前缀
)

// whereClause WHERE 子句结构
type whereClause struct {
	condition string  // 条件表达式(不含 OR/NOT 前缀)
	args      []any   // 条件参数
	op        whereOp // 前缀运算符(R04:用于 build.go 高效拼接)
}

// havingClause HAVING 子句结构
type havingClause struct {
	condition string // 条件表达式
	args      []any  // 条件参数
}

// SQL 关键词常量（保留供 build.go 与外部参考）
const (
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
	// 用 containsKeywordFold/indexKeywordFold 避免 strings.ToUpper 全量拷贝
	if containsKeywordFold(qr.query, "SELECT *") {
		qr.query = strings.Replace(qr.query, "*", selected, 1)
	} else if containsKeywordFold(qr.query, "SELECT ") {
		// 如果已有 SELECT 子句，替换其后的内容直到 FROM
		selectPos := indexKeywordFold(qr.query, "SELECT ")
		fromPos := indexKeywordFold(qr.query, " "+sqlFrom+" ")
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
func (qr *MySQLQueryResult) Join(joinType, table, on string, args ...any) *MySQLQueryResult {
	if qr.err != nil {
		return qr
	}
	qr.joins = append(qr.joins, joinClause{joinType: joinType, table: table, on: on, args: args})
	qr.dirty = true
	return qr
}

// InnerJoin 添加 INNER JOIN 子句
func (qr *MySQLQueryResult) InnerJoin(table, on string, args ...any) *MySQLQueryResult {
	return qr.Join("INNER JOIN", table, on, args...)
}

// LeftJoin 添加 LEFT JOIN 子句
func (qr *MySQLQueryResult) LeftJoin(table, on string, args ...any) *MySQLQueryResult {
	return qr.Join("LEFT JOIN", table, on, args...)
}

// RightJoin 添加 RIGHT JOIN 子句
func (qr *MySQLQueryResult) RightJoin(table, on string, args ...any) *MySQLQueryResult {
	return qr.Join("RIGHT JOIN", table, on, args...)
}

// Where 添加 WHERE 条件
func (qr *MySQLQueryResult) Where(condition string, args ...any) *MySQLQueryResult {
	if qr.err != nil {
		return qr
	}
	qr.wheres = append(qr.wheres, whereClause{condition: condition, args: args, op: whereOpNone})
	qr.dirty = true
	return qr
}

// OrWhere 是 Or(condition) 的语义化别名(API 一致性)
//
// 推荐使用 OrWhere 而非 Or,语义更明确
func (qr *MySQLQueryResult) OrWhere(condition string, args ...any) *MySQLQueryResult {
	return qr.Or(condition, args...)
}

// Or 添加 OR 条件
// R04 优化:用 whereOp 标志替代条件字符串前缀拼接,避免 build.go 重复 HasPrefix 扫描
func (qr *MySQLQueryResult) Or(condition string, args ...any) *MySQLQueryResult {
	if qr.err != nil {
		return qr
	}
	qr.wheres = append(qr.wheres, whereClause{condition: condition, args: args, op: whereOpOr})
	qr.dirty = true
	return qr
}

// Not 添加 NOT 条件
// R04 优化:用 whereOp 标志替代条件字符串前缀拼接
func (qr *MySQLQueryResult) Not(condition string, args ...any) *MySQLQueryResult {
	if qr.err != nil {
		return qr
	}
	qr.wheres = append(qr.wheres, whereClause{condition: condition, args: args, op: whereOpNot})
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
func (qr *MySQLQueryResult) Having(condition string, args ...any) *MySQLQueryResult {
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

// WithTimeout 为当前链式查询设置超时,返回自身以支持链式
//
// d <= 0 时直接返回 qr(不变更 ctx)
// 多次链式调用会回收前一次注入的 cancel,避免泄漏
// 替代 DAO 层 xxxDBTimeout + context.WithTimeout 样板
//
// 用法:
//
//	plugin.Table("users").Where(...).WithTimeout(3*time.Second).Find(&results)
func (qr *MySQLQueryResult) WithTimeout(d time.Duration) *MySQLQueryResult {
	if d <= 0 {
		return qr
	}
	if qr.cancel != nil {
		qr.cancel()
		qr.cancel = nil
	}
	ctx, cancel := context.WithTimeout(qr.ctx, d)
	qr.ctx = ctx
	qr.cancel = cancel
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

// Page 设置分页(page 从 1 开始,pageSize <= 0 时不生效)
//
// 替代 DAO 层 .Limit(20).Offset(20*(page-1)) 样板
// page<=0 视为 1;超出范围的页码由 DB 自然返回空集
//
// 用法:
//
//	plugin.Table("orders").Where(...).Page(1, 20).Find(&page1)
//	plugin.Table("orders").Where(...).Page(2, 20).Find(&page2)
func (qr *MySQLQueryResult) Page(page, pageSize int) *MySQLQueryResult {
	if qr.err != nil {
		return qr
	}
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		return qr
	}
	qr.limit = pageSize
	qr.offset = (page - 1) * pageSize
	qr.dirty = true
	return qr
}

// First 扫描第一条结果到目标结构(标量或结构体指针)
//
// 支持的 dest 类型:
//   - *Struct / **Struct   : sqlx 反射扫描到结构体
//   - *int64 / *int / *string / *float64 / *bool : 标量(走 scalarFirst 路径,无需 1 列表)
//
// 记录不存在返回 ErrModelNotFound(支持 errors.Is 链路)
func (qr *MySQLQueryResult) First(dest any) error {
	if qr.err != nil {
		return qr.err
	}
	defer releaseMySQLQueryResult(qr)

	// 标量派发:避免 sqlx 对 *int64 走 1-列结构体扫描的反射开销
	if v := reflect.ValueOf(dest); v.IsValid() && v.Kind() == reflect.Ptr {
		switch v.Elem().Kind() {
		case reflect.Int64, reflect.Int, reflect.Int32, reflect.Int8,
			reflect.String, reflect.Float64, reflect.Float32, reflect.Bool:
			return qr.scalarFirst(dest)
		}
	}

	query, args := qr.buildQuery()
	if !containsKeywordFold(query, " "+sqlLimit+" ") {
		query += " " + sqlLimit + " 1"
	}

	db, err := qr.plugin.getDB()
	if err != nil {
		return err
	}

	start := time.Now()
	err = db.GetContext(qr.ctx, dest, query, args...)
	duration := time.Since(start)

	if err == sql.ErrNoRows {
		qr.plugin.queryLogger.LogError(qr.ctx, query, duration, err, args...)
		return wrapMySQLError("", "first", ErrModelNotFound)
	}

	if err != nil {
		qr.plugin.queryLogger.LogError(qr.ctx, query, duration, err, args...)
		return wrapMySQLError("", "first", err)
	}

	qr.plugin.logQ(qr.ctx, "SELECT", query, duration, 1, args...)
	return nil
}

// scalarFirst 标量 First 内部实现:不再依赖 sqlx 反射,直接用 QueryRow+Scan
// dest 必须是 *int64 / *int / *string / *float64 / *bool 之一
func (qr *MySQLQueryResult) scalarFirst(dest any) error {
	query, args := qr.buildQuery()
	if !containsKeywordFold(query, " "+sqlLimit+" ") {
		query += " " + sqlLimit + " 1"
	}

	db, err := qr.plugin.getDB()
	if err != nil {
		return err
	}

	start := time.Now()
	row := db.QueryRowxContext(qr.ctx, query, args...)
	duration := time.Since(start)

	if err := row.Scan(dest); err != nil {
		if err == sql.ErrNoRows {
			qr.plugin.queryLogger.LogError(qr.ctx, query, duration, err, args...)
			return wrapMySQLError("", "first", ErrModelNotFound)
		}
		qr.plugin.queryLogger.LogError(qr.ctx, query, duration, err, args...)
		return wrapMySQLError("", "first", err)
	}

	qr.plugin.logQ(qr.ctx, "SELECT", query, duration, 1, args...)
	return nil
}

// Find 扫描所有结果到目标切片
//
// R05 新增:支持 &[]map[string]any / &[]map[string]string 目的地
// (走 sqlx MapScan),消除 DAO 改用 plugin.DB().QueryxContext 的 escape hatch
func (qr *MySQLQueryResult) Find(dest any) error {
	if qr.err != nil {
		return qr.err
	}
	defer releaseMySQLQueryResult(qr)

	query, args := qr.buildQuery()

	db, err := qr.plugin.getDB()
	if err != nil {
		return err
	}

	// R05:MapScan 路径 — dest 为 *[]map[string]any 或 *[]map[string]string
	if isMapSliceDest(dest) {
		return qr.findMapScan(db, query, args, dest)
	}

	start := time.Now()
	err = db.SelectContext(qr.ctx, dest, query, args...)
	duration := time.Since(start)

	if err != nil {
		qr.plugin.queryLogger.LogError(qr.ctx, query, duration, err, args...)
		return wrapMySQLError("", "find", err)
	}

	rows := int64(reflect.ValueOf(dest).Elem().Len())
	qr.plugin.logQ(qr.ctx, "SELECT", query, duration, rows, args...)
	return nil
}

// isMapSliceDest 判断 dest 是否是 *[]map[K]V 形式
// 用于 Find 派发到 MapScan 路径(替代用户逃到 plugin.DB().QueryxContext)
func isMapSliceDest(dest any) bool {
	if dest == nil {
		return false
	}
	v := reflect.ValueOf(dest)
	if v.Kind() != reflect.Ptr || v.Elem().Kind() != reflect.Slice {
		return false
	}
	elem := v.Elem().Type().Elem()
	if elem.Kind() != reflect.Map {
		return false
	}
	// 只支持 string 键(map 是 sqlx MapScan 的固定契约)
	return elem.Key().Kind() == reflect.String
}

// findMapScan 走 sqlx Rows.MapScan 逐行读取到 map
// 与 SelectContext 区别:不依赖结构体 tag,适合动态列数 / 运行时列名
// 当前仅支持 *[]map[string]any(其他值类型 map 需自定义转换,sqlx MapScan 固定返回 map[string]any)
func (qr *MySQLQueryResult) findMapScan(db *sqlx.DB, query string, args []any, dest any) error {
	start := time.Now()
	rows, err := db.QueryxContext(qr.ctx, query, args...)
	duration := time.Since(start)

	if err != nil {
		qr.plugin.queryLogger.LogError(qr.ctx, query, duration, err, args...)
		return wrapMySQLError("", "find", err)
	}
	defer rows.Close()

	// dest 是 *[]map[string]any
	dv := reflect.ValueOf(dest).Elem()
	// 用 map[string]any 收集每行;append 时直接 append 引用
	for rows.Next() {
		m := make(map[string]any)
		if err := rows.MapScan(m); err != nil {
			qr.plugin.queryLogger.LogError(qr.ctx, query, duration, err, args...)
			return wrapMySQLError("", "find", err)
		}
		dv = reflect.Append(dv, reflect.ValueOf(m))
	}
	if err := rows.Err(); err != nil {
		qr.plugin.queryLogger.LogError(qr.ctx, query, duration, err, args...)
		return wrapMySQLError("", "find", err)
	}

	reflect.ValueOf(dest).Elem().Set(dv)
	qr.plugin.logQ(qr.ctx, "SELECT", query, duration, int64(dv.Len()), args...)
	return nil
}

// Count 统计结果数量
//
// 走 buildQuery 应用 Where/Join/Group 等链式条件(R01 修复)
// 内部用 "SELECT COUNT(*) FROM (inner) AS count_table" 包裹以兼容复杂查询
func (qr *MySQLQueryResult) Count(count *int64) error {
	if qr.err != nil {
		return qr.err
	}
	defer releaseMySQLQueryResult(qr)

	// 走 buildQuery 以应用 Where/Join/Group/Having 等链式条件
	innerQuery, innerArgs := qr.buildQuery()

	var sb strings.Builder
	sb.Grow(len(innerQuery) + 40)
	sb.WriteString("SELECT COUNT(*) FROM (")
	sb.WriteString(innerQuery)
	sb.WriteString(") AS count_table")

	countQuery := sb.String()

	db, err := qr.plugin.getDB()
	if err != nil {
		return err
	}

	start := time.Now()
	err = db.GetContext(qr.ctx, count, countQuery, innerArgs...)
	duration := time.Since(start)

	if err != nil {
		qr.plugin.queryLogger.LogError(qr.ctx, countQuery, duration, err, innerArgs...)
		return wrapMySQLError("", "count", err)
	}

	qr.plugin.logQ(qr.ctx, "COUNT", countQuery, duration, 1, innerArgs...)
	return nil
}

// Update 更新记录（链式调用）
func (qr *MySQLQueryResult) Update(column string, value any) error {
	if qr.err != nil {
		return qr.err
	}
	defer releaseMySQLQueryResult(qr)

	query, args := qr.buildQuery()

	// 链式 Update:从 SELECT 改写;用 containsKeywordFold/indexKeywordFold 避免 ToUpper 拷贝
	if containsKeywordFold(query, "SELECT * FROM") {
		query = strings.Replace(query, "SELECT * FROM", "UPDATE ", 1)
	}

	wherePos := indexKeywordFold(query, " WHERE ")
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
	newArgs := make([]any, 0, totalArgs)
	newArgs = append(newArgs, value)
	newArgs = append(newArgs, args...)

	db, err := qr.plugin.getDB()
	if err != nil {
		return err
	}

	start := time.Now()
	result, err := db.ExecContext(qr.ctx, query, newArgs...)
	duration := time.Since(start)

	if err != nil {
		qr.plugin.queryLogger.LogError(qr.ctx, query, duration, err, newArgs...)
		return wrapMySQLError("", "update", err)
	}

	rowsAffected, _ := result.RowsAffected()
	qr.plugin.logQ(qr.ctx, "UPDATE", query, duration, rowsAffected, newArgs...)
	return nil
}

// Delete 删除记录（链式调用）
func (qr *MySQLQueryResult) Delete() error {
	if qr.err != nil {
		return qr.err
	}
	defer releaseMySQLQueryResult(qr)

	query, args := qr.buildQuery()

	if containsKeywordFold(query, "SELECT * FROM") {
		query = strings.Replace(query, "SELECT * FROM", "DELETE FROM", 1)
	}

	db, err := qr.plugin.getDB()
	if err != nil {
		return err
	}

	start := time.Now()
	result, err := db.ExecContext(qr.ctx, query, args...)
	duration := time.Since(start)

	if err != nil {
		qr.plugin.queryLogger.LogError(qr.ctx, query, duration, err, args...)
		return wrapMySQLError("", "delete", err)
	}

	rowsAffected, _ := result.RowsAffected()
	qr.plugin.logQ(qr.ctx, "DELETE", query, duration, rowsAffected, args...)
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

	db, err := qr.plugin.getDB()
	if err != nil {
		return nil, err
	}

	start := time.Now()
	result, err := db.ExecContext(qr.ctx, query, args...)
	duration := time.Since(start)

	if err != nil {
		qr.plugin.queryLogger.LogError(qr.ctx, query, duration, err, args...)
		return nil, wrapMySQLError("", "exec", err)
	}

	rowsAffected, _ := result.RowsAffected()
	qr.plugin.logQ(qr.ctx, "EXEC", query, duration, rowsAffected, args...)
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
	if containsKeywordFold(qr.query, "SELECT *") {
		qr.query = strings.Replace(qr.query, "SELECT *", "SELECT DISTINCT "+selected, 1)
	} else if containsKeywordFold(qr.query, "SELECT ") {
		selectPos := indexKeywordFold(qr.query, "SELECT ")
		fromPos := indexKeywordFold(qr.query, " "+sqlFrom+" ")
		if fromPos >= 0 && selectPos >= 0 {
			qr.query = qr.query[:selectPos+7] + "DISTINCT " + selected + qr.query[fromPos:]
		}
	}

	return qr
}

// Take 获取任意一条记录（不添加 LIMIT 1）
// 用法：orm.Table("users").Where("status = ?", 1).Take(&user)
func (qr *MySQLQueryResult) Take(dest any) error {
	if qr.err != nil {
		return qr.err
	}
	defer releaseMySQLQueryResult(qr)

	query, args := qr.buildQuery()

	db, err := qr.plugin.getDB()
	if err != nil {
		return err
	}

	start := time.Now()
	err = db.GetContext(qr.ctx, dest, query, args...)
	duration := time.Since(start)

	if err == sql.ErrNoRows {
		qr.plugin.queryLogger.LogError(qr.ctx, query, duration, err, args...)
		return wrapMySQLError("", "take", ErrModelNotFound)
	}

	if err != nil {
		qr.plugin.queryLogger.LogError(qr.ctx, query, duration, err, args...)
		return wrapMySQLError("", "take", err)
	}

	qr.plugin.logQ(qr.ctx, "SELECT", query, duration, 1, args...)
	return nil
}

// Pluck 查询单列到切片
// 用法：orm.Table("users").Where("age > ?", 18).Pluck("name", &names)
func (qr *MySQLQueryResult) Pluck(field string, dest any) error {
	if qr.err != nil {
		return qr.err
	}
	defer releaseMySQLQueryResult(qr)

	// 构建只查询该字段的 SQL
	query, args := qr.buildQuery()

	fromPos := indexKeywordFold(query, " "+sqlFrom+" ")
	if fromPos < 0 {
		return fmt.Errorf("pluck requires FROM clause")
	}

	// 替换 SELECT ... FROM 为 SELECT field FROM
	selectPos := indexKeywordFold(query, "SELECT ")
	if selectPos >= 0 {
		qr.query = qr.query[:selectPos+7] + field + qr.query[fromPos:]
	}

	db, err := qr.plugin.getDB()
	if err != nil {
		return err
	}

	start := time.Now()
	err = db.SelectContext(qr.ctx, dest, qr.query, args...)
	duration := time.Since(start)

	if err != nil {
		qr.plugin.queryLogger.LogError(qr.ctx, qr.query, duration, err, args...)
		return wrapMySQLError("", "pluck", err)
	}

	qr.plugin.logQ(qr.ctx, "SELECT", qr.query, duration, 0, args...)
	return nil
}
