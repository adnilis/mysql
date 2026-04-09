package plugins

import (
	"reflect"
	"strings"
)

// getTableNameFromDest 从目标类型推断表名
// 支持的类型：
//   - &[]User{} -> "users"
//   - &User{} -> "users"
//   - 实现了 IModel 接口的类型使用 TableName() 方法
func getTableNameFromDest(dest interface{}) string {
	t := reflect.TypeOf(dest)
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
// 示例：
//   - toSnakeCase("UserInfo") -> "user_info"
//   - toSnakeCase("UserID") -> "user_id"
func toSnakeCase(s string) string {
	var result strings.Builder
	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			result.WriteRune('_')
		}
		result.WriteRune(func() rune {
			if r >= 'A' && r <= 'Z' {
				return r + 32
			}
			return r
		}())
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
