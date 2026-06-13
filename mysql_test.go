package plugins

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/adnilis/wma"
	"github.com/jmoiron/sqlx"
)

// newMockDB creates a sqlx.DB with sqlmock for testing
func newMockDB(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create mock: %v", err)
	}
	return sqlx.NewDb(db, "mysql"), mock
}

// newTestPlugin creates a MySQLPlugin with mock DB for testing
func newTestPlugin(t *testing.T) (*MySQLPlugin, sqlmock.Sqlmock) {
	db, mock := newMockDB(t)
	plugin := &MySQLPlugin{
		name:   "test-mysql",
		config: newTestConfig(),
		state:  mysqlPluginStateRunning,
	}
	plugin.db.Store(db)
	return plugin, mock
}

// newTestConfig returns a default test configuration
func newTestConfig() MySQLPluginConfig {
	return MySQLPluginConfig{
		Addr:         "localhost:3306",
		User:         "test",
		Password:     "test",
		DBName:       "test_db",
		PoolSize:     10,
		MinIdleConns: 3,
		MaxIdleConns: 5,
		MaxLifetime:  time.Hour,
		MaxIdleTime:  5 * time.Minute,
		ConnTimeout:  5 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
		ParseTime:    true,
		Loc:          "Local",
	}
}

// TestPluginInit tests plugin initialization
func TestPluginInit(t *testing.T) {
	plugin := NewMySQLPlugin("test", &MySQLPluginConfig{
		Addr:     "localhost:3306",
		User:     "test",
		Password: "test",
		DBName:   "test_db",
	})

	if plugin.Name() != "test" {
		t.Errorf("expected name 'test', got '%s'", plugin.Name())
	}

	if plugin.Type() != wma.PluginTypeCustom {
		t.Errorf("expected type PluginTypeCustom, got %v", plugin.Type())
	}
}

// TestPluginStats tests the Stats() method
func TestPluginStats(t *testing.T) {
	plugin, _ := newTestPlugin(t)
	stats := plugin.Stats()

	if stats.Name != "test-mysql" {
		t.Errorf("expected name 'test-mysql', got '%s'", stats.Name)
	}

	if stats.PoolSize != 10 {
		t.Errorf("expected pool size 10, got %d", stats.PoolSize)
	}
}

// TestContextNilInTable tests that Table() sets a default context
func TestContextNilInTable(t *testing.T) {
	plugin, _ := newTestPlugin(t)

	// Table() should not cause nil context panic
	qr := plugin.Table("users")
	if qr.ctx == nil {
		t.Error("Table() should set a default context, got nil")
	}

	// Model() should also set a default context
	qr = plugin.Model(&testModel{})
	if qr.ctx == nil {
		t.Error("Model() should set a default context, got nil")
	}
}

// testModel is a test model for testing
type testModel struct {
	ID   int    `db:"id"`
	Name string `db:"name"`
}

func (m *testModel) TableName() string {
	return "test_models"
}

// TestQueryResultPool tests the object pool functionality
func TestQueryResultPool(t *testing.T) {
	qr1 := acquireMySQLQueryResult()
	qr1.query = "SELECT * FROM test"

	releaseMySQLQueryResult(qr1)

	// Should get a reset object from pool
	qr2 := acquireMySQLQueryResult()
	if qr2.query != "" {
		t.Errorf("expected empty query after reset, got '%s'", qr2.query)
	}
	if qr2.dirty != false {
		t.Error("expected dirty=false after reset")
	}
}

// TestTransactionBegin tests beginning a transaction
func TestTransactionBegin(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	mock.ExpectBegin()

	tx, err := plugin.Begin()
	if err != nil {
		t.Fatalf("failed to begin transaction: %v", err)
	}

	if tx == nil {
		t.Fatal("expected transaction, got nil")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

// TestDSNBuild tests DSN building with valid timezone
func TestDSNBuild(t *testing.T) {
	cfg := &MySQLPluginConfig{
		Addr:      "localhost:3306",
		User:      "root",
		Password:  "password",
		DBName:    "testdb",
		ParseTime: true,
		Loc:       "Asia/Shanghai",
	}

	dsn := buildDSN(cfg)
	if dsn == "" {
		t.Error("expected non-empty DSN")
	}
}

// TestDSNBuildInvalidTimezone tests DSN building with invalid timezone
func TestDSNBuildInvalidTimezone(t *testing.T) {
	cfg := &MySQLPluginConfig{
		Addr:      "localhost:3306",
		User:      "root",
		Password:  "password",
		DBName:    "testdb",
		ParseTime: true,
		Loc:       "Invalid/Timezone",
	}

	// Should not panic, should fallback to Local
	dsn := buildDSN(cfg)
	if dsn == "" {
		t.Error("expected non-empty DSN even with invalid timezone")
	}
}

// TestConfigNormalization and TestConfigPartialOverride were moved to config_test.go.

// TestInsert tests the Insert method
func TestInsert(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	model := &testModel{ID: 1, Name: "test"}

	// Fields are iterated in struct declaration order: ID (int) first, then Name (string)
	mock.ExpectExec("INSERT INTO test_models").
		WithArgs(1, "test").
		WillReturnResult(sqlmock.NewResult(1, 1))

	ctx := context.Background()
	id, err := plugin.Insert(ctx, model)
	if err != nil {
		t.Fatalf("failed to insert: %v", err)
	}

	if id != 1 {
		t.Errorf("expected id 1, got %d", id)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

// TestTableSQLInjection tests that SQL injection is prevented in Table()
func TestTableSQLInjection(t *testing.T) {
	plugin, _ := newTestPlugin(t)

	// Attempt SQL injection via table name
	qr := plugin.Table("users; DROP TABLE users;--")
	if qr.err == nil {
		t.Error("expected error for SQL injection attempt, got nil")
	}

	// Another injection pattern
	qr = plugin.Table("users' OR '1'='1")
	if qr.err == nil {
		t.Error("expected error for SQL injection attempt, got nil")
	}
}

// TestTableValidIdentifier tests valid table names work correctly
func TestTableValidIdentifier(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	rows := sqlmock.NewRows([]string{"id", "name"}).
		AddRow(1, "test")

	mock.ExpectQuery("SELECT \\* FROM users").
		WillReturnRows(rows)

	var results []testModel
	err := plugin.Table("users").Find(&results)
	if err != nil {
		t.Fatalf("failed to query: %v", err)
	}
}
