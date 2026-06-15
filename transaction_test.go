package plugins

import (
	"context"
	"errors"
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

	if err := tx.Commit(context.Background()); err != nil {
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

	if err := tx.Rollback(context.Background()); err != nil {
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

	if err := tx.Commit(context.Background()); err != nil {
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

	if err := tx.Commit(context.Background()); err != nil {
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
	if err := tx.Rollback(context.Background()); err != nil {
		t.Fatalf("first rollback failed: %v", err)
	}

	// Second rollback should be idempotent (ErrTxDone is ignored, so no error)
	if err := tx.Rollback(context.Background()); err != nil {
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
	if err := tx.Rollback(context.Background()); err != nil {
		t.Fatalf("rollback failed: %v", err)
	}

	// Commit after rollback should fail
	if err := tx.Commit(context.Background()); err == nil {
		t.Error("expected error on commit after rollback")
	}
}

// TestTransactionClose_AutoRollback verifies R01 安全网:未 Commit/Rollback 时,
// Close 应自动回滚,防止事务句柄泄漏
func TestTransactionClose_AutoRollback(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	mock.ExpectBegin()
	mock.ExpectRollback() // Close 应触发回滚

	tx, err := plugin.Begin()
	if err != nil {
		t.Fatalf("failed to begin: %v", err)
	}

	// 未显式 Commit/Rollback,直接 Close → 应自动回滚
	if err := tx.Close(); err != nil {
		t.Errorf("Close should auto-rollback, got: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("rollback expectation not met: %v", err)
	}
}

// TestTransactionClose_AfterCommit verifies Close in defer 不应破坏已 Commit 的事务
func TestTransactionClose_AfterCommit(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	mock.ExpectBegin()
	mock.ExpectCommit() // 只有 Commit,不应再 Rollback

	tx, err := plugin.Begin()
	if err != nil {
		t.Fatalf("failed to begin: %v", err)
	}

	if err := tx.Commit(context.Background()); err != nil {
		t.Fatalf("commit failed: %v", err)
	}

	// Close 在 Commit 之后应是 no-op
	if err := tx.Close(); err != nil {
		t.Errorf("Close after Commit should be no-op, got: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

// TestTransactionClose_AfterRollback verifies Close in defer 在已 Rollback 后是 no-op
func TestTransactionClose_AfterRollback(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	mock.ExpectBegin()
	mock.ExpectRollback() // 只 Rollback 一次

	tx, err := plugin.Begin()
	if err != nil {
		t.Fatalf("failed to begin: %v", err)
	}

	if err := tx.Rollback(context.Background()); err != nil {
		t.Fatalf("rollback failed: %v", err)
	}

	// Close 之后是 no-op
	if err := tx.Close(); err != nil {
		t.Errorf("Close after Rollback should be no-op, got: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

// TestTransactionClose_Idempotent 多次 Close 安全
func TestTransactionClose_Idempotent(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	mock.ExpectBegin()
	mock.ExpectRollback()

	tx, err := plugin.Begin()
	if err != nil {
		t.Fatalf("failed to begin: %v", err)
	}

	// 多次 Close,只应回滚一次
	for i := 0; i < 3; i++ {
		if err := tx.Close(); err != nil {
			t.Errorf("Close #%d should not error, got: %v", i, err)
		}
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("rollback called more than expected: %v", err)
	}
}

// TestRunInTransaction_HappyPath 验证 fn 返回 nil → 自动 Commit
func TestRunInTransaction_HappyPath(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO users").
		WithArgs(1, "alice").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	err := plugin.RunInTransaction(context.Background(), func(tx *MySQLTransaction) error {
		_, err := tx.Exec(context.Background(), "INSERT INTO users VALUES (?, ?)", 1, "alice")
		return err
	})

	if err != nil {
		t.Fatalf("RunInTransaction failed: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

// TestRunInTransaction_FnError 验证 fn 返回 error → 自动 Rollback 且 error 透传
func TestRunInTransaction_FnError(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO users").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectRollback()

	fnErr := errors.New("simulated fn error")
	err := plugin.RunInTransaction(context.Background(), func(tx *MySQLTransaction) error {
		_, _ = tx.Exec(context.Background(), "INSERT INTO users VALUES (?, ?)", 1, "alice")
		return fnErr
	})

	if !errors.Is(err, fnErr) {
		t.Errorf("expected fn error, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

// TestRunInTransaction_Panic 验证 fn panic → 自动 Rollback 后重新 panic
func TestRunInTransaction_Panic(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	mock.ExpectBegin()
	mock.ExpectRollback()

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic to be re-raised")
		}
		if r != "boom" {
			t.Errorf("expected panic value 'boom', got %v", r)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unfulfilled expectations: %v", err)
		}
	}()

	_ = plugin.RunInTransaction(context.Background(), func(tx *MySQLTransaction) error {
		panic("boom")
	})
}
