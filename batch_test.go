package plugins

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// TestBatchInsert tests batch insert functionality
func TestBatchInsert(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	// Setup mock for multiple insert calls
	// Fields are in struct order: ID (int), Name (string)
	mock.ExpectExec("INSERT INTO test_models").
		WithArgs(1, "test1", 2, "test2").
		WillReturnResult(sqlmock.NewResult(1, 1))

	mock.ExpectExec("INSERT INTO test_models").
		WithArgs(3, "test3").
		WillReturnResult(sqlmock.NewResult(3, 1))

	ctx := context.Background()
	models := []IModel{
		&testModel{ID: 1, Name: "test1"},
		&testModel{ID: 2, Name: "test2"},
		&testModel{ID: 3, Name: "test3"},
	}

	ids, err := plugin.BatchInsert(ctx, models, 2)
	if err != nil {
		t.Fatalf("batch insert failed: %v", err)
	}

	if len(ids) != 3 {
		t.Errorf("expected 3 ids, got %d", len(ids))
	}
}

// TestBatchInsertEmpty tests batch insert with empty slice
func TestBatchInsertEmpty(t *testing.T) {
	plugin, _ := newTestPlugin(t)

	ctx := context.Background()
	ids, err := plugin.BatchInsert(ctx, []IModel{}, 10)

	if err != nil {
		t.Fatalf("batch insert failed: %v", err)
	}

	if len(ids) != 0 {
		t.Errorf("expected 0 ids, got %d", len(ids))
	}
}

// TestBatchInsertSingle tests batch insert with single item
func TestBatchInsertSingle(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	// Fields are in struct order: ID (int), Name (string)
	mock.ExpectExec("INSERT INTO test_models").
		WithArgs(1, "test1").
		WillReturnResult(sqlmock.NewResult(1, 1))

	ctx := context.Background()
	models := []IModel{
		&testModel{ID: 1, Name: "test1"},
	}

	ids, err := plugin.BatchInsert(ctx, models, 10)
	if err != nil {
		t.Fatalf("batch insert failed: %v", err)
	}

	if len(ids) != 1 {
		t.Errorf("expected 1 id, got %d", len(ids))
	}
}

// TestBatchInsertCustomTable tests batch insert with custom table name
// Note: The actual method may not exist, this is a placeholder
func TestBatchInsertCustomTable(t *testing.T) {
	// Plugin does not have BatchInsertCustomTable method
	// Skipping this test
	t.Skip("BatchInsertCustomTable method does not exist")
}
