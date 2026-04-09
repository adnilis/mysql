package plugins

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

// newBenchPlugin creates a MySQLPlugin with mock DB for benchmarking
func newBenchPlugin(b *testing.B) (*MySQLPlugin, sqlmock.Sqlmock) {
	db, mock, err := sqlmock.New()
	if err != nil {
		b.Fatalf("failed to create mock: %v", err)
	}
	plugin := &MySQLPlugin{
		name:   "bench-mysql",
		config: newTestConfig(),
		db:     sqlx.NewDb(db, "mysql"),
		state:  mysqlPluginStateRunning,
	}
	return plugin, mock
}

// BenchmarkQueryBuild tests the performance of query building
func BenchmarkQueryBuild(b *testing.B) {
	plugin, _ := newBenchPlugin(b)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		qr := plugin.Query(ctx, "SELECT * FROM users").
			Where("age > ?", 18).
			Join("INNER JOIN", "orders", "users.id = orders.user_id", 1).
			Group("department").
			Order("name").
			Limit(10)
		qr.buildQuery()
		releaseMySQLQueryResult(qr)
	}
}

// BenchmarkQueryBuildCached tests cached query building performance
func BenchmarkQueryBuildCached(b *testing.B) {
	plugin, _ := newBenchPlugin(b)
	ctx := context.Background()

	qr := plugin.Query(ctx, "SELECT * FROM users").
		Where("age > ?", 18).
		Join("INNER JOIN", "orders", "users.id = orders.user_id", 1).
		Group("department").
		Order("name").
		Limit(10)

	// Warm up the cache
	qr.buildQuery()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		qr.buildQuery()
	}
	releaseMySQLQueryResult(qr)
}

// BenchmarkQuerySelect tests the performance of query execution with mock
func BenchmarkQuerySelect(b *testing.B) {
	plugin, mock := newBenchPlugin(b)

	rows := sqlmock.NewRows([]string{"id", "name"}).
		AddRow(1, "test")

	mock.ExpectQuery("SELECT \\* FROM users").
		WillReturnRows(rows)

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var results []testModel
		_ = plugin.Query(ctx, "SELECT * FROM users").
			Where("age > ?", 18).
			Find(&results)
	}
}

// BenchmarkBatchInsert tests the performance of batch insert
func BenchmarkBatchInsert(b *testing.B) {
	plugin, mock := newBenchPlugin(b)

	mock.ExpectExec("INSERT INTO test_models").
		WithArgs(1, "test1", 2, "test2", 3, "test3", 4, "test4", 5, "test5").
		WillReturnResult(sqlmock.NewResult(1, 5))

	ctx := context.Background()
	models := []IModel{
		&testModel{ID: 1, Name: "test1"},
		&testModel{ID: 2, Name: "test2"},
		&testModel{ID: 3, Name: "test3"},
		&testModel{ID: 4, Name: "test4"},
		&testModel{ID: 5, Name: "test5"},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = plugin.BatchInsert(ctx, models, 5)
	}
}

// BenchmarkObjectPoolAcquire tests the performance of acquiring from object pool
func BenchmarkObjectPoolAcquire(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		qr := acquireMySQLQueryResult()
		releaseMySQLQueryResult(qr)
	}
}

// BenchmarkDSNBuild tests the performance of DSN building
func BenchmarkDSNBuild(b *testing.B) {
	cfg := &MySQLPluginConfig{
		Addr:      "localhost:3306",
		User:      "root",
		Password:  "password",
		DBName:    "testdb",
		ParseTime: true,
		Loc:       "Asia/Shanghai",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = buildDSN(cfg)
	}
}

// BenchmarkFieldScannerMetaCache tests the performance of field metadata caching
func BenchmarkFieldScannerMetaCache(b *testing.B) {
	model := &testModel{ID: 1, Name: "test"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scanner := newFieldScanner(model)
		_, _, _ = scanner.buildInsertSQL()
	}
}
