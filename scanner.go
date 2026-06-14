package plugins

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
)

// fieldInfo 字段元数据中的单个字段描述
// 把先前在 fieldMeta/getFieldMeta/dbFields 三处重复的匿名 struct 提取为命名类型
type fieldInfo struct {
	tag      string // db tag 列名
	fieldIdx int    // 在结构体中的字段索引
}

// fieldMeta 类型元数据缓存
// 缓存每种类型的字段信息与预构建的 SQL 模板，避免重复反射扫描与重复拼接
type fieldMeta struct {
	tableName  string      // 表名（首次 newFieldScanner 时填充）
	columns    []string    // 所有 db tag 列名
	idIndex    int         // 主键字段的结构体索引；-1 表示不存在
	pkColumn   string      // 主键列名（来自 db:"id" 标签）；空表示不存在
	fieldInfos []fieldInfo // 字段信息

	// 预构建的 SQL 模板（同一类型跨调用复用）
	// 模板只包含占位符 `?`，运行时仅需反射填充 `vals`
	// 通过 sync.Once 在首次拿到表名后构建；不同实例表名不一致时以首次为准
	sqlOnce       sync.Once
	insertSQL     string // INSERT INTO table (cols) VALUES (?, ?, ?)
	updateAllSQL  string // UPDATE table SET col = ?, ...
	updateByIDSQL string // UPDATE table SET col = ?, ... WHERE pk = ?；无 PK 或无非 PK 列时为空
	deleteByIDSQL string // DELETE FROM table WHERE pk = ?；无 PK 时为空
	selectByIDSQL string // SELECT * FROM table WHERE pk = ?；无 PK 时回退到 "id"
}

// metaCache 类型元数据缓存，使用 sync.Map 保证并发安全
var metaCache sync.Map

// getFieldMeta 获取或创建类型元数据
func getFieldMeta(t reflect.Type) *fieldMeta {
	// 尝试从缓存获取
	if meta, ok := metaCache.Load(t); ok {
		return meta.(*fieldMeta)
	}

	// 缓存未命中，创建新元数据
	meta := &fieldMeta{
		columns:    make([]string, 0, t.NumField()),
		fieldInfos: make([]fieldInfo, 0, t.NumField()),
		idIndex:    -1,
	}

	// 扫描所有字段
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		tag := field.Tag.Get("db")
		if tag == "-" || tag == "" {
			continue
		}

		// 记录字段信息
		meta.fieldInfos = append(meta.fieldInfos, fieldInfo{tag: tag, fieldIdx: i})
		meta.columns = append(meta.columns, tag)

		// 识别主键：db:"id" 是约定的主键标签
		if tag == "id" {
			meta.idIndex = i
			meta.pkColumn = tag
		}
	}

	// 存入缓存
	actual, _ := metaCache.LoadOrStore(t, meta)
	return actual.(*fieldMeta)
}

// ensureSQL 懒构建 SQL 模板（仅执行一次，线程安全）
// 模板只包含占位符 `?`，运行时无需再次拼接 SQL
// tableName 在首次调用时确定并缓存；同一类型跨实例表名应保持一致
func (m *fieldMeta) ensureSQL(tableName string) {
	if m == nil || len(m.columns) == 0 {
		return
	}
	m.sqlOnce.Do(func() {
		m.tableName = tableName
		columnCount := len(m.columns)

		// 预拼接 "?, ?, ?, ..." 形式的占位符串
		placeholders := strings.TrimRight(strings.Repeat("?,", columnCount), ",")
		// 预拼接 "col1, col2, col3" 形式的列名串
		colList := strings.Join(m.columns, ", ")

		// INSERT INTO table (col1, col2) VALUES (?, ?, ?)
		var b strings.Builder
		b.Grow(20 + len(tableName) + len(colList) + len(placeholders))
		b.WriteString("INSERT INTO ")
		b.WriteString(tableName)
		b.WriteString(" (")
		b.WriteString(colList)
		b.WriteString(") VALUES (")
		b.WriteString(placeholders)
		b.WriteString(")")
		m.insertSQL = b.String()

		// UPDATE table SET col1 = ?, col2 = ?
		b.Reset()
		b.Grow(20 + len(tableName) + columnCount*8)
		b.WriteString("UPDATE ")
		b.WriteString(tableName)
		b.WriteString(" SET ")
		for i, col := range m.columns {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(col)
			b.WriteString(" = ?")
		}
		m.updateAllSQL = b.String()

		// DELETE FROM table WHERE pk = ?
		if m.pkColumn != "" {
			var db strings.Builder
			db.Grow(16 + len(tableName) + len(m.pkColumn))
			db.WriteString("DELETE FROM ")
			db.WriteString(tableName)
			db.WriteString(" WHERE ")
			db.WriteString(m.pkColumn)
			db.WriteString(" = ?")
			m.deleteByIDSQL = db.String()
		}

		// SELECT * FROM table WHERE pk = ?
		pk := m.pkColumn
		if pk == "" {
			pk = "id"
		}
		var sb strings.Builder
		sb.Grow(20 + len(tableName) + len(pk))
		sb.WriteString("SELECT * FROM ")
		sb.WriteString(tableName)
		sb.WriteString(" WHERE ")
		sb.WriteString(pk)
		sb.WriteString(" = ?")
		m.selectByIDSQL = sb.String()

		// UPDATE table SET col1 = ?, ... WHERE pk = ?
		// 仅在存在主键且至少有一个非主键列时构建
		if m.pkColumn == "" {
			return
		}
		hasNonPK := false
		for _, col := range m.columns {
			if col != m.pkColumn {
				hasNonPK = true
				break
			}
		}
		if !hasNonPK {
			return
		}
		var ub strings.Builder
		ub.Grow(20 + len(tableName) + columnCount*8 + len(m.pkColumn))
		ub.WriteString("UPDATE ")
		ub.WriteString(tableName)
		ub.WriteString(" SET ")
		first := true
		for _, col := range m.columns {
			if col == m.pkColumn {
				continue
			}
			if !first {
				ub.WriteString(", ")
			}
			first = false
			ub.WriteString(col)
			ub.WriteString(" = ?")
		}
		ub.WriteString(" WHERE ")
		ub.WriteString(m.pkColumn)
		ub.WriteString(" = ?")
		m.updateByIDSQL = ub.String()
	})
}

// fieldScanner 字段扫描工具
// 用于解析模型的 db tag 并构建 SQL
type fieldScanner struct {
	model     IModel
	modelType reflect.Type
	modelVal  reflect.Value
	table     string
	meta      *fieldMeta
}

// newFieldScanner 创建字段扫描器
func newFieldScanner(model IModel) *fieldScanner {
	if model == nil {
		return &fieldScanner{table: ""}
	}

	modelVal := reflect.ValueOf(model)
	if modelVal.Kind() == reflect.Ptr && modelVal.IsNil() {
		return &fieldScanner{table: ""}
	}

	modelType := modelVal.Type()

	// 解指针
	if modelType.Kind() == reflect.Ptr {
		modelVal = modelVal.Elem()
		modelType = modelType.Elem()
	}

	// 获取类型元数据（带缓存）
	meta := getFieldMeta(modelType)

	table := model.TableName()
	// 懒构建 SQL 模板（仅首次执行，线程安全）
	meta.ensureSQL(table)

	return &fieldScanner{
		model:     model,
		modelType: modelType,
		modelVal:  modelVal,
		table:     table,
		meta:      meta,
	}
}

// fieldValue 是 dbFields() 的返回元素，对外暴露字段名与运行时值
type fieldValue struct {
	tag   string
	value any
}

// dbFields 返回所有带 db tag 的字段信息
func (fs *fieldScanner) dbFields() []fieldValue {
	if fs.table == "" || fs.meta == nil {
		return nil
	}

	fields := make([]fieldValue, len(fs.meta.fieldInfos))
	for i, info := range fs.meta.fieldInfos {
		fields[i] = fieldValue{
			tag:   info.tag,
			value: fs.modelVal.Field(info.fieldIdx).Interface(),
		}
	}
	return fields
}

// primaryKey 返回主键的列名、运行时值与是否存在
// 用于 Save/First/DeleteByID 等需要按主键操作的方法
func (fs *fieldScanner) primaryKey() (col string, val any, ok bool) {
	if fs.meta == nil || fs.meta.idIndex < 0 || fs.meta.pkColumn == "" {
		return "", nil, false
	}
	return fs.meta.pkColumn, fs.modelVal.Field(fs.meta.idIndex).Interface(), true
}

// buildInsertSQL 构建 INSERT SQL
// 返回：INSERT INTO table (col1, col2, ...) VALUES (?, ?, ...)
// SQL 模板已在 fieldMeta 中预构建，此处仅反射填充 vals
func (fs *fieldScanner) buildInsertSQL() (string, []any, error) {
	if fs.meta == nil || len(fs.meta.columns) == 0 {
		return "", nil, fmt.Errorf("no columns to insert for table %s", fs.table)
	}

	vals := make([]any, len(fs.meta.fieldInfos))
	for i, info := range fs.meta.fieldInfos {
		vals[i] = fs.modelVal.Field(info.fieldIdx).Interface()
	}

	return fs.meta.insertSQL, vals, nil
}

// buildUpdateSQL 构建 UPDATE SQL (不含 WHERE)
// 返回：UPDATE table SET col1 = ?, col2 = ?, ...
// SQL 模板已在 fieldMeta 中预构建，此处仅反射填充 vals
func (fs *fieldScanner) buildUpdateSQL() (string, []any, error) {
	if fs.meta == nil || fs.meta.updateAllSQL == "" {
		return "", nil, fmt.Errorf("no columns to update for table %s", fs.table)
	}

	vals := make([]any, len(fs.meta.fieldInfos))
	for i, info := range fs.meta.fieldInfos {
		vals[i] = fs.modelVal.Field(info.fieldIdx).Interface()
	}

	return fs.meta.updateAllSQL, vals, nil
}

// buildUpdateByIDSQL 构建带 WHERE pk = ? 的 UPDATE SQL
// SQL 模板已在 fieldMeta 中预构建（排除主键列），此处仅反射填充 vals 并追加 id
func (fs *fieldScanner) buildUpdateByIDSQL(id any) (string, []any, error) {
	if fs.meta == nil || fs.meta.pkColumn == "" {
		return "", nil, fmt.Errorf("no primary key (db:\"id\") field found in model %s", fs.table)
	}
	if fs.meta.updateByIDSQL == "" {
		return "", nil, fmt.Errorf("no columns to update for table %s (only pk field)", fs.table)
	}

	// 计算非主键字段数
	nonPKCount := 0
	for _, info := range fs.meta.fieldInfos {
		if info.tag != fs.meta.pkColumn {
			nonPKCount++
		}
	}
	vals := make([]any, 0, nonPKCount+1)
	for _, info := range fs.meta.fieldInfos {
		if info.tag == fs.meta.pkColumn {
			continue
		}
		vals = append(vals, fs.modelVal.Field(info.fieldIdx).Interface())
	}

	if id != nil {
		vals = append(vals, id)
	} else if fs.meta.idIndex >= 0 {
		vals = append(vals, fs.modelVal.Field(fs.meta.idIndex).Interface())
	} else {
		return "", nil, fmt.Errorf("no id provided and no id field found in model %s", fs.table)
	}

	return fs.meta.updateByIDSQL, vals, nil
}

// buildDeleteByIDSQL 构建按主键删除的 DELETE SQL
// SQL 模板已在 fieldMeta 中预构建
func (fs *fieldScanner) buildDeleteByIDSQL(id any) (string, []any, error) {
	if fs.table == "" {
		return "", nil, fmt.Errorf("no table name for delete by id")
	}
	if fs.meta == nil || fs.meta.deleteByIDSQL == "" {
		return "", nil, fmt.Errorf("no primary key (db:\"id\") field found in model %s", fs.table)
	}
	return fs.meta.deleteByIDSQL, []any{id}, nil
}

// buildDeleteSQL 构建 DELETE SQL
func (fs *fieldScanner) buildDeleteSQL(where string, args ...any) (string, []any, error) {
	var builder strings.Builder
	builder.Grow(16 + len(fs.table) + len(where))
	builder.WriteString("DELETE FROM ")
	builder.WriteString(fs.table)
	if where != "" {
		builder.WriteString(" WHERE ")
		builder.WriteString(where)
	}
	return builder.String(), args, nil
}

// buildSelectByIDSQL 构建按主键查询的 SELECT SQL
// SQL 模板已在 fieldMeta 中预构建（缺少主键时回退到 "id"）
func (fs *fieldScanner) buildSelectByIDSQL() string {
	if fs.meta == nil || fs.meta.selectByIDSQL == "" {
		// 极端情况：meta 还未初始化（理论上不会发生，保留兜底）
		pk := "id"
		if fs.meta != nil && fs.meta.pkColumn != "" {
			pk = fs.meta.pkColumn
		}
		return "SELECT * FROM " + fs.table + " WHERE " + pk + " = ?"
	}
	return fs.meta.selectByIDSQL
}
