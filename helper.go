package plugins

import (
	"errors"
	"reflect"
	"regexp"
	"strings"
	"sync"
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

// tableNameCache 缓存 reflect.Type → 表名,避免每次 First 调用反射创建实例
// 写多读少场景用 sync.Map,纯读路径无锁
var tableNameCache sync.Map

// getTableNameFromDest 从目标类型推断表名
// 支持的类型：
//   - &[]User{} -> "users"
//   - &User{} -> "users"
//   - 实现了 IModel 接口的类型使用 TableName() 方法
//   - dest 为 nil 或非结构体时返回 ""
//
// 性能:首次反射后结果按 reflect.Type 缓存,后续调用 O(1) map 读取
func getTableNameFromDest(dest any) string {
	if dest == nil {
		return ""
	}
	t := reflect.TypeOf(dest)
	if t == nil {
		return ""
	}
	// 缓存键:取到 struct type 即可,ptr/slice 解引用后命中同一 entry
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() == reflect.Slice {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return ""
	}
	if v, ok := tableNameCache.Load(t); ok {
		return v.(string)
	}
	name := computeTableName(t)
	actual, _ := tableNameCache.LoadOrStore(t, name)
	return actual.(string)
}

// computeTableName 实际执行反射,只在类型首次出现时调用一次
func computeTableName(t reflect.Type) string {
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
//
// 格式: "tableName: operation failed: underlying error"
//
// 支持 errors.Is/As:
//   - errors.Is(wrapped, ErrModelNotFound) 在底层 err 匹配时返回 true
//   - errors.As(wrapped, &target) 可还原为 *wrappedMySQLError 访问 table/op 字段
//
// 推荐用法:
//
//	if errors.Is(err, mysqlplugin.ErrModelNotFound) {
//	    // 记录未命中
//	}
type wrappedMySQLError struct {
	table string
	op    string
	err   error
}

// wrapMySQLError 包装数据库操作错误,返回的错误支持 errors.Is/As
//   - table: 表名,可为空字符串
//   - op: 操作描述(如 "insert"/"update"/"select"/"delete"/"count"/"exec"/"first"/"get"/"take")
//   - err: 底层错误,若为 nil 则返回 nil
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

// Unwrap 返回被包装的原始错误,支持 errors.Is/As 链路遍历
func (e *wrappedMySQLError) Unwrap() error {
	return e.err
}

// Is 支持 errors.Is 短路匹配
// 直接匹配自身或通过 Unwrap 链匹配底层错误,使 ErrModelNotFound 等 sentinel
// 可被 errors.Is 在包装层级中识别
func (e *wrappedMySQLError) Is(target error) bool {
	if target == nil {
		return false
	}
	return errors.Is(e.err, target)
}

// Table 返回表名(可能为空)
func (e *wrappedMySQLError) Table() string { return e.table }

// Op 返回操作描述(如 "insert"/"update"/"select")
func (e *wrappedMySQLError) Op() string { return e.op }

// MySQLError 是 wrappedMySQLError 的导出别名,便于外部包做 errors.As
//
// 推荐在外部包这样使用:
//
//	var mErr *mysqlplugin.MySQLError
//	if errors.As(err, &mErr) {
//	    log.Printf("table=%s op=%s", mErr.Table(), mErr.Op())
//	}
type MySQLError = wrappedMySQLError
