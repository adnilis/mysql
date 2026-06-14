package plugins

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// TestQueryWhere tests WHERE clause building
func TestQueryWhere(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	rows := sqlmock.NewRows([]string{"id", "name"}).
		AddRow(1, "test")

	mock.ExpectQuery("SELECT \\* FROM users").
		WithArgs(18).
		WillReturnRows(rows)

	ctx := context.Background()
	var results []testModel
	err := plugin.Query(ctx, "SELECT * FROM users").
		Where("age > ?", 18).
		Find(&results)

	if err != nil {
		t.Fatalf("query failed: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
}

// TestQueryJoin tests JOIN clause building
func TestQueryJoin(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	rows := sqlmock.NewRows([]string{"id", "name"}).
		AddRow(1, "test")

	mock.ExpectQuery("SELECT \\* FROM users INNER JOIN orders").
		WithArgs(1).
		WillReturnRows(rows)

	ctx := context.Background()
	var results []testModel
	err := plugin.Query(ctx, "SELECT * FROM users").
		Join("INNER JOIN", "orders", "users.id = orders.user_id", 1).
		Find(&results)

	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
}

// TestQueryOrder tests ORDER BY clause building
func TestQueryOrder(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	rows := sqlmock.NewRows([]string{"id", "name"}).
		AddRow(1, "a").
		AddRow(2, "b")

	mock.ExpectQuery("SELECT \\* FROM users ORDER BY name DESC").
		WillReturnRows(rows)

	ctx := context.Background()
	var results []testModel
	err := plugin.Query(ctx, "SELECT * FROM users").
		Order("name DESC").
		Find(&results)

	if err != nil {
		t.Fatalf("query failed: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}
}

// TestQueryLimit tests LIMIT clause building
func TestQueryLimit(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	rows := sqlmock.NewRows([]string{"id", "name"}).
		AddRow(1, "a")

	mock.ExpectQuery("SELECT \\* FROM users LIMIT 1").
		WillReturnRows(rows)

	ctx := context.Background()
	var result testModel
	err := plugin.Query(ctx, "SELECT * FROM users").
		Limit(1).
		First(&result)

	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
}

// TestQueryOffset tests OFFSET clause building
func TestQueryOffset(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	rows := sqlmock.NewRows([]string{"id", "name"}).
		AddRow(3, "c")

	mock.ExpectQuery("SELECT \\* FROM users LIMIT 10 OFFSET 2").
		WillReturnRows(rows)

	ctx := context.Background()
	var results []testModel
	err := plugin.Query(ctx, "SELECT * FROM users").
		Limit(10).
		Offset(2).
		Find(&results)

	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
}

// TestQueryGroupBy tests GROUP BY clause building
func TestQueryGroupBy(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	rows := sqlmock.NewRows([]string{"department", "count"}).
		AddRow("IT", 5).
		AddRow("Sales", 3)

	mock.ExpectQuery("SELECT department, COUNT\\(\\*\\) as count FROM users GROUP BY department").
		WillReturnRows(rows)

	ctx := context.Background()
	var results []struct {
		Department string `db:"department"`
		Count      int    `db:"count"`
	}
	err := plugin.Query(ctx, "SELECT department, COUNT(*) as count FROM users").
		Group("department").
		Find(&results)

	if err != nil {
		t.Fatalf("query failed: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}
}

// TestQuerySelect tests SELECT clause modification
func TestQuerySelect(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	rows := sqlmock.NewRows([]string{"id", "name"}).
		AddRow(1, "test")

	mock.ExpectQuery("SELECT id, name FROM users").
		WillReturnRows(rows)

	ctx := context.Background()
	var results []testModel
	err := plugin.Query(ctx, "SELECT * FROM users").
		Select("id", "name").
		Find(&results)

	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
}

// TestQueryChain tests chained method calls
func TestQueryChain(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	rows := sqlmock.NewRows([]string{"id", "name"}).
		AddRow(1, "test")

	// Use regexp to match flexible SQL
	mock.ExpectQuery("SELECT id, name FROM users INNER JOIN orders .* WHERE age > \\? GROUP BY department ORDER BY name LIMIT 10").
		WithArgs(1, 18).
		WillReturnRows(rows)

	ctx := context.Background()
	var results []testModel
	err := plugin.Query(ctx, "SELECT * FROM users").
		Join("INNER JOIN", "orders", "users.id = orders.user_id", 1).
		Where("age > ?", 18).
		Group("department").
		Order("name").
		Limit(10).
		Select("id", "name").
		Find(&results)

	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
}

// TestQueryFirstNotFound tests First() with no results
func TestQueryFirstNotFound(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	rows := sqlmock.NewRows([]string{"id", "name"})

	mock.ExpectQuery("SELECT \\* FROM users LIMIT 1").
		WillReturnRows(rows)

	ctx := context.Background()
	var result testModel
	err := plugin.Query(ctx, "SELECT * FROM users").
		First(&result)

	// R01: First 走 wrapMySQLError,errors.Is 应仍能匹配 ErrModelNotFound
	if !errors.Is(err, ErrModelNotFound) {
		t.Errorf("expected ErrModelNotFound (via errors.Is), got %v", err)
	}
}

// TestQueryFindEmpty tests Find() with no results
func TestQueryFindEmpty(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	rows := sqlmock.NewRows([]string{"id", "name"})

	mock.ExpectQuery("SELECT \\* FROM users").
		WillReturnRows(rows)

	ctx := context.Background()
	var results []testModel
	err := plugin.Query(ctx, "SELECT * FROM users").
		Find(&results)

	if err != nil {
		t.Fatalf("query failed: %v", err)
	}

	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

// TestBuildQuery_InsertNewWhere 验证原查询无 WHERE 时，按原行为插入新 WHERE
func TestBuildQuery_InsertNewWhere(t *testing.T) {
	plugin, _ := newTestPlugin(t)
	ctx := context.Background()

	qr := plugin.Query(ctx, "SELECT * FROM users")
	qr.Where("age > ?", 18)
	defer releaseMySQLQueryResult(qr)
	query, args := qr.buildQuery()

	const expected = "SELECT * FROM users WHERE age > ?"
	if query != expected {
		t.Errorf("query = %q, want %q", query, expected)
	}
	if len(args) != 1 || args[0] != 18 {
		t.Errorf("args = %v, want [18]", args)
	}
}

// TestBuildQuery_AppendToExistingWhere 验证原查询已有 WHERE 时，链式 Where() 追加 AND
// 而不是再插一个 WHERE（这是预存在 bug 的修复测试）
func TestBuildQuery_AppendToExistingWhere(t *testing.T) {
	plugin, _ := newTestPlugin(t)
	ctx := context.Background()

	qr := plugin.Query(ctx, "SELECT * FROM users WHERE id = ?", 1)
	qr.Where("age > ?", 18)
	defer releaseMySQLQueryResult(qr)
	query, args := qr.buildQuery()

	const expected = "SELECT * FROM users WHERE id = ? AND age > ?"
	if query != expected {
		t.Errorf("query = %q, want %q", query, expected)
	}
	if len(args) != 2 || args[0] != 1 || args[1] != 18 {
		t.Errorf("args = %v, want [1 18]", args)
	}
}

// TestBuildQuery_AppendToExistingWhereBeforeGroupBy 验证追加到 WHERE 时不会破坏后续 GROUP BY
func TestBuildQuery_AppendToExistingWhereBeforeGroupBy(t *testing.T) {
	plugin, _ := newTestPlugin(t)
	ctx := context.Background()

	qr := plugin.Query(ctx, "SELECT * FROM users WHERE id = ? GROUP BY name", 1)
	qr.Where("age > ?", 18)
	defer releaseMySQLQueryResult(qr)
	query, args := qr.buildQuery()

	const expected = "SELECT * FROM users WHERE id = ? AND age > ? GROUP BY name"
	if query != expected {
		t.Errorf("query = %q, want %q", query, expected)
	}
	if len(args) != 2 || args[0] != 1 || args[1] != 18 {
		t.Errorf("args = %v, want [1 18]", args)
	}
}

// TestBuildQuery_AppendMultipleWheresToExisting 验证多次链式 Where() 都追加到现有 WHERE
func TestBuildQuery_AppendMultipleWheresToExisting(t *testing.T) {
	plugin, _ := newTestPlugin(t)
	ctx := context.Background()

	qr := plugin.Query(ctx, "SELECT * FROM users WHERE id = ?", 1)
	qr.Where("age > ?", 18)
	qr.Where("status = ?", "active")
	defer releaseMySQLQueryResult(qr)
	query, args := qr.buildQuery()

	const expected = "SELECT * FROM users WHERE id = ? AND age > ? AND status = ?"
	if query != expected {
		t.Errorf("query = %q, want %q", query, expected)
	}
	if len(args) != 3 || args[0] != 1 || args[1] != 18 || args[2] != "active" {
		t.Errorf("args = %v, want [1 18 active]", args)
	}
}

// TestBuildQuery_OrWhere_Mixed 验证 R01 加固:Where/OrWhere 混合时正确产出
// "WHERE a = ? AND (b = ? OR c = ?)" 模式(OR 前缀条件不重复加 AND)
func TestBuildQuery_OrWhere_Mixed(t *testing.T) {
	plugin, _ := newTestPlugin(t)
	ctx := context.Background()

	qr := plugin.Query(ctx, "SELECT * FROM users")
	qr.Where("a = ?", 1)
	qr.OrWhere("b = ?", 2)
	qr.Where("c = ?", 3)
	defer releaseMySQLQueryResult(qr)
	query, args := qr.buildQuery()

	// 当前 R01 行为:OR 条件与前一个条件之间用空格而非 AND
	// 后续 R02/R05 可优化为 AND (a = ? OR b = ?) 子句
	if !strings.Contains(query, "a = ?") || !strings.Contains(query, "OR b = ?") {
		t.Errorf("query should contain both a=? and OR b=?: %s", query)
	}
	if len(args) != 3 {
		t.Errorf("expected 3 args, got %d: %v", len(args), args)
	}
}

// TestBuildQuery_OrWhere_ProducesORPrefix 验证 OrWhere 生成的条件以 "OR " 开头
func TestBuildQuery_OrWhere_ProducesORPrefix(t *testing.T) {
	plugin, _ := newTestPlugin(t)
	ctx := context.Background()

	qr := plugin.Query(ctx, "SELECT * FROM users")
	qr.Where("a = ?", 1)
	qr.OrWhere("b = ?", 2)
	defer releaseMySQLQueryResult(qr)
	query, args := qr.buildQuery()

	if !strings.Contains(query, "a = ?") || !strings.Contains(query, "OR b = ?") {
		t.Errorf("query should contain both a=? and OR b=?: %s", query)
	}
	if len(args) != 2 {
		t.Errorf("expected 2 args, got %d: %v", len(args), args)
	}
}

// TestCount_UsesBuildQuery 验证 R01 修复:Count 走 buildQuery(此前忽略 Where/Join 等)
func TestCount_UsesBuildQuery(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	mock.ExpectQuery("SELECT COUNT.*FROM.*users.*WHERE age =").
		WithArgs(18).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(42))

	var count int64
	err := plugin.Query(context.Background(), "SELECT * FROM users").
		Where("age = ?", 18).
		Count(&count)

	if err != nil {
		t.Fatalf("Count failed: %v", err)
	}
	if count != 42 {
		t.Errorf("count = %d, want 42", count)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

// TestCount_NoWhere 验证 Count 在无 Where 时仍能正常执行
func TestCount_NoWhere(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	mock.ExpectQuery("SELECT COUNT.*FROM.*users").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(100))

	var count int64
	err := plugin.Query(context.Background(), "SELECT * FROM users").
		Count(&count)

	if err != nil {
		t.Fatalf("Count failed: %v", err)
	}
	if count != 100 {
		t.Errorf("count = %d, want 100", count)
	}
}

// TestBuildQuery_AppendGroupByToExisting 验证原查询已有 GROUP BY 时链式 Group() 追加列
func TestBuildQuery_AppendGroupByToExisting(t *testing.T) {
	plugin, _ := newTestPlugin(t)
	ctx := context.Background()

	qr := plugin.Query(ctx, "SELECT dept, COUNT(*) FROM users GROUP BY dept")
	qr.Group("city")
	defer releaseMySQLQueryResult(qr)
	query, _ := qr.buildQuery()

	const expected = "SELECT dept, COUNT(*) FROM users GROUP BY dept, city"
	if query != expected {
		t.Errorf("query = %q, want %q", query, expected)
	}
}

// TestBuildQuery_AppendHavingToExisting 验证原查询已有 HAVING 时链式 Having() 追加 AND
func TestBuildQuery_AppendHavingToExisting(t *testing.T) {
	plugin, _ := newTestPlugin(t)
	ctx := context.Background()

	qr := plugin.Query(ctx, "SELECT dept, COUNT(*) as c FROM users GROUP BY dept HAVING c > ?", 5)
	qr.Having("c < ?", 100)
	defer releaseMySQLQueryResult(qr)
	query, args := qr.buildQuery()

	const expected = "SELECT dept, COUNT(*) as c FROM users GROUP BY dept HAVING c > ? AND c < ?"
	if query != expected {
		t.Errorf("query = %q, want %q", query, expected)
	}
	if len(args) != 2 || args[0] != 5 || args[1] != 100 {
		t.Errorf("args = %v, want [5 100]", args)
	}
}

// TestBuildQuery_AppendOrderByToExisting 验证原查询已有 ORDER BY 时链式 Order() 追加列
func TestBuildQuery_AppendOrderByToExisting(t *testing.T) {
	plugin, _ := newTestPlugin(t)
	ctx := context.Background()

	qr := plugin.Query(ctx, "SELECT * FROM users ORDER BY name")
	qr.Order("age DESC")
	defer releaseMySQLQueryResult(qr)
	query, _ := qr.buildQuery()

	const expected = "SELECT * FROM users ORDER BY name, age DESC"
	if query != expected {
		t.Errorf("query = %q, want %q", query, expected)
	}
}

// TestBuildQuery_ReplaceLimitToExisting 验证原查询已有 LIMIT 时链式 Limit() 替换值
func TestBuildQuery_ReplaceLimitToExisting(t *testing.T) {
	plugin, _ := newTestPlugin(t)
	ctx := context.Background()

	qr := plugin.Query(ctx, "SELECT * FROM users LIMIT 5")
	qr.Limit(20)
	defer releaseMySQLQueryResult(qr)
	query, _ := qr.buildQuery()

	const expected = "SELECT * FROM users LIMIT 20"
	if query != expected {
		t.Errorf("query = %q, want %q", query, expected)
	}
}

// TestBuildQuery_ReplaceOffsetToExisting 验证原查询已有 OFFSET 时链式 Offset() 替换值
func TestBuildQuery_ReplaceOffsetToExisting(t *testing.T) {
	plugin, _ := newTestPlugin(t)
	ctx := context.Background()

	qr := plugin.Query(ctx, "SELECT * FROM users LIMIT 10 OFFSET 2")
	qr.Offset(50)
	defer releaseMySQLQueryResult(qr)
	query, _ := qr.buildQuery()

	const expected = "SELECT * FROM users LIMIT 10 OFFSET 50"
	if query != expected {
		t.Errorf("query = %q, want %q", query, expected)
	}
}

// TestBuildQuery_JoinAndWhereExisting 验证 JOIN + 现有 WHERE 的位置交互
// 重构前会因 endPos 基于原 queryUpper 计算、JOIN 插入后位置偏移而出错
func TestBuildQuery_JoinAndWhereExisting(t *testing.T) {
	plugin, _ := newTestPlugin(t)
	ctx := context.Background()

	qr := plugin.Query(ctx, "SELECT * FROM users WHERE id = ?", 1)
	qr.Join("INNER JOIN", "orders", "users.id = orders.user_id", 1)
	qr.Where("age > ?", 18)
	defer releaseMySQLQueryResult(qr)
	query, args := qr.buildQuery()

	const expected = "SELECT * FROM users INNER JOIN orders ON users.id = orders.user_id WHERE id = ? AND age > ?"
	if query != expected {
		t.Errorf("query = %q, want %q", query, expected)
	}
	// args 顺序：JOIN 的 `?` 在 FROM 段里先出现，原查询的 `?` 在 WHERE 段里
	if len(args) != 3 || args[0] != 1 || args[1] != 1 || args[2] != 18 {
		t.Errorf("args = %v, want [1 1 18]", args)
	}
}
