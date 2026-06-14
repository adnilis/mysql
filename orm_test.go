package plugins

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// orm CRUD 测试集中放在此文件，配合 mysql_test.go 的 newTestPlugin/newMockDB 助手

// TestORM_Update
func TestORM_Update(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	model := &testModel{ID: 1, Name: "u"}
	mock.ExpectExec("UPDATE test_models SET .* WHERE name = \\?").
		WithArgs(1, "u", "old").
		WillReturnResult(sqlmock.NewResult(0, 1))

	n, err := plugin.Update(context.Background(), model, "name = ?", "old")
	if err != nil {
		t.Fatalf("update failed: %v", err)
	}
	if n != 1 {
		t.Errorf("rows = %d, want 1", n)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled: %v", err)
	}
}

// TestORM_Delete
func TestORM_Delete(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	model := &testModel{}
	mock.ExpectExec("DELETE FROM test_models WHERE id = \\?").
		WithArgs(5).
		WillReturnResult(sqlmock.NewResult(0, 1))

	n, err := plugin.Delete(context.Background(), model, "id = ?", 5)
	if err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	if n != 1 {
		t.Errorf("rows = %d, want 1", n)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled: %v", err)
	}
}

// TestORM_GetByID_Found
func TestORM_GetByID_Found(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	rows := sqlmock.NewRows([]string{"id", "name"}).AddRow(1, "found")
	mock.ExpectQuery("SELECT \\* FROM test_models WHERE id = \\?").
		WithArgs(1).
		WillReturnRows(rows)

	m := &testModel{}
	if err := plugin.GetByID(context.Background(), m, 1); err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}
	if m.Name != "found" {
		t.Errorf("name = %q, want %q", m.Name, "found")
	}
}

// TestORM_GetByID_NotFound
func TestORM_GetByID_NotFound(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	mock.ExpectQuery("SELECT \\* FROM test_models WHERE id = \\?").
		WithArgs(99).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name"}))

	m := &testModel{}
	err := plugin.GetByID(context.Background(), m, 99)
	if !errors.Is(err, ErrModelNotFound) {
		t.Errorf("expected ErrModelNotFound, got %v", err)
	}
}

// TestORM_UpdateByID
func TestORM_UpdateByID(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	model := &testModel{ID: 3, Name: "renamed"}
	mock.ExpectExec("UPDATE test_models SET name = \\? WHERE id = \\?").
		WithArgs("renamed", 3).
		WillReturnResult(sqlmock.NewResult(0, 1))

	n, err := plugin.UpdateByID(context.Background(), model, 3)
	if err != nil {
		t.Fatalf("UpdateByID failed: %v", err)
	}
	if n != 1 {
		t.Errorf("rows = %d, want 1", n)
	}
}

// TestORM_DeleteByID 验证使用 scanner 解析的主键列
func TestORM_DeleteByID(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	mock.ExpectExec("DELETE FROM test_models WHERE id = \\?").
		WithArgs(7).
		WillReturnResult(sqlmock.NewResult(0, 1))

	n, err := plugin.DeleteByID(context.Background(), &testModel{}, 7)
	if err != nil {
		t.Fatalf("DeleteByID failed: %v", err)
	}
	if n != 1 {
		t.Errorf("rows = %d, want 1", n)
	}
}

// TestORM_Exists_True
func TestORM_Exists_True(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	mock.ExpectQuery("SELECT COUNT").
		WithArgs(1).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	ok, err := plugin.Exists(context.Background(), "users", "id = ?", 1)
	if err != nil {
		t.Fatalf("Exists failed: %v", err)
	}
	if !ok {
		t.Error("expected exists=true")
	}
}

// TestORM_Exists_False
func TestORM_Exists_False(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	mock.ExpectQuery("SELECT COUNT").
		WithArgs(99).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

	ok, err := plugin.Exists(context.Background(), "users", "id = ?", 99)
	if err != nil {
		t.Fatalf("Exists failed: %v", err)
	}
	if ok {
		t.Error("expected exists=false")
	}
}

// TestORM_Get_Found
func TestORM_Get_Found(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	rows := sqlmock.NewRows([]string{"id", "name"}).AddRow(1, "a")
	mock.ExpectQuery("SELECT \\* FROM test_models").
		WithArgs(1).
		WillReturnRows(rows)

	m := &testModel{}
	if err := plugin.Get(context.Background(), m, "SELECT * FROM test_models WHERE id = ?", 1); err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if m.Name != "a" {
		t.Errorf("name = %q", m.Name)
	}
}

// TestORM_Get_NotFound
func TestORM_Get_NotFound(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	mock.ExpectQuery("SELECT \\* FROM test_models").
		WithArgs(99).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name"}))

	m := &testModel{}
	err := plugin.Get(context.Background(), m, "SELECT * FROM test_models WHERE id = ?", 99)
	if !errors.Is(err, ErrModelNotFound) {
		t.Errorf("expected ErrModelNotFound, got %v", err)
	}
}

// TestORM_Get_NotFound_HasWrapContext 验证 R01 错误统一:Get 未命中也走 wrapMySQLError
// 调用方可通过 errors.As 拿到 *MySQLError,读取 Op() 判断操作类型
func TestORM_Get_NotFound_HasWrapContext(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	mock.ExpectQuery("SELECT \\* FROM test_models").
		WithArgs(99).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name"}))

	m := &testModel{}
	err := plugin.Get(context.Background(), m, "SELECT * FROM test_models WHERE id = ?", 99)

	// 必须能 errors.Is 找到 ErrModelNotFound
	if !errors.Is(err, ErrModelNotFound) {
		t.Errorf("errors.Is should match ErrModelNotFound, got %v", err)
	}

	// 必须能 errors.As 拿到 MySQLError,且 Op() == "get"
	var mErr *MySQLError
	if !errors.As(err, &mErr) {
		t.Fatal("errors.As should match *MySQLError")
	}
	if mErr.Op() != "get" {
		t.Errorf("Op() = %q, want %q", mErr.Op(), "get")
	}
}

// TestORM_Get_NilDest 验证 nil dest 防 panic
func TestORM_Get_NilDest(t *testing.T) {
	plugin, _ := newTestPlugin(t)
	err := plugin.Get(context.Background(), nil, "SELECT 1")
	if !errors.Is(err, ErrInvalidModel) {
		t.Errorf("expected ErrInvalidModel for nil dest, got %v", err)
	}
}

// TestORM_Select_NilDest
func TestORM_Select_NilDest(t *testing.T) {
	plugin, _ := newTestPlugin(t)
	err := plugin.Select(context.Background(), nil, "SELECT 1")
	if !errors.Is(err, ErrInvalidModel) {
		t.Errorf("expected ErrInvalidModel for nil dest, got %v", err)
	}
}

// TestORM_ExecReturningID
func TestORM_ExecReturningID(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	mock.ExpectExec("INSERT INTO test_models").
		WillReturnResult(sqlmock.NewResult(42, 1))

	id, err := plugin.ExecReturningID(context.Background(), "INSERT INTO test_models (name) VALUES ('x')")
	if err != nil {
		t.Fatalf("ExecReturningID failed: %v", err)
	}
	if id != 42 {
		t.Errorf("id = %d, want 42", id)
	}
}

// TestORM_Exec
func TestORM_Exec(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	mock.ExpectExec("DELETE FROM test_models").
		WillReturnResult(sqlmock.NewResult(0, 3))

	n, err := plugin.Exec(context.Background(), "DELETE FROM test_models WHERE x = 1")
	if err != nil {
		t.Fatalf("Exec failed: %v", err)
	}
	if n != 3 {
		t.Errorf("rows = %d, want 3", n)
	}
}

// TestORM_Save_Insert id=0 走 INSERT 路径
func TestORM_Save_Insert(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	mock.ExpectExec("INSERT INTO test_models").
		WithArgs(0, "new").
		WillReturnResult(sqlmock.NewResult(1, 1))

	model := &testModel{ID: 0, Name: "new"}
	if err := plugin.Save(context.Background(), model); err != nil {
		t.Fatalf("Save failed: %v", err)
	}
}

// TestORM_Save_Update id!=0 走 UPDATE 路径
func TestORM_Save_Update(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	mock.ExpectExec("UPDATE test_models SET name = \\? WHERE id = \\?").
		WithArgs("updated", 5).
		WillReturnResult(sqlmock.NewResult(0, 1))

	model := &testModel{ID: 5, Name: "updated"}
	if err := plugin.Save(context.Background(), model); err != nil {
		t.Fatalf("Save failed: %v", err)
	}
}

// TestORM_Save_NilModel
func TestORM_Save_NilModel(t *testing.T) {
	plugin, _ := newTestPlugin(t)
	if err := plugin.Save(context.Background(), nil); !errors.Is(err, ErrInvalidModel) {
		t.Errorf("expected ErrInvalidModel, got %v", err)
	}
}

// TestORM_First
func TestORM_First(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	rows := sqlmock.NewRows([]string{"id", "name"}).AddRow(1, "first")
	mock.ExpectQuery("SELECT \\* FROM test_models WHERE id = \\? LIMIT 1").
		WithArgs(1).
		WillReturnRows(rows)

	m := &testModel{}
	if err := plugin.First(context.Background(), m, 1); err != nil {
		t.Fatalf("First failed: %v", err)
	}
	if m.Name != "first" {
		t.Errorf("name = %q", m.Name)
	}
}

// TestORM_First_UnknownDest 不能推断表名时返回错误
func TestORM_First_UnknownDest(t *testing.T) {
	plugin, _ := newTestPlugin(t)
	x := 1
	err := plugin.First(context.Background(), &x, 1)
	if err == nil {
		t.Error("expected error for non-struct dest")
	}
}

// TestORM_Find
func TestORM_Find(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	rows := sqlmock.NewRows([]string{"id", "name"}).AddRow(1, "a").AddRow(2, "b")
	mock.ExpectQuery("SELECT \\* FROM test_models").
		WillReturnRows(rows)

	var results []testModel
	if err := plugin.Find(context.Background(), &results, "SELECT * FROM test_models"); err != nil {
		t.Fatalf("Find failed: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("got %d rows, want 2", len(results))
	}
}

// TestORM_Create 包装 Insert
func TestORM_Create(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	mock.ExpectExec("INSERT INTO test_models").
		WithArgs(0, "created").
		WillReturnResult(sqlmock.NewResult(1, 1))

	if err := plugin.Create(context.Background(), &testModel{Name: "created"}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
}

// TestORM_Count
func TestORM_Count(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	mock.ExpectQuery("SELECT COUNT").
		WithArgs(18).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(7))

	n, err := plugin.Count(context.Background(), "users", "age > ?", 18)
	if err != nil {
		t.Fatalf("Count failed: %v", err)
	}
	if n != 7 {
		t.Errorf("count = %d, want 7", n)
	}
}

// TestORM_Count_NoWhere
func TestORM_Count_NoWhere(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	mock.ExpectQuery("SELECT COUNT").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(42))

	n, err := plugin.Count(context.Background(), "users", "")
	if err != nil {
		t.Fatalf("Count failed: %v", err)
	}
	if n != 42 {
		t.Errorf("count = %d, want 42", n)
	}
}

// TestORM_Count_InvalidTable 验证 Phase 2.9 防注入
func TestORM_Count_InvalidTable(t *testing.T) {
	plugin, _ := newTestPlugin(t)
	_, err := plugin.Count(context.Background(), "users; DROP TABLE users;--", "")
	if !errors.Is(err, ErrInvalidModel) {
		t.Errorf("expected ErrInvalidModel, got %v", err)
	}
}

// TestORM_Query 原生 Query 路径
func TestORM_Query(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	rows := sqlmock.NewRows([]string{"id", "name"}).AddRow(1, "a")
	mock.ExpectQuery("SELECT \\* FROM test_models WHERE id = \\?").
		WithArgs(1).
		WillReturnRows(rows)

	var m testModel
	err := plugin.Query(context.Background(), "SELECT * FROM test_models WHERE id = ?", 1).First(&m)
	if err != nil {
		t.Fatalf("Query.First failed: %v", err)
	}
	if m.Name != "a" {
		t.Errorf("name = %q", m.Name)
	}
}

// TestORM_GetDB_NilWhenNotStarted
func TestORM_GetDB_NilWhenNotStarted(t *testing.T) {
	plugin := NewMySQLPlugin("uninit", &MySQLPluginConfig{
		Addr: "localhost:3306", User: "u", DBName: "d",
	})
	// 未 Start，db 是 nil
	_, err := plugin.getDB()
	if !errors.Is(err, ErrMySQLNotEnabled) {
		t.Errorf("expected ErrMySQLNotEnabled, got %v", err)
	}

	// 进而所有 CRUD 都应返回 ErrMySQLNotEnabled
	_, err = plugin.Insert(context.Background(), &testModel{})
	if !errors.Is(err, ErrMySQLNotEnabled) {
		t.Errorf("Insert: expected ErrMySQLNotEnabled, got %v", err)
	}
}

// TestORM_Model_InvalidTable
func TestORM_Model_InvalidTable(t *testing.T) {
	plugin, _ := newTestPlugin(t)

	badModel := &badTableModel{}
	qr := plugin.Model(badModel)
	if !errors.Is(qr.err, ErrInvalidModel) {
		t.Errorf("expected ErrInvalidModel, got %v", qr.err)
	}
}

// TestORM_Model_NilModel
func TestORM_Model_NilModel(t *testing.T) {
	plugin, _ := newTestPlugin(t)
	qr := plugin.Model(nil)
	if !errors.Is(qr.err, ErrInvalidModel) {
		t.Errorf("expected ErrInvalidModel, got %v", qr.err)
	}
}

// badTableModel 用于测试 Model 对非法表名的拒绝
type badTableModel struct {
	ID int `db:"id"`
}

func (m *badTableModel) TableName() string { return "users; DROP TABLE users;--" }
