package plugins

import (
	"context"
	"fmt"
	"strings"
)

// TableInfo 表结构自省结果(R07)
type TableInfo struct {
	TableName string      // 表名
	Columns   []ColumnDef // 列定义(按 ordinal_position 排序)
	Indexes   []IndexDef  // 索引定义
}

// ColumnDef 列定义
type ColumnDef struct {
	Name     string // 列名
	DataType string // MySQL 数据类型(例:varchar(64), bigint(20) unsigned)
	Nullable bool   // 是否可空
	Default  string // 默认值(空字符串表示无默认)
	Key      string // PRI/UNI/MUL(主键/唯一/普通索引标记)
	Comment  string // 列注释
}

// IndexDef 索引定义
type IndexDef struct {
	Name    string   // 索引名
	Columns []string // 索引覆盖的列(按索引内顺序)
	Unique  bool     // 是否唯一
	Primary bool     // 是否主键
}

// ListTables 列出当前 DB 中所有基表(R07)
//
// 排除视图(VIEW)与临时表;返回按表名字典序排序。
// 仅在用户有 information_schema 读权限时可用。
func (p *MySQLPlugin) ListTables(ctx context.Context) ([]string, error) {
	db, err := p.getDB()
	if err != nil {
		return nil, err
	}
	const query = `SELECT TABLE_NAME FROM information_schema.TABLES
		WHERE TABLE_SCHEMA = ? AND TABLE_TYPE = 'BASE TABLE'
		ORDER BY TABLE_NAME`
	var names []string
	if err := db.SelectContext(ctx, &names, query, p.config.DBName); err != nil {
		return nil, wrapMySQLError(p.config.DBName, "list tables", err)
	}
	p.logQ(ctx, "LIST_TABLES", query, 0, int64(len(names)), p.config.DBName)
	return names, nil
}

// DescribeTable 返回表结构(R07)
//
// 包含列定义与索引定义;若表不存在返回 ErrModelNotFound(可 errors.Is 判定)。
// 仅在用户有 information_schema 读权限时可用。
func (p *MySQLPlugin) DescribeTable(ctx context.Context, table string) (*TableInfo, error) {
	if !isValidIdentifier(table) {
		return nil, wrapMySQLError(table, "describe table", ErrInvalidModel)
	}
	db, err := p.getDB()
	if err != nil {
		return nil, err
	}

	// 1) 列定义
	type columnRow struct {
		ColumnName    string  `db:"COLUMN_NAME"`
		DataType      string  `db:"DATA_TYPE"`
		ColumnType    string  `db:"COLUMN_TYPE"`
		IsNullable    string  `db:"IS_NULLABLE"`
		ColumnDefault *string `db:"COLUMN_DEFAULT"`
		ColumnKey     string  `db:"COLUMN_KEY"`
		ColumnComment string  `db:"COLUMN_COMMENT"`
		OrdinalPos    int     `db:"ORDINAL_POSITION"`
	}
	const colQuery = `SELECT COLUMN_NAME, DATA_TYPE, COLUMN_TYPE, IS_NULLABLE,
		COLUMN_DEFAULT, COLUMN_KEY, COLUMN_COMMENT, ORDINAL_POSITION
		FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?
		ORDER BY ORDINAL_POSITION`

	var rows []columnRow
	if err := db.SelectContext(ctx, &rows, colQuery, p.config.DBName, table); err != nil {
		return nil, wrapMySQLError(table, "describe table", err)
	}
	if len(rows) == 0 {
		return nil, wrapMySQLError(table, "describe table", ErrModelNotFound)
	}

	columns := make([]ColumnDef, len(rows))
	for i, r := range rows {
		def := ""
		if r.ColumnDefault != nil {
			def = *r.ColumnDefault
		}
		columns[i] = ColumnDef{
			Name:     r.ColumnName,
			DataType: r.ColumnType,
			Nullable: r.IsNullable == "YES",
			Default:  def,
			Key:      r.ColumnKey,
			Comment:  r.ColumnComment,
		}
	}

	// 2) 索引定义
	type indexColRow struct {
		IndexName  string `db:"INDEX_NAME"`
		ColumnName string `db:"COLUMN_NAME"`
		Seq        int    `db:"SEQ_IN_INDEX"`
		NonUnique  int    `db:"NON_UNIQUE"`
	}
	const idxQuery = `SELECT INDEX_NAME, COLUMN_NAME, SEQ_IN_INDEX, NON_UNIQUE
		FROM information_schema.STATISTICS
		WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?
		ORDER BY INDEX_NAME, SEQ_IN_INDEX`

	var idxRows []indexColRow
	if err := db.SelectContext(ctx, &idxRows, idxQuery, p.config.DBName, table); err != nil {
		return nil, wrapMySQLError(table, "describe table", err)
	}

	// 按索引名分组
	idxMap := make(map[string]*IndexDef, 4)
	idxOrder := make([]string, 0, 4)
	for _, r := range idxRows {
		idx, ok := idxMap[r.IndexName]
		if !ok {
			idx = &IndexDef{
				Name:    r.IndexName,
				Primary: r.IndexName == "PRIMARY",
				Unique:  r.NonUnique == 0,
			}
			idxMap[r.IndexName] = idx
			idxOrder = append(idxOrder, r.IndexName)
		}
		idx.Columns = append(idx.Columns, r.ColumnName)
	}
	indexes := make([]IndexDef, 0, len(idxOrder))
	for _, name := range idxOrder {
		indexes = append(indexes, *idxMap[name])
	}

	// 记录到 query logger(无 args)
	p.logQ(ctx, "DESCRIBE_TABLE", strings.Join([]string{colQuery, idxQuery}, ";"), 0, int64(len(columns)+len(indexes)), p.config.DBName, table)

	return &TableInfo{
		TableName: table,
		Columns:   columns,
		Indexes:   indexes,
	}, nil
}

// String 便于 fmt 打印 TableInfo
func (t *TableInfo) String() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Table %s:\n", t.TableName)
	for _, c := range t.Columns {
		nullable := "NOT NULL"
		if c.Nullable {
			nullable = "NULL"
		}
		fmt.Fprintf(&sb, "  %-30s %-20s %-8s key=%s\n", c.Name, c.DataType, nullable, c.Key)
	}
	for _, i := range t.Indexes {
		kind := "idx"
		if i.Primary {
			kind = "PK"
		} else if i.Unique {
			kind = "uniq"
		}
		fmt.Fprintf(&sb, "  [%s] %s (%s)\n", kind, i.Name, strings.Join(i.Columns, ", "))
	}
	return sb.String()
}

// ListIndexes 列出当前 DB 中所有非主键索引(R08)
//
// 主键索引(PRIMARY)不在此返回;按 (TABLE_NAME, INDEX_NAME) 排序。
// 可用于代码生成器扫描数据库的全部索引,生成对应的 DAO 辅助方法。
func (p *MySQLPlugin) ListIndexes(ctx context.Context) ([]IndexDef, error) {
	db, err := p.getDB()
	if err != nil {
		return nil, err
	}
	const query = `SELECT INDEX_NAME, TABLE_NAME, COLUMN_NAME, SEQ_IN_INDEX, NON_UNIQUE
		FROM information_schema.STATISTICS
		WHERE TABLE_SCHEMA = ? AND INDEX_NAME != 'PRIMARY'
		ORDER BY TABLE_NAME, INDEX_NAME, SEQ_IN_INDEX`

	type row struct {
		IndexName  string `db:"INDEX_NAME"`
		TableName  string `db:"TABLE_NAME"`
		ColumnName string `db:"COLUMN_NAME"`
		Seq        int    `db:"SEQ_IN_INDEX"`
		NonUnique  int    `db:"NON_UNIQUE"`
	}
	var rows []row
	if err := db.SelectContext(ctx, &rows, query, p.config.DBName); err != nil {
		return nil, wrapMySQLError(p.config.DBName, "list indexes", err)
	}

	// 按 (table, index) 分组
	idxMap := make(map[string]*IndexDef, 8)
	idxOrder := make([]string, 0, 8)
	for _, r := range rows {
		key := r.TableName + "." + r.IndexName
		idx, ok := idxMap[key]
		if !ok {
			idx = &IndexDef{
				Name:    r.IndexName,
				Unique:  r.NonUnique == 0,
				Primary: false,
			}
			idxMap[key] = idx
			idxOrder = append(idxOrder, key)
		}
		idx.Columns = append(idx.Columns, r.ColumnName)
	}
	result := make([]IndexDef, 0, len(idxOrder))
	for _, k := range idxOrder {
		result = append(result, *idxMap[k])
	}
	p.logQ(ctx, "LIST_INDEXES", query, 0, int64(len(result)), p.config.DBName)
	return result, nil
}

// DescribeIndex 返回单表的所有索引(R08)
//
// 含主键索引(PRIMARY);若表不存在返回 ErrModelNotFound。
func (p *MySQLPlugin) DescribeIndex(ctx context.Context, table string) ([]IndexDef, error) {
	if !isValidIdentifier(table) {
		return nil, wrapMySQLError(table, "describe index", ErrInvalidModel)
	}
	db, err := p.getDB()
	if err != nil {
		return nil, err
	}
	const query = `SELECT INDEX_NAME, COLUMN_NAME, SEQ_IN_INDEX, NON_UNIQUE
		FROM information_schema.STATISTICS
		WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?
		ORDER BY INDEX_NAME, SEQ_IN_INDEX`

	type row struct {
		IndexName  string `db:"INDEX_NAME"`
		ColumnName string `db:"COLUMN_NAME"`
		Seq        int    `db:"SEQ_IN_INDEX"`
		NonUnique  int    `db:"NON_UNIQUE"`
	}
	var rows []row
	if err := db.SelectContext(ctx, &rows, query, p.config.DBName, table); err != nil {
		return nil, wrapMySQLError(table, "describe index", err)
	}
	if len(rows) == 0 {
		return nil, wrapMySQLError(table, "describe index", ErrModelNotFound)
	}

	idxMap := make(map[string]*IndexDef, 4)
	idxOrder := make([]string, 0, 4)
	for _, r := range rows {
		idx, ok := idxMap[r.IndexName]
		if !ok {
			idx = &IndexDef{
				Name:    r.IndexName,
				Unique:  r.NonUnique == 0,
				Primary: r.IndexName == "PRIMARY",
			}
			idxMap[r.IndexName] = idx
			idxOrder = append(idxOrder, r.IndexName)
		}
		idx.Columns = append(idx.Columns, r.ColumnName)
	}
	result := make([]IndexDef, 0, len(idxOrder))
	for _, k := range idxOrder {
		result = append(result, *idxMap[k])
	}
	p.logQ(ctx, "DESCRIBE_INDEX", query, 0, int64(len(result)), p.config.DBName, table)
	return result, nil
}
