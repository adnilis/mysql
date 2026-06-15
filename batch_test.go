package plugins

import (
	"context"
	"errors"
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

// TestBatchExec_SingleChunk 验证单 chunk 不分片
func TestBatchExec_SingleChunk(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	mock.ExpectExec(`INSERT INTO log \(uid, msg\) VALUES \(\?,\?\), \(\?,\?\)`).
		WithArgs(1, "a", 2, "b").
		WillReturnResult(sqlmock.NewResult(1, 2))

	rows := [][]any{
		{1, "a"},
		{2, "b"},
	}
	affected, err := plugin.BatchExec(context.Background(), "log", []string{"uid", "msg"}, rows, 10)
	if err != nil {
		t.Fatalf("BatchExec failed: %v", err)
	}
	if affected != 2 {
		t.Errorf("expected 2 affected, got %d", affected)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled: %v", err)
	}
}

// TestBatchExec_MultipleChunks 验证 chunkSize 强制分片
func TestBatchExec_MultipleChunks(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	// chunkSize=2 → 3 个 chunk:[0:2], [2:4], [4:5]
	mock.ExpectExec(`INSERT INTO t \(x\) VALUES \(\?\), \(\?\)`).
		WithArgs(1, 2).
		WillReturnResult(sqlmock.NewResult(1, 2))
	mock.ExpectExec(`INSERT INTO t \(x\) VALUES \(\?\), \(\?\)`).
		WithArgs(3, 4).
		WillReturnResult(sqlmock.NewResult(3, 2))
	mock.ExpectExec(`INSERT INTO t \(x\) VALUES \(\?\)`).
		WithArgs(5).
		WillReturnResult(sqlmock.NewResult(5, 1))

	rows := [][]any{{1}, {2}, {3}, {4}, {5}}
	affected, err := plugin.BatchExec(context.Background(), "t", []string{"x"}, rows, 2)
	if err != nil {
		t.Fatalf("BatchExec failed: %v", err)
	}
	if affected != 5 {
		t.Errorf("expected 5 affected, got %d", affected)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled: %v", err)
	}
}

// TestBatchExec_Empty 空 rows 直接返回 0,nil
func TestBatchExec_Empty(t *testing.T) {
	plugin, _ := newTestPlugin(t)

	affected, err := plugin.BatchExec(context.Background(), "t", []string{"x"}, nil, 10)
	if err != nil {
		t.Errorf("empty rows should not error, got: %v", err)
	}
	if affected != 0 {
		t.Errorf("expected 0 affected, got %d", affected)
	}
}

// TestBatchExec_InvalidTable 拒绝非法表名
func TestBatchExec_InvalidTable(t *testing.T) {
	plugin, _ := newTestPlugin(t)

	_, err := plugin.BatchExec(context.Background(), "evil; DROP TABLE t", []string{"x"}, [][]any{{1}}, 10)
	if err == nil {
		t.Fatal("expected error for invalid table name")
	}
	if !errors.Is(err, ErrInvalidModel) {
		t.Errorf("expected ErrInvalidModel, got %v", err)
	}
}

// TestBatchExec_ColumnMismatch 列数不匹配返回错误
func TestBatchExec_ColumnMismatch(t *testing.T) {
	plugin, _ := newTestPlugin(t)

	_, err := plugin.BatchExec(context.Background(), "t", []string{"a", "b"},
		[][]any{{1}}, 10)
	if err == nil {
		t.Fatal("expected error for column mismatch")
	}
}
