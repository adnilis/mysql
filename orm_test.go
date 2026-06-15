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

// TestUpsert_SingleChunk 验证单 chunk upsert 生成正确 ON DUPLICATE KEY UPDATE
// 默认 updateCols=nil 时:除 "id"/"ID" 外的所有列都更新(本例 k/v 两列都在 update 列表)
func TestUpsert_SingleChunk(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	mock.ExpectExec(`INSERT INTO config \(k, v\) VALUES \(\?,\?\), \(\?,\?\) ON DUPLICATE KEY UPDATE k = VALUES\(k\), v = VALUES\(v\)`).
		WithArgs("a", 1, "b", 2).
		WillReturnResult(sqlmock.NewResult(0, 2))

	rows := [][]any{
		{"a", 1},
		{"b", 2},
	}
	affected, err := plugin.Upsert(context.Background(), "config",
		[]string{"k", "v"}, rows, nil, 10)
	if err != nil {
		t.Fatalf("Upsert failed: %v", err)
	}
	if affected != 2 {
		t.Errorf("expected 2 affected, got %d", affected)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled: %v", err)
	}
}

// TestUpsert_DefaultSkipsID 验证 updateCols=nil 默认跳过 "id" / "ID" 列
func TestUpsert_DefaultSkipsID(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	mock.ExpectExec(`INSERT INTO users \(id, name\) VALUES \(\?,\?\) ON DUPLICATE KEY UPDATE name = VALUES\(name\)`).
		WithArgs(1, "alice").
		WillReturnResult(sqlmock.NewResult(0, 1))

	rows := [][]any{{1, "alice"}}
	_, err := plugin.Upsert(context.Background(), "users",
		[]string{"id", "name"}, rows, nil, 10)
	if err != nil {
		t.Fatalf("Upsert failed: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled: %v", err)
	}
}

// TestUpsert_ExplicitUpdateCols 验证显式指定 updateCols
func TestUpsert_ExplicitUpdateCols(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	mock.ExpectExec(`INSERT INTO t \(id, name, score\) VALUES \(\?,\?,\?\) ON DUPLICATE KEY UPDATE score = VALUES\(score\), name = VALUES\(name\)`).
		WithArgs(1, "alice", 100).
		WillReturnResult(sqlmock.NewResult(0, 1))

	rows := [][]any{{1, "alice", 100}}
	affected, err := plugin.Upsert(context.Background(), "t",
		[]string{"id", "name", "score"}, rows,
		[]string{"score", "name"}, 10)
	if err != nil {
		t.Fatalf("Upsert failed: %v", err)
	}
	if affected != 1 {
		t.Errorf("expected 1 affected, got %d", affected)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled: %v", err)
	}
}

// TestUpsert_InvalidTable 拒绝非法表名
func TestUpsert_InvalidTable(t *testing.T) {
	plugin, _ := newTestPlugin(t)

	_, err := plugin.Upsert(context.Background(), "evil; DROP", []string{"x"},
		[][]any{{1}}, nil, 10)
	if !errors.Is(err, ErrInvalidModel) {
		t.Errorf("expected ErrInvalidModel, got %v", err)
	}
}

// TestStats_QueryMetrics 验证内存级指标递增
func TestStats_QueryMetrics(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	mock.ExpectQuery(`SELECT \* FROM users`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).AddRow(1, "a").AddRow(2, "b"))

	var results []testModel
	err := plugin.Query(context.Background(), "SELECT * FROM users").Find(&results)
	if err != nil {
		t.Fatalf("Find failed: %v", err)
	}

	stats := plugin.Stats()
	if stats.QueryTotal < 1 {
		t.Errorf("QueryTotal should be >= 1, got %d", stats.QueryTotal)
	}
	if stats.RowsRead < 2 {
		t.Errorf("RowsRead should be >= 2, got %d", stats.RowsRead)
	}
}

// TestSaveOnConflict_Valid 验证 IModel 版的 ON DUPLICATE KEY UPDATE
func TestSaveOnConflict_Valid(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	mock.ExpectExec(`INSERT INTO test_models \(id, name\) VALUES \(\?,\?\) ON DUPLICATE KEY UPDATE name = VALUES\(name\)`).
		WithArgs(1, "alice").
		WillReturnResult(sqlmock.NewResult(0, 1))

	affected, err := plugin.SaveOnConflict(context.Background(),
		&testModel{ID: 1, Name: "alice"}, "id")
	if err != nil {
		t.Fatalf("SaveOnConflict failed: %v", err)
	}
	if affected != 1 {
		t.Errorf("expected 1 affected, got %d", affected)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled: %v", err)
	}
}

// TestSaveOnConflict_AllConflictColumns 验证所有列都是 conflict 时回退 INSERT IGNORE
func TestSaveOnConflict_AllConflictColumns(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	mock.ExpectExec(`INSERT IGNORE INTO test_models \(id, name\) VALUES \(\?,\?\)`).
		WithArgs(1, "alice").
		WillReturnResult(sqlmock.NewResult(0, 0))

	_, err := plugin.SaveOnConflict(context.Background(),
		&testModel{ID: 1, Name: "alice"}, "id", "name")
	if err != nil {
		t.Fatalf("SaveOnConflict all-conflict failed: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled: %v", err)
	}
}

// TestSaveOnConflict_NilModel nil 指针返回 ErrInvalidModel
func TestSaveOnConflict_NilModel(t *testing.T) {
	plugin, _ := newTestPlugin(t)

	_, err := plugin.SaveOnConflict(context.Background(), nil)
	if !errors.Is(err, ErrInvalidModel) {
		t.Errorf("expected ErrInvalidModel, got %v", err)
	}
}

// TestDescribeTable_Valid 验证 schema 自省 SQL
func TestDescribeTable_Valid(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	mock.ExpectQuery(`SELECT COLUMN_NAME.*FROM information_schema\.COLUMNS`).
		WithArgs("test_db", "users").
		WillReturnRows(sqlmock.NewRows([]string{
			"COLUMN_NAME", "DATA_TYPE", "COLUMN_TYPE", "IS_NULLABLE",
			"COLUMN_DEFAULT", "COLUMN_KEY", "COLUMN_COMMENT", "ORDINAL_POSITION",
		}).AddRow("id", "bigint", "bigint(20)", "NO", nil, "PRI", "primary key", 1).
			AddRow("name", "varchar", "varchar(64)", "YES", nil, "", "user name", 2))

	mock.ExpectQuery(`SELECT INDEX_NAME.*FROM information_schema\.STATISTICS`).
		WithArgs("test_db", "users").
		WillReturnRows(sqlmock.NewRows([]string{
			"INDEX_NAME", "COLUMN_NAME", "SEQ_IN_INDEX", "NON_UNIQUE",
		}).AddRow("PRIMARY", "id", 1, 0))

	info, err := plugin.DescribeTable(context.Background(), "users")
	if err != nil {
		t.Fatalf("DescribeTable failed: %v", err)
	}
	if info.TableName != "users" {
		t.Errorf("TableName = %q, want users", info.TableName)
	}
	if len(info.Columns) != 2 {
		t.Errorf("expected 2 columns, got %d", len(info.Columns))
	}
	if info.Columns[0].Name != "id" || info.Columns[0].Key != "PRI" {
		t.Errorf("id column wrong: %+v", info.Columns[0])
	}
	if info.Columns[1].Nullable != true {
		t.Errorf("name should be nullable")
	}
	if len(info.Indexes) != 1 || info.Indexes[0].Name != "PRIMARY" {
		t.Errorf("PRIMARY index missing: %+v", info.Indexes)
	}
}

// TestListTables_Valid 验证 list tables
func TestListTables_Valid(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	mock.ExpectQuery(`SELECT TABLE_NAME FROM information_schema\.TABLES`).
		WithArgs("test_db").
		WillReturnRows(sqlmock.NewRows([]string{"TABLE_NAME"}).
			AddRow("orders").
			AddRow("users"))

	tables, err := plugin.ListTables(context.Background())
	if err != nil {
		t.Fatalf("ListTables failed: %v", err)
	}
	if len(tables) != 2 || tables[0] != "orders" || tables[1] != "users" {
		t.Errorf("unexpected tables: %v", tables)
	}
}

// TestBulkUpdate_SingleChunk 验证单 chunk(<=16 行)单条 SQL CASE WHEN
func TestBulkUpdate_SingleChunk(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	mock.ExpectExec(`UPDATE inventory SET stock = CASE id WHEN \? THEN \? WHEN \? THEN \? WHEN \? THEN \? END WHERE id IN \(\?,\?,\?\)`).
		WithArgs(1, 100, 2, 200, 3, 300, 1, 2, 3).
		WillReturnResult(sqlmock.NewResult(0, 3))

	affected, err := plugin.BulkUpdate(context.Background(), "inventory", "id",
		[]any{1, 2, 3}, "stock", []any{100, 200, 300})
	if err != nil {
		t.Fatalf("BulkUpdate failed: %v", err)
	}
	if affected != 3 {
		t.Errorf("expected 3 affected, got %d", affected)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled: %v", err)
	}
}

// TestBulkUpdate_AutoChunk 验证 >16 行自动分片
func TestBulkUpdate_AutoChunk(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	// 20 行 → 2 chunk:[0:16] + [16:20]
	// 第一个 chunk:16 个 WHEN
	mock.ExpectExec(`UPDATE t SET score = CASE id WHEN \? THEN \? .* END WHERE id IN \(\?.*\)`).
		WillReturnResult(sqlmock.NewResult(0, 16))
	// 第二个 chunk:4 个 WHEN
	mock.ExpectExec(`UPDATE t SET score = CASE id WHEN \? THEN \? WHEN \? THEN \? WHEN \? THEN \? WHEN \? THEN \? END WHERE id IN \(\?,\?,\?,\?\)`).
		WillReturnResult(sqlmock.NewResult(0, 4))

	ids := make([]any, 20)
	values := make([]any, 20)
	for i := range ids {
		ids[i] = i + 1
		values[i] = (i + 1) * 10
	}
	affected, err := plugin.BulkUpdate(context.Background(), "t", "id", ids, "score", values)
	if err != nil {
		t.Fatalf("BulkUpdate failed: %v", err)
	}
	if affected != 20 {
		t.Errorf("expected 20 affected, got %d", affected)
	}
}

// TestBulkUpdate_Empty 空 ids 直接返回
func TestBulkUpdate_Empty(t *testing.T) {
	plugin, _ := newTestPlugin(t)

	affected, err := plugin.BulkUpdate(context.Background(), "t", "id", nil, "score", nil)
	if err != nil {
		t.Errorf("empty BulkUpdate should not error, got %v", err)
	}
	if affected != 0 {
		t.Errorf("expected 0 affected, got %d", affected)
	}
}

// TestBulkUpdate_LengthMismatch ids 与 values 长度不匹配
func TestBulkUpdate_LengthMismatch(t *testing.T) {
	plugin, _ := newTestPlugin(t)

	_, err := plugin.BulkUpdate(context.Background(), "t", "id",
		[]any{1, 2, 3}, "score", []any{100})
	if err == nil {
		t.Fatal("expected length mismatch error")
	}
}

// TestBulkUpdate_PkEqualsUpdate 拒绝 update 列 = pk 列
func TestBulkUpdate_PkEqualsUpdate(t *testing.T) {
	plugin, _ := newTestPlugin(t)

	_, err := plugin.BulkUpdate(context.Background(), "t", "id",
		[]any{1}, "id", []any{2})
	if err == nil {
		t.Fatal("expected error when update col == pk col")
	}
}
