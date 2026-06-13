package plugins

import (
	"reflect"
	"regexp"
	"strings"
)

// SQL 标识符验证正则
// 合法的 SQL 标识符：字母或下划线开头，后跟字母、数字或下划线
var validIdentifier = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// isValidIdentifier 验证 SQL 标识符是否安全
// 标识符只能是字母、数字和下划线，不能包含特殊字符或 SQL 关键字
func isValidIdentifier(name string) bool {
	if name == "" {
		return false
	}
	return validIdentifier.MatchString(name)
}

// sanitizeIdentifier 清理 SQL 标识符
// 如果标识符无效，返回空字符串（调用方应该拒绝使用）
func sanitizeIdentifier(name string) string {
	name = strings.TrimSpace(name)
	if !isValidIdentifier(name) {
		return ""
	}
	return name
}

// getTableNameFromDest 从目标类型推断表名
// 支持的类型：
//   - &[]User{} -> "users"
//   - &User{} -> "users"
//   - 实现了 IModel 接口的类型使用 TableName() 方法
//   - dest 为 nil 或非结构体时返回 ""
func getTableNameFromDest(dest any) string {
	if dest == nil {
		return ""
	}
	t := reflect.TypeOf(dest)
	if t == nil {
		return ""
	}
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() == reflect.Slice {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return ""
	}
	if model, ok := reflect.New(t).Interface().(IModel); ok {
		return model.TableName()
	}
	return toSnakeCase(t.Name()) + "s"
}

// toSnakeCase 将驼峰命名转换为蛇形命名
// 规则：当前为大写且 (前一个字符是小写 或 后一个字符是小写) 时，前面插入下划线
// 示例：
//   - toSnakeCase("UserInfo") -> "user_info"
//   - toSnakeCase("UserID") -> "user_id"
//   - toSnakeCase("MySQLPlugin") -> "my_sql_plugin"
//   - toSnakeCase("HTTPResponse") -> "http_response"
func toSnakeCase(s string) string {
	runes := []rune(s)
	var result strings.Builder
	result.Grow(len(runes) + 4)
	for i, r := range runes {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				prev := runes[i-1]
				prevIsLower := prev >= 'a' && prev <= 'z'
				nextIsLower := i+1 < len(runes) && runes[i+1] >= 'a' && runes[i+1] <= 'z'
				if prevIsLower || nextIsLower {
					result.WriteRune('_')
				}
			}
			result.WriteRune(r + 32)
			continue
		}
		result.WriteRune(r)
	}
	return result.String()
}

// wrappedMySQLError 带表名和操作的错误
type wrappedMySQLError struct {
	table string
	op    string
	err   error
}

// wrapMySQLError 包装数据库操作错误
// 格式: "tableName: operation failed: underlying error"
func wrapMySQLError(table, op string, err error) error {
	if err == nil {
		return nil
	}
	return &wrappedMySQLError{table: table, op: op, err: err}
}

// Error 返回错误描述
func (e *wrappedMySQLError) Error() string {
	return e.table + ": " + e.op + " failed: " + e.err.Error()
}

// Unwrap 返回被包装的原始错误
func (e *wrappedMySQLError) Unwrap() error {
	return e.err
}
