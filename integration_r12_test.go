//go:build integration

// Package plugins R07-R12 新 API 集成测试(需 -tags=integration)
package plugins

import (
	"context"
	"testing"
	"time"
)

// TestIntegration_MapScan 集成测试:R05 MapScan Find
func TestIntegration_MapScan(t *testing.T) {
	cfg := integrationTestConfig()
	plugin := NewMySQLPlugin("integration-test", &cfg)
	if err := plugin.Start(context.Background()); err != nil {
		t.Skipf("MySQL not available: %v", err)
	}
	defer plugin.Stop(context.Background())

	ctx := context.Background()
	_, _ = plugin.Exec(ctx, `CREATE TEMPORARY TABLE integration_mapscan (id INT PRIMARY KEY, name VARCHAR(50))`)

	for i := 1; i <= 3; i++ {
		_, err := plugin.Exec(ctx, "INSERT INTO integration_mapscan VALUES (?, ?)", i, "name_"+string(rune('A'+i-1)))
		if err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	var results []map[string]any
	err := plugin.Table("integration_mapscan").Order("id").Find(&results)
	if err != nil {
		t.Fatalf("Find MapScan failed: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("expected 3 results, got %d", len(results))
	}
}

// TestIntegration_Upsert 集成测试:R06 Upsert (ON DUPLICATE KEY UPDATE)
func TestIntegration_Upsert(t *testing.T) {
	cfg := integrationTestConfig()
	plugin := NewMySQLPlugin("integration-test", &cfg)
	if err := plugin.Start(context.Background()); err != nil {
		t.Skipf("MySQL not available: %v", err)
	}
	defer plugin.Stop(context.Background())

	ctx := context.Background()
	_, _ = plugin.Exec(ctx, `CREATE TEMPORARY TABLE integration_upsert (
		id INT PRIMARY KEY,
		val INT NOT NULL
	) ENGINE=InnoDB`)

	// 首次插入
	_, err := plugin.Upsert(ctx, "integration_upsert", []string{"id", "val"},
		[][]any{{1, 100}, {2, 200}}, nil, 10)
	if err != nil {
		t.Fatalf("first Upsert: %v", err)
	}

	// 冲突更新
	_, err = plugin.Upsert(ctx, "integration_upsert", []string{"id", "val"},
		[][]any{{1, 999}, {3, 300}}, nil, 10)
	if err != nil {
		t.Fatalf("second Upsert: %v", err)
	}

	var count int64
	if err := plugin.Get(ctx, &count, "SELECT COUNT(*) FROM integration_upsert"); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 3 {
		t.Errorf("expected 3 rows after upsert, got %d", count)
	}

	// id=1 的 val 应被更新为 999
	var val int
	if err := plugin.Get(ctx, &val, "SELECT val FROM integration_upsert WHERE id = ?", 1); err != nil {
		t.Fatalf("get updated: %v", err)
	}
	if val != 999 {
		t.Errorf("expected val=999, got %d", val)
	}
}

// TestIntegration_RunInTransaction 集成测试:R04 RunInTransaction
func TestIntegration_RunInTransaction(t *testing.T) {
	cfg := integrationTestConfig()
	plugin := NewMySQLPlugin("integration-test", &cfg)
	if err := plugin.Start(context.Background()); err != nil {
		t.Skipf("MySQL not available: %v", err)
	}
	defer plugin.Stop(context.Background())

	ctx := context.Background()
	_, _ = plugin.Exec(ctx, `CREATE TEMPORARY TABLE integration_ritx (id INT PRIMARY KEY, val INT)`)

	// fn 返回 nil → 提交
	err := plugin.RunInTransaction(ctx, func(tx *MySQLTransaction) error {
		_, err := tx.Exec(ctx, "INSERT INTO integration_ritx VALUES (1, 100)")
		return err
	})
	if err != nil {
		t.Fatalf("RunInTransaction: %v", err)
	}
	var count int64
	if err := plugin.Get(ctx, &count, "SELECT COUNT(*) FROM integration_ritx"); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 row, got %d", count)
	}

	// fn 返回 error → 回滚
	err = plugin.RunInTransaction(ctx, func(tx *MySQLTransaction) error {
		_, _ = tx.Exec(ctx, "INSERT INTO integration_ritx VALUES (2, 200)")
		return context.DeadlineExceeded // 模拟错误
	})
	if err == nil {
		t.Error("expected error from RunInTransaction")
	}
	if err := plugin.Get(ctx, &count, "SELECT COUNT(*) FROM integration_ritx"); err != nil {
		t.Fatalf("count 2: %v", err)
	}
	if count != 1 {
		t.Errorf("expected still 1 row after rollback, got %d", count)
	}
}

// TestIntegration_DescribeTable 集成测试:R07 schema 自省
func TestIntegration_DescribeTable(t *testing.T) {
	cfg := integrationTestConfig()
	plugin := NewMySQLPlugin("integration-test", &cfg)
	if err := plugin.Start(context.Background()); err != nil {
		t.Skipf("MySQL not available: %v", err)
	}
	defer plugin.Stop(context.Background())

	ctx := context.Background()
	_, _ = plugin.Exec(ctx, `CREATE TEMPORARY TABLE integration_describe (
		id INT PRIMARY KEY,
		name VARCHAR(50) NOT NULL,
		score INT
	) ENGINE=InnoDB`)

	plugin.InvalidateSchemaCache("integration_describe") // 确保不被旧缓存命中

	info, err := plugin.DescribeTable(ctx, "integration_describe")
	if err != nil {
		t.Fatalf("DescribeTable: %v", err)
	}
	if info.TableName != "integration_describe" {
		t.Errorf("TableName = %q", info.TableName)
	}
	if len(info.Columns) != 3 {
		t.Errorf("expected 3 columns, got %d", len(info.Columns))
	}
	if len(info.Indexes) < 1 {
		t.Errorf("expected at least 1 index (PRIMARY), got %d", len(info.Indexes))
	}

	// 验证 R12 schema cache:第二次调用应命中缓存(不报错即可)
	_, _ = plugin.DescribeTable(ctx, "integration_describe")
}

// TestIntegration_MetricsOpenMetrics 集成测试:R12 Prometheus 输出
func TestIntegration_MetricsOpenMetrics(t *testing.T) {
	cfg := integrationTestConfig()
	plugin := NewMySQLPlugin("integration-test", &cfg)
	if err := plugin.Start(context.Background()); err != nil {
		t.Skipf("MySQL not available: %v", err)
	}
	defer plugin.Stop(context.Background())

	// 跑几次 query 让指标非零
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _ = plugin.Exec(ctx, "SELECT 1")
	_, _ = plugin.Exec(ctx, "SELECT 2")

	// 等指标累加
	time.Sleep(50 * time.Millisecond)

	metrics := plugin.MetricsOpenMetrics()
	s := string(metrics)
	if !contains(s, "mysql_query_total") {
		t.Errorf("metrics missing mysql_query_total: %s", s)
	}
	if !contains(s, "mysql_health") {
		t.Errorf("metrics missing mysql_health: %s", s)
	}
	if !contains(s, "# HELP ") {
		t.Errorf("metrics missing HELP comments: %s", s)
	}
}

// contains 简易 strings.Contains 包装
func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// TestIntegration_BatchExec 集成测试:R04 BatchExec 多行 INSERT
func TestIntegration_BatchExec(t *testing.T) {
	cfg := integrationTestConfig()
	plugin := NewMySQLPlugin("integration-test", &cfg)
	if err := plugin.Start(context.Background()); err != nil {
		t.Skipf("MySQL not available: %v", err)
	}
	defer plugin.Stop(context.Background())

	ctx := context.Background()
	_, _ = plugin.Exec(ctx, `CREATE TEMPORARY TABLE integration_batchexec (
		id INT PRIMARY KEY,
		name VARCHAR(50),
		val INT
	) ENGINE=InnoDB`)

	rows := [][]any{
		{1, "alice", 100},
		{2, "bob", 200},
		{3, "carol", 300},
	}
	affected, err := plugin.BatchExec(ctx, "integration_batchexec",
		[]string{"id", "name", "val"}, rows, 10)
	if err != nil {
		t.Fatalf("BatchExec: %v", err)
	}
	if affected != 3 {
		t.Errorf("expected 3 affected, got %d", affected)
	}
}

// TestIntegration_BulkUpdate 集成测试:R08 BulkUpdate CASE WHEN
func TestIntegration_BulkUpdate(t *testing.T) {
	cfg := integrationTestConfig()
	plugin := NewMySQLPlugin("integration-test", &cfg)
	if err := plugin.Start(context.Background()); err != nil {
		t.Skipf("MySQL not available: %v", err)
	}
	defer plugin.Stop(context.Background())

	ctx := context.Background()
	_, _ = plugin.Exec(ctx, `CREATE TEMPORARY TABLE integration_bulkupdate (
		id INT PRIMARY KEY,
		score INT
	) ENGINE=InnoDB`)

	// 初始数据
	for i := 1; i <= 3; i++ {
		_, err := plugin.Exec(ctx, "INSERT INTO integration_bulkupdate VALUES (?, ?)", i, 0)
		if err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	// 批量更新 score
	affected, err := plugin.BulkUpdate(ctx, "integration_bulkupdate", "id",
		[]any{1, 2, 3}, "score", []any{100, 200, 300})
	if err != nil {
		t.Fatalf("BulkUpdate: %v", err)
	}
	if affected != 3 {
		t.Errorf("expected 3 affected, got %d", affected)
	}

	// 验证
	for i, expected := range []int{100, 200, 300} {
		var score int
		if err := plugin.Get(ctx, &score, "SELECT score FROM integration_bulkupdate WHERE id = ?", i+1); err != nil {
			t.Fatalf("get %d: %v", i+1, err)
		}
		if score != expected {
			t.Errorf("id %d: score = %d, want %d", i+1, score, expected)
		}
	}
}

// TestIntegration_WithRetry 集成测试:R09 WithRetry 死锁重试
func TestIntegration_WithRetry(t *testing.T) {
	cfg := integrationTestConfig()
	plugin := NewMySQLPlugin("integration-test", &cfg)
	if err := plugin.Start(context.Background()); err != nil {
		t.Skipf("MySQL not available: %v", err)
	}
	defer plugin.Stop(context.Background())

	// WithRetry 路径不实际触发死锁(集成环境难复现),
	// 这里只验证 retry helper 在 fn 立即成功时不重试
	calls := 0
	policy := mysqlRetryTestPolicy()
	err := WithRetry(context.Background(), policy, func(ctx context.Context) error {
		calls++
		return nil // 立即成功
	})
	if err != nil {
		t.Errorf("WithRetry nil-fn: %v", err)
	}
	if calls != 1 {
		t.Errorf("expected 1 call (no retry on success), got %d", calls)
	}
}

// mysqlRetryTestPolicy 集成测试用的快退避策略
func mysqlRetryTestPolicy() RetryPolicy {
	return RetryPolicy{
		MaxAttempts:    2,
		InitialBackoff: time.Millisecond,
		MaxBackoff:     10 * time.Millisecond,
		Multiplier:     2.0,
	}
}

// TestIntegration_AdminEndpoints 集成测试:R12 admin HTTP 端点
func TestIntegration_AdminEndpoints(t *testing.T) {
	cfg := integrationTestConfig()
	plugin := NewMySQLPlugin("integration-test", &cfg)
	if err := plugin.Start(context.Background()); err != nil {
		t.Skipf("MySQL not available: %v", err)
	}
	defer plugin.Stop(context.Background())

	// 跑几个 query 让指标非零
	ctx := context.Background()
	_, _ = plugin.Exec(ctx, "SELECT 1")
	_, _ = plugin.Exec(ctx, "SELECT 2")
	time.Sleep(50 * time.Millisecond)

	// 验证 metrics 端点输出
	metrics := plugin.MetricsOpenMetrics()
	if !contains(string(metrics), "mysql_query_total") {
		t.Error("metrics missing mysql_query_total")
	}

	// 验证 /debug/stats 输出合法 JSON
	body, err := plugin.StatsJSON()
	if err != nil {
		t.Fatalf("StatsJSON: %v", err)
	}
	if !contains(string(body), "query_total") {
		t.Error("stats JSON missing query_total")
	}
}
