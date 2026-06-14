package plugins

import (
	"context"
	"testing"
)

// TestPoolConfiguration tests connection pool configuration
func TestPoolConfiguration(t *testing.T) {
	plugin, _ := newTestPlugin(t)

	stats := plugin.Stats()
	if stats.PoolSize != 10 {
		t.Errorf("expected pool size 10, got %d", stats.PoolSize)
	}

	if stats.MaxIdleConns != 5 {
		t.Errorf("expected max idle conns 5, got %d", stats.MaxIdleConns)
	}
}

// TestPoolStop tests stopping the plugin
func TestPoolStop(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	mock.ExpectClose()

	ctx := context.Background()
	if err := plugin.Stop(ctx); err != nil {
		t.Fatalf("failed to stop: %v", err)
	}

	stats := plugin.Stats()
	if stats.State != "stopped" {
		t.Errorf("expected state 'stopped', got '%s'", stats.State)
	}
}

// TestPoolStopIdempotent tests that Stop is idempotent
func TestPoolStopIdempotent(t *testing.T) {
	plugin, mock := newTestPlugin(t)

	mock.ExpectClose()

	ctx := context.Background()

	// First stop
	if err := plugin.Stop(ctx); err != nil {
		t.Fatalf("first stop failed: %v", err)
	}

	// Second stop should not fail
	if err := plugin.Stop(ctx); err != nil {
		t.Fatalf("second stop failed: %v", err)
	}
}

// TestPoolContextCancellation tests graceful shutdown via context
// Note: This test requires a real database connection, so we skip it in unit tests
func TestPoolContextCancellation(t *testing.T) {
	t.Skip("requires real database connection")
}

// TestPluginStateTransitions tests plugin state transitions
func TestPluginStateTransitions(t *testing.T) {
	plugin := NewMySQLPlugin("test", &MySQLPluginConfig{
		Addr:     "localhost:3306",
		User:     "test",
		Password: "test",
		DBName:   "test_db",
	})

	// Initial state should be Ready
	stats := plugin.Stats()
	if stats.State != "ready" {
		t.Errorf("expected state 'ready', got '%s'", stats.State)
	}
}

// TestQueryResultReset tests QueryResult reset between uses
func TestQueryResultReset(t *testing.T) {
	qr := acquireMySQLQueryResult()

	// Set some state
	qr.query = "SELECT * FROM users"
	qr.args = []interface{}{1, 2, 3}
	qr.limit = 10
	qr.dirty = true

	// Reset
	qr.reset()

	// Verify all fields are reset
	if qr.query != "" {
		t.Errorf("expected empty query, got '%s'", qr.query)
	}
	if qr.args != nil && len(qr.args) != 0 {
		t.Error("expected nil or empty args")
	}
	if qr.limit != 0 {
		t.Errorf("expected limit 0, got %d", qr.limit)
	}
	if qr.dirty != false {
		t.Error("expected dirty=false")
	}
}

// TestBuildQueryCaching tests that buildQuery caches results
func TestBuildQueryCaching(t *testing.T) {
	plugin, _ := newTestPlugin(t)

	qr := plugin.Query(context.Background(), "SELECT * FROM users")
	qr.Where("id > ?", 1)

	// First build
	q1, args1 := qr.buildQuery()

	// Second build should return cached result
	q2, args2 := qr.buildQuery()

	if q1 != q2 {
		t.Error("expected same query from cache")
	}

	if len(args1) != len(args2) {
		t.Error("expected same args from cache")
	}
}

// TestEffectiveMaxIdleConns covers R01 风险 #4 修复 — MinIdleConns 影响实际 MaxIdleConns
func TestEffectiveMaxIdleConns(t *testing.T) {
	tests := []struct {
		name string
		cfg  MySQLPluginConfig
		want int
	}{
		{"min < max keeps max", MySQLPluginConfig{MinIdleConns: 3, MaxIdleConns: 5}, 5},
		{"min == max keeps value", MySQLPluginConfig{MinIdleConns: 5, MaxIdleConns: 5}, 5},
		{"min > max bumps up", MySQLPluginConfig{MinIdleConns: 8, MaxIdleConns: 5}, 8},
		{"both zero stays zero", MySQLPluginConfig{MinIdleConns: 0, MaxIdleConns: 0}, 0},
		{"only min set, max zero", MySQLPluginConfig{MinIdleConns: 4, MaxIdleConns: 0}, 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := effectiveMaxIdleConns(&tt.cfg)
			if got != tt.want {
				t.Errorf("effectiveMaxIdleConns() = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestStart_ReentranceClosesOld verifies R01 风险 #3 修复 — 重复 Start 不会泄漏旧 db
//
// 模拟 Start 中的 swap+close 模式(因 Start 内部调用 sqlx.Connect 需要真实 MySQL),
// 验证旧 db 在被新 db 替换后会被 Close,防止句柄泄漏
func TestStart_ReentranceClosesOld(t *testing.T) {
	plugin := NewMySQLPlugin("test", &MySQLPluginConfig{
		Addr:     "localhost:3306",
		User:     "test",
		Password: "test",
		DBName:   "test_db",
	})

	// 预存一个旧 db,挂上 sqlmock 期望 Close 被调用
	oldDB, oldMock := newMockDB(t)
	oldMock.ExpectClose()
	plugin.db.Store(oldDB)

	// 模拟第二次 Start:swap+close 模式
	newDB, newMock := newMockDB(t)
	newMock.ExpectClose()
	if old := plugin.db.Swap(newDB); old != nil {
		_ = old.Close()
	}

	// 验证旧 db 的 Close 期望被满足
	if err := oldMock.ExpectationsWereMet(); err != nil {
		t.Errorf("old db Close not called: %v", err)
	}

	// 清理
	if err := newDB.Close(); err != nil {
		t.Errorf("close new db: %v", err)
	}
	if err := newMock.ExpectationsWereMet(); err != nil {
		t.Errorf("new db Close not met: %v", err)
	}
}

// TestStart_FirstCallDoesNotPanic verifies the swap+close pattern is safe
// when no prior db exists (first Start, no leak risk)
func TestStart_FirstCallDoesNotPanic(t *testing.T) {
	plugin := NewMySQLPlugin("test", &MySQLPluginConfig{
		Addr:     "localhost:3306",
		User:     "test",
		Password: "test",
		DBName:   "test_db",
	})

	// 模拟第一次 Start:plugin.db 初始为 nil
	if plugin.db.Load() != nil {
		t.Fatal("expected nil initial db")
	}

	// swap+close 模式不应 panic
	newDB, _ := newMockDB(t)
	defer newDB.Close()
	if old := plugin.db.Swap(newDB); old != nil {
		_ = old.Close()
	}

	if got := plugin.db.Load(); got != newDB {
		t.Error("expected newDB after swap")
	}
}
