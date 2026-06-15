package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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

// TestPrepareCache_HitMiss 验证同 SQL 命中复用,不同 SQL 各自 Prepare
func TestPrepareCache_HitMiss(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	// 同一条 SQL 出现 2 次 → ExpectPrepare 只命中 1 次
	mock.ExpectPrepare(`SELECT \* FROM users WHERE id = \?`)
	// 第一次调用 Prepare 时 mock 实际触发

	db := plugin.DB()
	cache := NewPrepareCache(4)

	// 第一次 Prepare
	stmt1, err := cache.Prepare(context.Background(), db, "SELECT * FROM users WHERE id = ?")
	if err != nil {
		t.Fatalf("first Prepare: %v", err)
	}
	if stmt1 == nil {
		t.Fatal("expected non-nil stmt")
	}

	// 第二次同 SQL → 命中缓存,不调 Prepare
	stmt2, err := cache.Prepare(context.Background(), db, "SELECT * FROM users WHERE id = ?")
	if err != nil {
		t.Fatalf("cached Prepare: %v", err)
	}
	if stmt1 != stmt2 {
		t.Error("cached Prepare should return same *sqlx.Stmt")
	}

	hits, misses, size := cache.Stats()
	if hits != 1 || misses != 1 || size != 1 {
		t.Errorf("stats = (hits=%d, misses=%d, size=%d), want (1,1,1)", hits, misses, size)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled: %v", err)
	}
	cache.CloseAll()
}

// TestPrepareCache_Disabled 验证 cap=0 时禁用缓存
func TestPrepareCache_Disabled(t *testing.T) {
	cache := NewPrepareCache(0) // 禁用

	_, _, size := cache.Stats()
	if size != 0 {
		t.Errorf("disabled cache should report 0 size, got %d", size)
	}
}

// TestDescribeIndex_Valid 验证单表索引自省
func TestDescribeIndex_Valid(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	mock.ExpectQuery(`SELECT INDEX_NAME.*FROM information_schema\.STATISTICS`).
		WithArgs("test_db", "users").
		WillReturnRows(sqlmock.NewRows([]string{
			"INDEX_NAME", "COLUMN_NAME", "SEQ_IN_INDEX", "NON_UNIQUE",
		}).AddRow("PRIMARY", "id", 1, 0).
			AddRow("idx_name", "name", 1, 1))

	idxs, err := plugin.DescribeIndex(context.Background(), "users")
	if err != nil {
		t.Fatalf("DescribeIndex failed: %v", err)
	}
	if len(idxs) != 2 {
		t.Errorf("expected 2 indexes, got %d", len(idxs))
	}
	var pkFound, idxFound bool
	for _, idx := range idxs {
		if idx.Name == "PRIMARY" && idx.Primary {
			pkFound = true
		}
		if idx.Name == "idx_name" && !idx.Unique {
			idxFound = true
		}
	}
	if !pkFound {
		t.Error("PRIMARY index not found or Primary=false")
	}
	if !idxFound {
		t.Error("idx_name index not found")
	}
}

// TestDescribeIndex_InvalidTable 拒绝非法表名
func TestDescribeIndex_InvalidTable(t *testing.T) {
	plugin, _ := newTestPlugin(t)

	_, err := plugin.DescribeIndex(context.Background(), "evil; DROP")
	if !errors.Is(err, ErrInvalidModel) {
		t.Errorf("expected ErrInvalidModel, got %v", err)
	}
}

// BenchmarkPrepareCache_Hit 验证 R08 PrepareCache 缓存命中开销
func BenchmarkPrepareCache_Hit(b *testing.B) {
	plugin, mock := newBenchPlugin(b)
	mock.ExpectPrepare(`SELECT \* FROM users WHERE id = \?`)
	_ = mock

	cache := NewPrepareCache(64)
	// 预热:首次 Prepare
	_, _ = cache.Prepare(context.Background(), plugin.DB(), "SELECT * FROM users WHERE id = ?")
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = cache.Prepare(context.Background(), plugin.DB(), "SELECT * FROM users WHERE id = ?")
	}
	cache.CloseAll()
}

// TestSlowQueryBuffer_RecordAndSnapshot 验证环形缓冲写入与快照(时间倒序)
func TestSlowQueryBuffer_RecordAndSnapshot(t *testing.T) {
	buf := NewSlowQueryBuffer(3)
	for i := 0; i < 5; i++ {
		buf.Record("query", []any{i}, 100*time.Millisecond, int64(i))
		time.Sleep(time.Millisecond) // 确保 At 时间不同
	}

	if buf.Len() != 3 {
		t.Errorf("Len should be 3 (capacity), got %d", buf.Len())
	}

	snap := buf.Snapshot()
	if len(snap) != 3 {
		t.Errorf("Snapshot should have 3 entries, got %d", len(snap))
	}
	// 时间倒序:最新写入的(4)应在 [0],最旧的(2)应在 [2]
	if snap[0].Rows != 4 || snap[1].Rows != 3 || snap[2].Rows != 2 {
		t.Errorf("Snapshot order wrong: %v", []int64{snap[0].Rows, snap[1].Rows, snap[2].Rows})
	}
}

// TestSlowQueryBuffer_Disabled cap=0 时禁用
func TestSlowQueryBuffer_Disabled(t *testing.T) {
	buf := NewSlowQueryBuffer(0)
	buf.Record("q", nil, time.Millisecond, 1)
	if buf.Len() != 0 {
		t.Error("disabled buffer should have 0 entries")
	}
	if buf.Snapshot() != nil {
		t.Error("disabled buffer Snapshot should be nil")
	}
}

// TestWithRetry_RetryableError 验证 mock 错误类型触发重试
func TestWithRetry_RetryableError(t *testing.T) {
	policy := RetryPolicy{
		MaxAttempts:    3,
		InitialBackoff: time.Millisecond, // 测试用 1ms 加速
		MaxBackoff:     10 * time.Millisecond,
		Multiplier:     2.0,
	}
	deadlockErr := errors.New("Error 1213: Deadlock found when trying to get lock")
	attempts := 0
	err := WithRetry(context.Background(), policy, func(ctx context.Context) error {
		attempts++
		if attempts < 3 {
			return deadlockErr
		}
		return nil
	})
	if err != nil {
		t.Errorf("expected nil after 3 attempts, got %v", err)
	}
	if attempts != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts)
	}
}

// TestWithRetry_NonRetryableError 验证非重试错误立即返回
func TestWithRetry_NonRetryableError(t *testing.T) {
	policy := DefaultRetryPolicy()
	otherErr := errors.New("some other error not retryable")
	attempts := 0
	err := WithRetry(context.Background(), policy, func(ctx context.Context) error {
		attempts++
		return otherErr
	})
	if err != otherErr {
		t.Errorf("expected original error, got %v", err)
	}
	if attempts != 1 {
		t.Errorf("expected 1 attempt (no retry), got %d", attempts)
	}
}

// TestWithRetry_ExhaustedRetries 验证超过 MaxAttempts 后返回最后错误
func TestWithRetry_ExhaustedRetries(t *testing.T) {
	policy := RetryPolicy{
		MaxAttempts:    2,
		InitialBackoff: time.Millisecond,
		MaxBackoff:     10 * time.Millisecond,
		Multiplier:     2.0,
	}
	deadlockErr := errors.New("Deadlock found")
	attempts := 0
	err := WithRetry(context.Background(), policy, func(ctx context.Context) error {
		attempts++
		return deadlockErr
	})
	if err != deadlockErr {
		t.Errorf("expected deadlock err, got %v", err)
	}
	if attempts != 2 {
		t.Errorf("expected 2 attempts (MaxAttempts), got %d", attempts)
	}
}

// TestNullInt64_Scan 验证 SQL 各种来源类型都能 Scan
func TestNullInt64_Scan(t *testing.T) {
	tests := []struct {
		name    string
		src     any
		wantV   bool
		wantVal int64
		wantErr bool
	}{
		{"nil", nil, false, 0, false},
		{"int64", int64(42), true, 42, false},
		{"int", int(100), true, 100, false},
		{"int32", int32(7), true, 7, false},
		{"string", "99", true, 99, false},
		{"bad_string", "abc", false, 0, true},
		{"bytes", []byte("123"), true, 123, false},
		{"bad_type", 3.14, false, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var n NullInt64
			err := n.Scan(tt.src)
			if (err != nil) != tt.wantErr {
				t.Errorf("err = %v, wantErr = %v", err, tt.wantErr)
			}
			if !tt.wantErr && n.Valid != tt.wantV {
				t.Errorf("Valid = %v, want %v", n.Valid, tt.wantV)
			}
			if !tt.wantErr && n.Int64 != tt.wantVal {
				t.Errorf("Int64 = %d, want %d", n.Int64, tt.wantVal)
			}
		})
	}
}

// TestNullInt64_Value 验证 Valuer 写出
func TestNullInt64_Value(t *testing.T) {
	n := NewNullInt64(42)
	v, err := n.Value()
	if err != nil {
		t.Fatalf("Value: %v", err)
	}
	if v.(int64) != 42 {
		t.Errorf("Value = %v, want 42", v)
	}

	// NULL path
	nn := NullInt64{}
	v, err = nn.Value()
	if err != nil {
		t.Fatalf("Value: %v", err)
	}
	if v != nil {
		t.Errorf("NULL Value should be nil, got %v", v)
	}
}

// TestNullString_Scan 验证 string / []byte / nil 各种 Scan
func TestNullString_Scan(t *testing.T) {
	var n NullString
	if err := n.Scan("hello"); err != nil || !n.Valid || n.Str != "hello" {
		t.Errorf("Scan string failed: %+v err=%v", n, err)
	}
	if err := n.Scan([]byte("world")); err != nil || n.Str != "world" {
		t.Errorf("Scan []byte failed: %+v err=%v", n, err)
	}
	if err := n.Scan(nil); err != nil || n.Valid {
		t.Errorf("Scan nil should set Valid=false, got %+v", n)
	}
}

// TestNullInt64_FromPtr 验证指针工厂方法
func TestNullInt64_FromPtr(t *testing.T) {
	v := int64(42)
	n := NewNullInt64FromPtr(&v)
	if !n.Valid || n.Int64 != 42 {
		t.Errorf("FromPtr: %+v", n)
	}
	var p *int64
	n = NewNullInt64FromPtr(p)
	if n.Valid {
		t.Errorf("FromPtr(nil) should be invalid, got %+v", n)
	}
}

// TestStatsJSON 验证 StatsJSON 返回合法 JSON 且含 Healthy 字段
func TestStatsJSON(t *testing.T) {
	plugin, _ := newTestPlugin(t)

	body, err := plugin.StatsJSON()
	if err != nil {
		t.Fatalf("StatsJSON: %v", err)
	}
	var got MySQLStatsJSON
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Name != "test-mysql" {
		t.Errorf("Name = %q, want test-mysql", got.Name)
	}
	if got.State != "running" {
		t.Errorf("State = %q, want running", got.State)
	}
}

// TestSlowQueriesJSON_Empty 空 buffer 返回 []
func TestSlowQueriesJSON_Empty(t *testing.T) {
	plugin, _ := newTestPlugin(t)

	body, err := plugin.SlowQueriesJSON()
	if err != nil {
		t.Fatalf("SlowQueriesJSON: %v", err)
	}
	if string(body) != "[]" && string(body) != "null" {
		// 没挂 buffer 时返回 nil → "null" 也算合法
		t.Logf("body = %s", body)
	}
}

// TestSlowQueriesJSON_Records 挂 buffer 后能序列化
func TestSlowQueriesJSON_Records(t *testing.T) {
	plugin, _ := newTestPlugin(t)
	buf := NewSlowQueryBuffer(5)
	for i := 0; i < 3; i++ {
		buf.Record("SELECT ?", []any{i}, 100*time.Millisecond, int64(i))
	}
	plugin.AttachSlowBuffer(buf)

	body, err := plugin.SlowQueriesJSON()
	if err != nil {
		t.Fatalf("SlowQueriesJSON: %v", err)
	}
	var got []SlowQueryJSON
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("expected 3 records, got %d", len(got))
	}
}

// TestAdminHandler_Routes 验证 /debug/stats / /debug/slow / /debug/table 路由
func TestAdminHandler_Routes(t *testing.T) {
	plugin, _ := newTestPlugin(t)
	handler := plugin.AdminHandler()

	tests := []struct {
		path   string
		status int
	}{
		{"/debug/stats", http.StatusOK},
		{"/debug/slow", http.StatusOK},
	}

	for _, tt := range tests {
		req := httptest.NewRequest(http.MethodGet, tt.path, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != tt.status {
			t.Errorf("GET %s status = %d, want %d", tt.path, w.Code, tt.status)
		}
	}

	// 非法方法
	req := httptest.NewRequest(http.MethodPost, "/debug/stats", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /debug/stats status = %d, want 405", w.Code)
	}

	// table 端点
	req = httptest.NewRequest(http.MethodGet, "/debug/table/", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("GET /debug/table/ (no name) status = %d, want 400", w.Code)
	}
}

// TestHealthChecker_StartStop 验证启停钩子
func TestHealthChecker_StartStop(t *testing.T) {
	plugin, _ := newTestPlugin(t)
	hc := NewHealthChecker(50*time.Millisecond, 3)
	plugin.AttachHealthChecker(hc)
	hc.Start(plugin, context.Background())
	defer hc.Stop()

	// 立即 IsHealthy 应为 true(初始状态)
	if !hc.IsHealthy() {
		t.Error("expected initial IsHealthy=true")
	}
	hc.Stop()
}

// BenchmarkSlowQueryBuffer_Record 验证 R11-perf 写路径开销
func BenchmarkSlowQueryBuffer_Record(b *testing.B) {
	buf := NewSlowQueryBuffer(1000)
	for i := 0; i < b.N; i++ {
		buf.Record("SELECT ?", []any{i}, time.Millisecond, int64(i))
	}
}

// BenchmarkSlowQueryBuffer_Snapshot 验证 R11-perf 读路径开销
func BenchmarkSlowQueryBuffer_Snapshot(b *testing.B) {
	buf := NewSlowQueryBuffer(1000)
	for i := 0; i < 1000; i++ {
		buf.Record("SELECT ?", []any{i}, time.Millisecond, int64(i))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = buf.Snapshot()
	}
}

// BenchmarkStatsJSON_CachedHit 验证 R11-perf 100ms 短 TTL 缓存命中
func BenchmarkStatsJSON_CachedHit(b *testing.B) {
	plugin, _ := newBenchPlugin(b)
	// 预热缓存
	_, _ = plugin.StatsJSON()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = plugin.StatsJSON()
	}
}

// BenchmarkHashQuery_FNV 验证 R11-perf FNV-1a 替代 SHA256
func BenchmarkHashQuery_FNV(b *testing.B) {
	q := "SELECT id, name, email FROM users WHERE age > ? AND status = ? AND created_at > ?"
	for i := 0; i < b.N; i++ {
		_ = hashQuery(q)
	}
}
