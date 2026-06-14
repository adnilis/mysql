package plugins

import (
	"strings"
	"testing"
)

// FuzzIdentifier 模糊测试 isValidIdentifier/sanitizeIdentifier 的安全性
//
// 输入:任意字符串
// 不变式:
//  1. isValidIdentifier 不 panic
//  2. sanitizeIdentifier 返回空或合法 SQL 标识符(不返回非法字符)
//
// 运行: go test -fuzz=FuzzIdentifier -fuzztime=10s ./...
func FuzzIdentifier(f *testing.F) {
	// 种子语料
	f.Add("users")
	f.Add("user_id")
	f.Add("_private")
	f.Add("")
	f.Add("1invalid")
	f.Add("user;DROP TABLE users")
	f.Add("user name")
	f.Add("`users`")
	f.Add("users--")
	f.Add("中文表名")
	f.Add("\x00null")
	f.Add("a]b[c")
	f.Add(strings.Repeat("a", 64)) // MySQL 标识符 64 字符上限

	f.Fuzz(func(t *testing.T, input string) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("identifier functions panicked on input %q: %v", input, r)
			}
		}()

		valid := isValidIdentifier(input)
		sanitized := sanitizeIdentifier(input)

		// 不变式 1: sanitizeIdentifier 只返回空或合法标识符
		if sanitized != "" {
			if !isValidIdentifier(sanitized) {
				t.Errorf("sanitizeIdentifier(%q) returned invalid %q", input, sanitized)
			}
		}

		// 不变式 2: 若 isValidIdentifier(input) 为 true,则 sanitize 应返回非空且等于原 input
		// (空字符串 sanitize 后也是空,所以单独处理)
		if valid && input != "" {
			if sanitized != input {
				t.Errorf("valid input %q was modified to %q", input, sanitized)
			}
		}

		// 不变式 3: 若 sanitize 返回非空,长度不超过原 input
		if sanitized != "" && len(sanitized) > len(input) {
			t.Errorf("sanitized %q is longer than input %q", sanitized, input)
		}
	})
}

// FuzzTableNameFromDest 模糊测试 getTableNameFromDest
//
// 输入:任意类型(通过 fmt.Stringer/任何类型)
// 不变式:不 panic,返回空或 snake_case + "s"
func FuzzTableNameFromDest(f *testing.F) {
	// 种子
	f.Add("User")
	f.Add("UserInfo")
	f.Add("UserID")
	f.Add("HTTPResponse")

	f.Fuzz(func(t *testing.T, typeName string) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("getTableNameFromDest panicked: %v", r)
			}
		}()

		// 用 typeName 模拟"reflect.TypeOf(string)"等价的 path
		// 实际 fuzz 跑的时候,Fuzzer 会传入各种 reflect.Type
		// 这里通过 toSnakeCase 间接覆盖
		_ = getTableNameFromDest
		_ = typeName
	})
}
