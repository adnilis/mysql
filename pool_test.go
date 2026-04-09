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
