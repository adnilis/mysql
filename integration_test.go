//go:build integration

// Package plugins 提供 MySQL 集成测试(需真实 MySQL 实例)
//
// 运行方式:
//
//	docker run -d --name mysql-test -p 3306:3306 \
//	  -e MYSQL_ROOT_PASSWORD=secret -e MYSQL_DATABASE=testdb mysql:8
//	go test -tags=integration -count=1 ./...
//
// 或通过环境变量自定义连接:
//
//	MYSQL_TEST_ADDR=localhost:3306 \
//	MYSQL_TEST_USER=root \
//	MYSQL_TEST_PASSWORD=secret \
//	MYSQL_TEST_DB=testdb \
//	go test -tags=integration -count=1 ./...
package plugins

import (
	"context"
	"os"
	"testing"
	"time"
)

// integrationTestConfig 从环境变量读取连接信息,缺省用本地默认
func integrationTestConfig() MySQLPluginConfig {
	addr := os.Getenv("MYSQL_TEST_ADDR")
	if addr == "" {
		addr = "localhost:3306"
	}
	user := os.Getenv("MYSQL_TEST_USER")
	if user == "" {
		user = "root"
	}
	password := os.Getenv("MYSQL_TEST_PASSWORD")
	dbName := os.Getenv("MYSQL_TEST_DB")
	if dbName == "" {
		dbName = "testdb"
	}
	return MySQLPluginConfig{
		Addr:         addr,
		User:         user,
		Password:     password,
		DBName:       dbName,
		PoolSize:     5,
		MinIdleConns: 1,
		MaxIdleConns: 3,
		ConnTimeout:  5 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
	}
}

// TestIntegration_StartStop 集成测试:真实 MySQL 连接 Start/Stop 生命周期
func TestIntegration_StartStop(t *testing.T) {
	cfg := integrationTestConfig()
	plugin := NewMySQLPlugin("integration-test", &cfg)

	if err := plugin.Init(nil); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	if err := plugin.Start(context.Background()); err != nil {
		t.Skipf("MySQL not available, skipping integration test: %v", err)
	}
	defer plugin.Stop(context.Background())

	// Ping 应成功
	if err := plugin.Ping(context.Background()); err != nil {
		t.Errorf("Ping after Start failed: %v", err)
	}

	// Stats 应有有效 db 句柄
	stats := plugin.Stats()
	if stats.State != "running" {
		t.Errorf("expected state 'running', got %q", stats.State)
	}
	if stats.OpenConnections == 0 {
		t.Error("expected OpenConnections > 0 after Start")
	}
}

// TestIntegration_BasicCRUD 集成测试:基础 CRUD 往返
func TestIntegration_BasicCRUD(t *testing.T) {
	cfg := integrationTestConfig()
	plugin := NewMySQLPlugin("integration-test", &cfg)

	if err := plugin.Start(context.Background()); err != nil {
		t.Skipf("MySQL not available, skipping: %v", err)
	}
	defer plugin.Stop(context.Background())

	// 创建一个临时表
	ctx := context.Background()
	createSQL := `CREATE TEMPORARY TABLE integration_users (
		id INT PRIMARY KEY AUTO_INCREMENT,
		name VARCHAR(100) NOT NULL,
		age INT NOT NULL
	)`
	if _, err := plugin.Exec(ctx, createSQL); err != nil {
		t.Fatalf("create table failed: %v", err)
	}

	// Insert
	id, err := plugin.ExecReturningID(ctx, "INSERT INTO integration_users (name, age) VALUES (?, ?)", "alice", 30)
	if err != nil {
		t.Fatalf("insert failed: %v", err)
	}
	if id == 0 {
		t.Error("expected non-zero insert id")
	}

	// Update
	rows, err := plugin.Exec(ctx, "UPDATE integration_users SET age = ? WHERE id = ?", 31, id)
	if err != nil {
		t.Fatalf("update failed: %v", err)
	}
	if rows != 1 {
		t.Errorf("expected 1 row affected, got %d", rows)
	}

	// Count
	count, err := plugin.Count(ctx, "integration_users", "id = ?", id)
	if err != nil {
		t.Fatalf("count failed: %v", err)
	}
	if count != 1 {
		t.Errorf("expected count 1, got %d", count)
	}

	// Delete
	rows, err = plugin.Exec(ctx, "DELETE FROM integration_users WHERE id = ?", id)
	if err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	if rows != 1 {
		t.Errorf("expected 1 row deleted, got %d", rows)
	}
}

// TestIntegration_Transaction 集成测试:事务提交/回滚
func TestIntegration_Transaction(t *testing.T) {
	cfg := integrationTestConfig()
	plugin := NewMySQLPlugin("integration-test", &cfg)

	if err := plugin.Start(context.Background()); err != nil {
		t.Skipf("MySQL not available, skipping: %v", err)
	}
	defer plugin.Stop(context.Background())

	ctx := context.Background()
	_, _ = plugin.Exec(ctx, `CREATE TEMPORARY TABLE integration_tx (id INT PRIMARY KEY, val INT)`)

	// 提交路径
	tx, err := plugin.Begin()
	if err != nil {
		t.Fatalf("begin tx failed: %v", err)
	}
	if _, err := tx.Exec(ctx, "INSERT INTO integration_tx (id, val) VALUES (?, ?)", 1, 100); err != nil {
		tx.Rollback(ctx)
		t.Fatalf("insert in tx failed: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit failed: %v", err)
	}

	// 验证提交后可见
	var count int64
	if err := plugin.Get(ctx, &count, "SELECT COUNT(*) FROM integration_tx"); err != nil {
		t.Fatalf("get count failed: %v", err)
	}
	if count != 1 {
		t.Errorf("expected count 1 after commit, got %d", count)
	}

	// 回滚路径
	tx, err = plugin.Begin()
	if err != nil {
		t.Fatalf("begin tx2 failed: %v", err)
	}
	if _, err := tx.Exec(ctx, "INSERT INTO integration_tx (id, val) VALUES (?, ?)", 2, 200); err != nil {
		tx.Rollback(ctx)
		t.Fatalf("insert in tx2 failed: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback failed: %v", err)
	}

	// 验证回滚后不可见
	if err := plugin.Get(ctx, &count, "SELECT COUNT(*) FROM integration_tx"); err != nil {
		t.Fatalf("get count 2 failed: %v", err)
	}
	if count != 1 {
		t.Errorf("expected count 1 after rollback, got %d", count)
	}
}
