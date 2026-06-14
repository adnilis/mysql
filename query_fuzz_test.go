package plugins

import (
	"context"
	"testing"
)

// FuzzBuildQuery 模糊测试 buildQuery 的拼接鲁棒性
//
// 输入:任意原始 query(不传 args,聚焦 query 拼接本身)
// 不变式:
//  1. 不 panic
//  2. 返回的 query 是非空字符串
//  3. 相同输入 → 相同输出(确定性)
//
// 注:? 计数与 args 数量的一致性是 CALLER 的契约,不是 buildQuery 的责任,
// 因此不作为 Fuzz 不变式。
//
// 运行: go test -fuzz='^FuzzBuildQuery$' -fuzztime=10s ./...
func FuzzBuildQuery(f *testing.F) {
	// 种子语料
	f.Add("SELECT * FROM users")
	f.Add("SELECT * FROM users WHERE id = ?")
	f.Add("SELECT * FROM users WHERE id = ? AND status = ?")
	f.Add("SELECT u.* FROM users u")
	f.Add("SELECT a, b, c FROM t")
	f.Add("DELETE FROM users WHERE id = ?")
	f.Add("UPDATE users SET name = ? WHERE id = ?")

	f.Fuzz(func(t *testing.T, query string) {
		// 空 query 是合法输入(返回空输出),不视为失败
		if query == "" {
			t.Skip("empty query: legal boundary, skip")
		}

		plugin, _ := newTestPlugin(t)
		qr := plugin.Query(context.Background(), query)
		// 强制重置 dirty 让 buildQuery 重新构建
		qr.dirty = true

		defer func() {
			if r := recover(); r != nil {
				t.Errorf("buildQuery panicked on input %q: %v", query, r)
			}
			releaseMySQLQueryResult(qr)
		}()

		builtQuery, _ := qr.buildQuery()

		// 不变式 1: 非空输入 → 非空输出
		if builtQuery == "" {
			t.Errorf("buildQuery returned empty string for non-empty input %q", query)
		}

		// 不变式 2: 确定性 — 第二次 buildQuery 应返回相同结果
		// (buildQuery 有缓存,先重置 dirty)
		qr.dirty = true
		builtQuery2, _ := qr.buildQuery()
		if builtQuery != builtQuery2 {
			t.Errorf("buildQuery not deterministic: %q vs %q", builtQuery, builtQuery2)
		}
	})
}

// FuzzBuildQueryWithArgs 模糊测试带占位符参数的 buildQuery
//
// 用固定 string 占位符 + 整型,验证不 panic
func FuzzBuildQueryWithArgs(f *testing.F) {
	f.Add("SELECT * FROM users WHERE id = ?", int64(1))
	f.Add("SELECT * FROM users WHERE name = ?", int64(2))
	f.Add("UPDATE users SET name = ? WHERE id = ?", int64(3))
	f.Add("DELETE FROM posts WHERE created_at < ?", int64(4))

	f.Fuzz(func(t *testing.T, query string, arg int64) {
		// 空 query 是合法输入(返回空输出),不视为失败
		if query == "" {
			t.Skip("empty query: legal boundary, skip")
		}

		plugin, _ := newTestPlugin(t)
		qr := plugin.Query(context.Background(), query, arg)
		qr.dirty = true

		defer func() {
			if r := recover(); r != nil {
				t.Errorf("buildQuery panicked on input %q: %v", query, r)
			}
			releaseMySQLQueryResult(qr)
		}()

		builtQuery, _ := qr.buildQuery()

		if builtQuery == "" {
			t.Errorf("buildQuery returned empty string for non-empty input %q", query)
		}
	})
}
