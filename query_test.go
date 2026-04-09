package plugins

import (
	"context"
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

	if err != ErrModelNotFound {
		t.Errorf("expected ErrModelNotFound, got %v", err)
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
