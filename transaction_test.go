package plugins

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// TestTransactionCommit tests transaction commit
func TestTransactionCommit(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	mock.ExpectBegin()
	mock.ExpectCommit()

	tx, err := plugin.Begin()
	if err != nil {
		t.Fatalf("failed to begin: %v", err)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("failed to commit: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

// TestTransactionRollback tests transaction rollback
func TestTransactionRollback(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	mock.ExpectBegin()
	mock.ExpectRollback()

	tx, err := plugin.Begin()
	if err != nil {
		t.Fatalf("failed to begin: %v", err)
	}

	if err := tx.Rollback(); err != nil {
		t.Fatalf("failed to rollback: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

// TestTransactionQuery tests query within transaction
func TestTransactionQuery(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	mock.ExpectBegin()

	rows := sqlmock.NewRows([]string{"id", "name"}).
		AddRow(1, "test")

	mock.ExpectQuery("SELECT \\* FROM users").
		WillReturnRows(rows)

	mock.ExpectCommit()

	tx, err := plugin.Begin()
	if err != nil {
		t.Fatalf("failed to begin: %v", err)
	}

	var results []testModel
	err = tx.Select(context.Background(), &results, "SELECT * FROM users")
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("failed to commit: %v", err)
	}
}

// TestTransactionExec tests Exec within transaction
func TestTransactionExec(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	mock.ExpectBegin()

	mock.ExpectExec("INSERT INTO test_models").
		WithArgs("test", 1).
		WillReturnResult(sqlmock.NewResult(1, 1))

	mock.ExpectCommit()

	tx, err := plugin.Begin()
	if err != nil {
		t.Fatalf("failed to begin: %v", err)
	}

	_, err = tx.Exec(context.Background(), "INSERT INTO test_models (name, id) VALUES (?, ?)", "test", 1)
	if err != nil {
		t.Fatalf("exec failed: %v", err)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("failed to commit: %v", err)
	}
}

// TestTransactionRollbackOnly tests rollback when transaction is done
func TestTransactionRollbackDone(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	mock.ExpectBegin()
	mock.ExpectRollback()

	tx, err := plugin.Begin()
	if err != nil {
		t.Fatalf("failed to begin: %v", err)
	}

	// First rollback should succeed
	if err := tx.Rollback(); err != nil {
		t.Fatalf("first rollback failed: %v", err)
	}

	// Second rollback should be idempotent (ErrTxDone is ignored, so no error)
	if err := tx.Rollback(); err != nil {
		t.Errorf("second rollback should not return error, got: %v", err)
	}
}

// TestTransactionCommitDone tests commit when transaction is done
func TestTransactionCommitDone(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	mock.ExpectBegin()
	mock.ExpectRollback()

	tx, err := plugin.Begin()
	if err != nil {
		t.Fatalf("failed to begin: %v", err)
	}

	// Rollback first
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback failed: %v", err)
	}

	// Commit after rollback should fail
	if err := tx.Commit(); err == nil {
		t.Error("expected error on commit after rollback")
	}
}
