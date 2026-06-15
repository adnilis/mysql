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
		state:  mysqlPluginStateRunning,
	}
	plugin.db.Store(sqlx.NewDb(db, "mysql"))
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

// BenchmarkInsertSQLBuild measures Insert SQL template lookup
func BenchmarkInsertSQLBuild(b *testing.B) {
	model := &testModel{ID: 1, Name: "test"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scanner := newFieldScanner(model)
		_, _, _ = scanner.buildInsertSQL()
	}
}

// BenchmarkUpdateByIDSQLBuild measures UpdateByID SQL template lookup
func BenchmarkUpdateByIDSQLBuild(b *testing.B) {
	model := &testModel{ID: 1, Name: "test"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scanner := newFieldScanner(model)
		_, _, _ = scanner.buildUpdateByIDSQL(1)
	}
}

// BenchmarkGetDBParallel measures concurrent getDB() throughput after the
// atomic.Pointer[sqlx.DB] switch. RWMutex-based version contended under
// parallel load; atomic.Load scales linearly with CPU.
func BenchmarkGetDBParallel(b *testing.B) {
	db, _, err := sqlmock.New()
	if err != nil {
		b.Fatalf("failed to create mock: %v", err)
	}
	plugin := &MySQLPlugin{
		name:  "bench-mysql",
		state: mysqlPluginStateRunning,
	}
	plugin.db.Store(sqlx.NewDb(db, "mysql"))

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := plugin.getDB(); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkFormatQuery measures the SQL log formatter throughput.
// Pre-optimization: 5x strings.ToUpper(query) + N string concatenations.
// Post-optimization: 1x ToUpper + single-pass strings.Builder.
func BenchmarkFormatQuery(b *testing.B) {
	ql := NewQueryLogger(&fakeLoggerConfig{enabled: true})
	query := "SELECT id, name FROM users INNER JOIN orders ON users.id = orders.user_id WHERE age > ? AND status = ? AND city = ?"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ql.formatQuery(query, 18, "active", "Beijing")
	}
}

// BenchmarkFormatTableNames isolates the backtick-injection cost.
func BenchmarkFormatTableNames(b *testing.B) {
	query := "SELECT id, name FROM users INNER JOIN orders ON users.id = orders.user_id WHERE age > 18"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = formatTableNames(query)
	}
}

// BenchmarkBuildQueryPooled 验证 scratch 缓冲复用对 buildQuery 分配的影响
// 链式 5 个 Where + Join + Limit,模拟真实 DAO 查询
func BenchmarkBuildQueryPooled(b *testing.B) {
	plugin, _ := newBenchPlugin(b)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		qr := plugin.Query(ctx, "SELECT * FROM users")
		qr.LeftJoin("orders", "users.id = orders.user_id", 1)
		qr.Where("age > ?", 18)
		qr.Where("status = ?", "active")
		qr.Where("country = ?", "CN")
		qr.Where("level >= ?", 10)
		qr.Where("vip = ?", true)
		qr.Limit(50)
		_, _ = qr.buildQuery()
	}
}

// BenchmarkBuildQueryPooled_AcquireRelease 验证对象池复用对 acquire/release 的影响
// (MySQLQueryResult 通过 sync.Pool 复用,避免每次 new + 字段零值)
func BenchmarkBuildQueryPooled_AcquireRelease(b *testing.B) {
	plugin, _ := newBenchPlugin(b)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		qr := plugin.Query(ctx, "SELECT * FROM users")
		qr.Where("id = ?", 1)
		_, _ = qr.buildQuery()
		// 终端方法未调用,手动 release 模拟链式场景
		releaseMySQLQueryResult(qr)
	}
}

// BenchmarkBatchExec 验证 BatchExec 单 chunk 路径的开销
func BenchmarkBatchExec(b *testing.B) {
	plugin, mock := newBenchPlugin(b)
	rows := make([][]any, 100)
	for i := range rows {
		rows[i] = []any{int64(i), "name"}
	}
	// sqlmock 期望每个 B.N 一次执行
	mock.ExpectExec(`INSERT INTO t \(x, y\) VALUES \(\?,\?\)`).
		WillReturnResult(sqlmock.NewResult(1, 1))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = plugin.BatchExec(context.Background(), "t", []string{"x", "y"}, rows, 100)
	}
}

// BenchmarkFormatQuery_R05 验证 R05 优化(formatTableNames 去 ToUpper + strings.Builder)的开销
func BenchmarkFormatQuery_R05(b *testing.B) {
	ql := NewQueryLogger(&mysqlLoggerConfig{enabled: true, slowThreshold: 100})
	query := "SELECT id, name FROM users INNER JOIN orders ON users.id = orders.user_id WHERE age > ? AND status = ?"
	args := []any{18, "active"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ql.formatQuery(query, args...)
	}
}

// BenchmarkFormatQuery_NoArgs_R05 无 args 路径(formatTableNames 单调用)
func BenchmarkFormatQuery_NoArgs_R05(b *testing.B) {
	ql := NewQueryLogger(&mysqlLoggerConfig{enabled: true, slowThreshold: 100})
	query := "SELECT * FROM users INNER JOIN orders ON users.id = orders.user_id"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ql.formatQuery(query)
	}
}

// BenchmarkValsPool 验证 R05 valsPool 复用对写路径的分配影响
func BenchmarkValsPool(b *testing.B) {
	m := &benchModel{ID: 1, Name: "alice", Age: 25}
	scanner := newFieldScanner(m)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, vals, _ := scanner.buildInsertSQL()
		valsPool.Put(&vals)
	}
}

type benchModel struct {
	ID   int64  `db:"id"`
	Name string `db:"name"`
	Age  int    `db:"age"`
}

func (m *benchModel) TableName() string { return "bench" }
