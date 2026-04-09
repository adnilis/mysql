package plugins

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
)

// fieldMeta 类型元数据缓存
// 缓存每种类型的字段信息，避免重复反射扫描
type fieldMeta struct {
	tableName  string     // 表名
	columns    []string   // 所有 db tag 列名
	idIndex    int        // id 字段索引，-1 表示不存在
	fieldInfos []struct { // 字段信息
		tag      string
		fieldIdx int
	}
	insertPlaceholders []string // INSERT 语句占位符
	updateSetParts     []string // UPDATE SET 子句片段
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
		columns:            make([]string, 0, t.NumField()),
		fieldInfos:         make([]struct{ tag string; fieldIdx int }, 0, t.NumField()),
		idIndex:            -1,
		insertPlaceholders: make([]string, 0, t.NumField()),
		updateSetParts:     make([]string, 0, t.NumField()),
	}

	// 扫描所有字段
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		tag := field.Tag.Get("db")
		if tag == "-" || tag == "" {
			continue
		}

		// 记录字段信息
		meta.fieldInfos = append(meta.fieldInfos, struct {
			tag      string
			fieldIdx int
		}{tag: tag, fieldIdx: i})
		meta.columns = append(meta.columns, tag)
		meta.insertPlaceholders = append(meta.insertPlaceholders, "?")
		meta.updateSetParts = append(meta.updateSetParts, fmt.Sprintf("%s = ?", tag))

		// 记录 id 字段索引
		if tag == "id" {
			meta.idIndex = i
		}
	}

	// 存入缓存
	actual, _ := metaCache.LoadOrStore(t, meta)
	return actual.(*fieldMeta)
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

	return &fieldScanner{
		model:     model,
		modelType: modelType,
		modelVal:  modelVal,
		table:     model.TableName(),
		meta:      meta,
	}
}

// dbFields 返回所有带 db tag 的字段信息
func (fs *fieldScanner) dbFields() []struct {
	tag   string
	value interface{}
} {
	if fs.table == "" || fs.meta == nil {
		return nil
	}

	fields := make([]struct {
		tag   string
		value interface{}
	}, len(fs.meta.fieldInfos))

	for i, info := range fs.meta.fieldInfos {
		fields[i] = struct {
			tag   string
			value interface{}
		}{
			tag:   info.tag,
			value: fs.modelVal.Field(info.fieldIdx).Interface(),
		}
	}

	return fields
}

// buildInsertSQL 构建 INSERT SQL
// 返回：INSERT INTO table (col1, col2, ...) VALUES (?, ?, ...)
func (fs *fieldScanner) buildInsertSQL() (string, []interface{}, error) {
	if fs.meta == nil || len(fs.meta.columns) == 0 {
		return "", nil, fmt.Errorf("no columns to insert for table %s", fs.table)
	}

	fields := fs.dbFields()
	vals := make([]interface{}, len(fields))
	for i, f := range fields {
		vals[i] = f.value
	}

	var builder strings.Builder
	builder.Grow(32 + len(fs.table) + len(fs.meta.columns)*8)
	builder.WriteString("INSERT INTO ")
	builder.WriteString(fs.table)
	builder.WriteString(" (")
	for i, col := range fs.meta.columns {
		if i > 0 {
			builder.WriteString(", ")
		}
		builder.WriteString(col)
	}
	builder.WriteString(") VALUES (")
	for i := range fs.meta.insertPlaceholders {
		if i > 0 {
			builder.WriteString(", ")
		}
		builder.WriteString(fs.meta.insertPlaceholders[i])
	}
	builder.WriteString(")")

	return builder.String(), vals, nil
}

// buildUpdateSQL 构建 UPDATE SQL (不含 WHERE)
// 返回：UPDATE table SET col1 = ?, col2 = ?, ...
func (fs *fieldScanner) buildUpdateSQL() (string, []interface{}, error) {
	if fs.meta == nil || len(fs.meta.updateSetParts) == 0 {
		return "", nil, fmt.Errorf("no columns to update for table %s", fs.table)
	}

	fields := fs.dbFields()
	vals := make([]interface{}, len(fields))
	for i, f := range fields {
		vals[i] = f.value
	}

	var builder strings.Builder
	builder.Grow(32 + len(fs.table) + len(fs.meta.updateSetParts)*8)
	builder.WriteString("UPDATE ")
	builder.WriteString(fs.table)
	builder.WriteString(" SET ")
	for i, part := range fs.meta.updateSetParts {
		if i > 0 {
			builder.WriteString(", ")
		}
		builder.WriteString(part)
	}

	return builder.String(), vals, nil
}

// buildUpdateByIDSQL 构建带 WHERE id = ? 的 UPDATE SQL
func (fs *fieldScanner) buildUpdateByIDSQL(id interface{}) (string, []interface{}, error) {
	fields := fs.dbFields()
	if len(fields) == 0 {
		return "", nil, fmt.Errorf("no columns to update for table %s", fs.table)
	}

	setParts := make([]string, 0, len(fs.meta.updateSetParts))
	vals := make([]interface{}, 0, len(fields))
	for _, f := range fields {
		if f.tag == "id" {
			continue
		}
		setParts = append(setParts, fmt.Sprintf("%s = ?", f.tag))
		vals = append(vals, f.value)
	}
	if len(setParts) == 0 {
		return "", nil, fmt.Errorf("no columns to update for table %s (only id field)", fs.table)
	}

	if id != nil {
		vals = append(vals, id)
	} else if fs.meta.idIndex >= 0 {
		vals = append(vals, fs.modelVal.Field(fs.meta.idIndex).Interface())
	} else {
		return "", nil, fmt.Errorf("no id provided and no id field found in model %s", fs.table)
	}

	var builder strings.Builder
	builder.Grow(48 + len(fs.table) + len(setParts)*8)
	builder.WriteString("UPDATE ")
	builder.WriteString(fs.table)
	builder.WriteString(" SET ")
	for i, part := range setParts {
		if i > 0 {
			builder.WriteString(", ")
		}
		builder.WriteString(part)
	}
	builder.WriteString(" WHERE id = ?")

	return builder.String(), vals, nil
}

// buildDeleteSQL 构建 DELETE SQL
func (fs *fieldScanner) buildDeleteSQL(where string, args ...interface{}) (string, []interface{}, error) {
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

// buildSelectByIDSQL 构建按 ID 查询的 SELECT SQL
func (fs *fieldScanner) buildSelectByIDSQL() string {
	var builder strings.Builder
	builder.Grow(32 + len(fs.table))
	builder.WriteString("SELECT * FROM ")
	builder.WriteString(fs.table)
	builder.WriteString(" WHERE id = ?")
	return builder.String()
}
