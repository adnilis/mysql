package plugins

import (
	"reflect"
	"strings"
	"testing"
)

// scannerTestModel 是 scanner 测试用模型，覆盖：
//   - 带 db:"id" 主键标签
//   - 普通 db tag
//   - db:"-" 跳过标签
//   - 无 tag 未导出字段
type scannerTestModel struct {
	ID         int    `db:"id"`
	Name       string `db:"name"`
	Age        int    `db:"age"`
	SkipMe     string `db:"-"`
	internal   int
	NoTagField string
}

func (m *scannerTestModel) TableName() string { return "scanner_test" }

// scannerNoIDModel 缺少 db:"id" 主键
type scannerNoIDModel struct {
	UID  int    `db:"uid"`
	Name string `db:"name"`
}

func (m *scannerNoIDModel) TableName() string { return "no_id" }

// TestNewFieldScanner_IModel
func TestNewFieldScanner_IModel(t *testing.T) {
	fs := newFieldScanner(&scannerTestModel{ID: 1, Name: "a"})
	if fs.table != "scanner_test" {
		t.Errorf("table = %q, want %q", fs.table, "scanner_test")
	}
	if fs.meta == nil {
		t.Fatal("meta is nil")
	}
}

// TestNewFieldScanner_NilModel
func TestNewFieldScanner_NilModel(t *testing.T) {
	fs := newFieldScanner(nil)
	if fs.table != "" {
		t.Errorf("nil model should give empty table, got %q", fs.table)
	}
}

// TestNewFieldScanner_NilPtr
func TestNewFieldScanner_NilPtr(t *testing.T) {
	var m *scannerTestModel
	fs := newFieldScanner(m)
	if fs.table != "" {
		t.Errorf("nil pointer should give empty table, got %q", fs.table)
	}
}

// TestDBFields 验证只收集带 db tag 的字段，"-" 与 无 tag 跳过
func TestDBFields(t *testing.T) {
	fs := newFieldScanner(&scannerTestModel{ID: 1, Name: "bob", Age: 30, SkipMe: "ignored"})
	fields := fs.dbFields()

	wantTags := []string{"id", "name", "age"}
	if len(fields) != len(wantTags) {
		t.Fatalf("got %d fields, want %d (%v)", len(fields), len(wantTags), fields)
	}
	for i, f := range fields {
		if f.tag != wantTags[i] {
			t.Errorf("field[%d].tag = %q, want %q", i, f.tag, wantTags[i])
		}
	}

	// 验证 value 值
	if fields[0].value != 1 {
		t.Errorf("id value = %v, want 1", fields[0].value)
	}
	if fields[1].value != "bob" {
		t.Errorf("name value = %v, want \"bob\"", fields[1].value)
	}
}

// TestDBFields_EmptyTable
func TestDBFields_EmptyTable(t *testing.T) {
	fs := newFieldScanner(nil)
	if got := fs.dbFields(); got != nil {
		t.Errorf("nil scanner.dbFields() = %v, want nil", got)
	}
}

// TestPrimaryKey_Found
func TestPrimaryKey_Found(t *testing.T) {
	fs := newFieldScanner(&scannerTestModel{ID: 42})
	col, val, ok := fs.primaryKey()
	if !ok {
		t.Fatal("primaryKey() should report found")
	}
	if col != "id" {
		t.Errorf("col = %q, want %q", col, "id")
	}
	if val != 42 {
		t.Errorf("val = %v, want 42", val)
	}
}

// TestPrimaryKey_NotFound 没有 db:"id" 标签
func TestPrimaryKey_NotFound(t *testing.T) {
	fs := newFieldScanner(&scannerNoIDModel{UID: 7})
	if _, _, ok := fs.primaryKey(); ok {
		t.Error("primaryKey() should NOT find pk on a model without db:\"id\"")
	}
}

// TestBuildInsertSQL
func TestBuildInsertSQL(t *testing.T) {
	fs := newFieldScanner(&scannerTestModel{ID: 1, Name: "x", Age: 18})
	q, vals, err := fs.buildInsertSQL()
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(q, "INSERT INTO scanner_test") {
		t.Errorf("query missing INSERT prefix: %q", q)
	}
	if !strings.Contains(q, "id") || !strings.Contains(q, "name") || !strings.Contains(q, "age") {
		t.Errorf("query missing columns: %q", q)
	}
	if len(vals) != 3 {
		t.Errorf("expected 3 values, got %d", len(vals))
	}
}

// TestBuildUpdateSQL
func TestBuildUpdateSQL(t *testing.T) {
	fs := newFieldScanner(&scannerTestModel{ID: 1, Name: "y", Age: 22})
	q, vals, err := fs.buildUpdateSQL()
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !strings.HasPrefix(q, "UPDATE scanner_test SET ") {
		t.Errorf("query prefix wrong: %q", q)
	}
	if len(vals) != 3 {
		t.Errorf("expected 3 values, got %d", len(vals))
	}
}

// TestBuildUpdateByIDSQL_WithExplicitID 显式传 id（不使用 model.ID）
func TestBuildUpdateByIDSQL_WithExplicitID(t *testing.T) {
	fs := newFieldScanner(&scannerTestModel{ID: 1, Name: "z", Age: 33})
	q, vals, err := fs.buildUpdateByIDSQL(99)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(q, "WHERE id = ?") {
		t.Errorf("missing WHERE id = ?: %q", q)
	}
	// 排除 id 列后：name, age, + id 参数 = 3
	if len(vals) != 3 {
		t.Errorf("expected 3 values (excluding id from SET, including id at end), got %d: %v", len(vals), vals)
	}
	if vals[len(vals)-1] != 99 {
		t.Errorf("last arg should be id=99, got %v", vals[len(vals)-1])
	}
}

// TestBuildUpdateByIDSQL_NilUsesModelID 不传 id 时使用模型的 ID
func TestBuildUpdateByIDSQL_NilUsesModelID(t *testing.T) {
	fs := newFieldScanner(&scannerTestModel{ID: 7, Name: "x", Age: 1})
	_, vals, err := fs.buildUpdateByIDSQL(nil)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if vals[len(vals)-1] != 7 {
		t.Errorf("last arg should be model.ID=7, got %v", vals[len(vals)-1])
	}
}

// TestBuildUpdateByIDSQL_NoPKFails
func TestBuildUpdateByIDSQL_NoPKFails(t *testing.T) {
	fs := newFieldScanner(&scannerNoIDModel{UID: 1, Name: "x"})
	_, _, err := fs.buildUpdateByIDSQL(1)
	if err == nil {
		t.Error("expected error for model without pk")
	}
}

// TestBuildDeleteSQL_NoWhere
func TestBuildDeleteSQL_NoWhere(t *testing.T) {
	fs := newFieldScanner(&scannerTestModel{})
	q, _, err := fs.buildDeleteSQL("")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if q != "DELETE FROM scanner_test" {
		t.Errorf("got %q", q)
	}
}

// TestBuildDeleteSQL_WithWhere
func TestBuildDeleteSQL_WithWhere(t *testing.T) {
	fs := newFieldScanner(&scannerTestModel{})
	q, vals, err := fs.buildDeleteSQL("name = ?", "x")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	want := "DELETE FROM scanner_test WHERE name = ?"
	if q != want {
		t.Errorf("got %q, want %q", q, want)
	}
	if len(vals) != 1 || vals[0] != "x" {
		t.Errorf("vals = %v", vals)
	}
}

// TestBuildDeleteByIDSQL
func TestBuildDeleteByIDSQL(t *testing.T) {
	fs := newFieldScanner(&scannerTestModel{ID: 1})
	q, vals, err := fs.buildDeleteByIDSQL(55)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	want := "DELETE FROM scanner_test WHERE id = ?"
	if q != want {
		t.Errorf("got %q, want %q", q, want)
	}
	if len(vals) != 1 || vals[0] != 55 {
		t.Errorf("vals = %v, want [55]", vals)
	}
}

// TestBuildDeleteByIDSQL_NoPKFails
func TestBuildDeleteByIDSQL_NoPKFails(t *testing.T) {
	fs := newFieldScanner(&scannerNoIDModel{UID: 1})
	if _, _, err := fs.buildDeleteByIDSQL(1); err == nil {
		t.Error("expected error for model without pk")
	}
}

// TestBuildSelectByIDSQL
func TestBuildSelectByIDSQL(t *testing.T) {
	fs := newFieldScanner(&scannerTestModel{})
	q := fs.buildSelectByIDSQL()
	want := "SELECT * FROM scanner_test WHERE id = ?"
	if q != want {
		t.Errorf("got %q, want %q", q, want)
	}
}

// TestBuildSelectByIDSQL_FallbackToId 模型缺主键时回退 "id"
func TestBuildSelectByIDSQL_FallbackToId(t *testing.T) {
	fs := newFieldScanner(&scannerNoIDModel{})
	q := fs.buildSelectByIDSQL()
	if !strings.Contains(q, "WHERE id = ?") {
		t.Errorf("fallback failed: %q", q)
	}
}

// TestFieldMetaCache 两次同类型调用应返回同一 meta 指针
func TestFieldMetaCache(t *testing.T) {
	fs1 := newFieldScanner(&scannerTestModel{})
	fs2 := newFieldScanner(&scannerTestModel{Name: "different"})
	if fs1.meta != fs2.meta {
		t.Error("expected same *fieldMeta from cache")
	}
}

// TestFieldMetaCache_DifferentTypes
func TestFieldMetaCache_DifferentTypes(t *testing.T) {
	fs1 := newFieldScanner(&scannerTestModel{})
	fs2 := newFieldScanner(&scannerNoIDModel{})
	if fs1.meta == fs2.meta {
		t.Error("expected different *fieldMeta for different types")
	}
}

// TestFieldInfo_NamedType 验证 fieldInfo 类型存在且字段名正确
func TestFieldInfo_NamedType(t *testing.T) {
	fi := fieldInfo{tag: "x", fieldIdx: 5}
	if fi.tag != "x" {
		t.Errorf("fieldInfo.tag broken")
	}
	if fi.fieldIdx != 5 {
		t.Errorf("fieldInfo.fieldIdx broken")
	}
	// 类型应可用于切片
	infos := []fieldInfo{{tag: "a"}, {tag: "b"}}
	if len(infos) != 2 {
		t.Error("fieldInfo slice broken")
	}
}

// TestFieldMeta_PKColumnRecorded
func TestFieldMeta_PKColumnRecorded(t *testing.T) {
	fs := newFieldScanner(&scannerTestModel{})
	if fs.meta.pkColumn != "id" {
		t.Errorf("pkColumn = %q, want %q", fs.meta.pkColumn, "id")
	}
	if fs.meta.idIndex < 0 {
		t.Errorf("idIndex = %d, want >= 0", fs.meta.idIndex)
	}

	fs2 := newFieldScanner(&scannerNoIDModel{})
	if fs2.meta.pkColumn != "" {
		t.Errorf("pkColumn for no-id model = %q, want empty", fs2.meta.pkColumn)
	}
}

// TestPrimaryKey_ValueReflectsModelValue 验证 primaryKey() 跟随模型当前值
func TestPrimaryKey_ValueReflectsModelValue(t *testing.T) {
	m := &scannerTestModel{ID: 100, Name: "k"}
	fs := newFieldScanner(m)
	_, val, ok := fs.primaryKey()
	if !ok {
		t.Fatal("expected pk")
	}
	// 反射出的值必须等于模型当前 ID
	if v := reflect.ValueOf(val).Int(); v != 100 {
		t.Errorf("pk value = %d, want 100", v)
	}
}
