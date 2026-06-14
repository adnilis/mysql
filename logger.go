package plugins

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/adnilis/logger"
)

// QueryLoggerConfig 查询日志配置接口
// 放在 logger.go 而非 config.go 以保持与 QueryLogger 的内聚
type QueryLoggerConfig interface {
	EnableQueryLog() bool
	SlowThreshold() int64
}

// QueryLogger SQL 查询日志记录器
type QueryLogger struct {
	config      QueryLoggerConfig
	enabled     bool
	slowEnabled bool
}

// NewQueryLogger 创建查询日志记录器
// 传入 nil 时返回 nil（所有方法均接受 nil 接收者并安全退化为 no-op）
func NewQueryLogger(config QueryLoggerConfig) *QueryLogger {
	if config == nil {
		return nil
	}
	return &QueryLogger{
		config:      config,
		enabled:     config.EnableQueryLog(),
		slowEnabled: config.SlowThreshold() > 0,
	}
}

// IsEnabled 检查日志是否启用（含 nil 接收者保护）
func (ql *QueryLogger) IsEnabled() bool {
	return ql != nil && ql.enabled
}

// shouldLog 判断是否应该记录日志
func (ql *QueryLogger) shouldLog() bool {
	return ql != nil && ql.enabled
}

// LogQuery 记录 SQL 查询日志（统一入口，覆盖原 LogQuery 与 LogOperation）
// 格式: [12.345ms] [rows:1] SELECT * FROM `users` WHERE `id` = 1
// 调用方为 INSERT/UPDATE/DELETE/EXEC 等操作时，可在 query 前手动加 "[INSERT] " 等前缀
func (ql *QueryLogger) LogQuery(ctx context.Context, query string, duration time.Duration, rowsAffected int64, args ...any) {
	if !ql.shouldLog() {
		return
	}

	formattedQuery := ql.formatQuery(query, args...)
	durationStr := formatDuration(duration)
	logger.Info("%s [rows:%d] %s", durationStr, rowsAffected, formattedQuery)
}

// LogSlowQuery 记录慢查询日志
// 格式: [SLOW 250.789ms] [rows:0] SELECT * FROM `orders` WHERE `status` = 'pending'
func (ql *QueryLogger) LogSlowQuery(ctx context.Context, query string, duration time.Duration, rowsAffected int64, args ...any) {
	if ql == nil || !ql.slowEnabled {
		return
	}

	if duration.Milliseconds() >= ql.config.SlowThreshold() {
		formattedQuery := ql.formatQuery(query, args...)
		durationStr := formatDuration(duration)
		logger.Warn("[SLOW %s] [rows:%d] [threshold: %dms] %s",
			durationStr, rowsAffected, ql.config.SlowThreshold(), formattedQuery)
	}
}

// LogError 记录错误日志（对 nil logger 与 nil err 都安全）
// 格式: [ERROR 5.123ms] INSERT INTO `users` (`name`) VALUES (?)
// Error: Duplicate entry 'John' for key 'idx_name'
func (ql *QueryLogger) LogError(ctx context.Context, query string, duration time.Duration, err error, args ...any) {
	if ql == nil || err == nil {
		return
	}

	formattedQuery := ql.formatQuery(query, args...)
	durationStr := formatDuration(duration)
	logger.Error("[ERROR %s] %s\nError: %v",
		durationStr, formattedQuery, err)
}

// LogTransaction 记录事务操作（COMMIT / ROLLBACK / BEGIN）
// 格式: [COMMIT] [0.123ms]
func (ql *QueryLogger) LogTransaction(ctx context.Context, operation string, duration time.Duration) {
	if !ql.shouldLog() {
		return
	}

	durationStr := formatDuration(duration)
	logger.Info("[%s] %s", operation, durationStr)
}

// formatDuration 格式化持续时间，类似 GORM
// 格式: [12.345ms]
func formatDuration(duration time.Duration) string {
	ms := float64(duration.Nanoseconds()) / 1e6
	return fmt.Sprintf("[%.3fms]", ms)
}

// formatQuery 格式化 SQL 查询（替换参数占位符）
// 注意：本函数仅供调试日志使用，是"尽力而为"的格式化：
//   - `?` 占位符替换是朴素的字符串扫描，无法识别字符串字面量内的 `?`
//     （例如 `WHERE name = '?'` 会被错误替换）
//   - 同样无法识别注释、子查询深嵌套等复杂情形
//
// 不要用本函数的输出做安全审计或 SQL 解析。
func (ql *QueryLogger) formatQuery(query string, args ...any) string {
	if len(args) == 0 {
		return formatTableNames(query)
	}

	// 将参数转换为字符串表示
	argStrs := make([]string, len(args))
	for i, arg := range args {
		argStrs[i] = formatArg(arg)
	}

	// 替换占位符（朴素实现，详见函数注释）
	formatted := query
	for _, arg := range argStrs {
		idx := strings.Index(formatted, "?")
		if idx == -1 {
			break
		}
		formatted = formatted[:idx] + arg + formatted[idx+1:]
	}

	// 格式化表名，添加反引号
	return formatTableNames(formatted)
}

// formatTableNames 给 SQL 中常见关键字（FROM/JOIN/INTO/UPDATE/TABLE）后的表名加反引号
// 简化版本：直接对每个关键字调用 addBackticksAfterKeyword，由其中的"已加反引号则跳过"逻辑做幂等保护
func formatTableNames(query string) string {
	keywords := []string{"FROM", "JOIN", "INTO", "UPDATE", "TABLE"}
	result := query
	for _, kw := range keywords {
		result = addBackticksAfterKeyword(result, kw)
	}
	return result
}

// addBackticksAfterKeyword 在关键字后的表名添加反引号
// 如果标识符已是反引号包裹或是子查询 "("，则跳过
func addBackticksAfterKeyword(query, keyword string) string {
	keywordUpper := strings.ToUpper(keyword)
	queryUpper := strings.ToUpper(query)
	result := query

	pos := 0
	for {
		// 查找关键字位置
		keywordPos := strings.Index(queryUpper[pos:], keywordUpper)
		if keywordPos == -1 {
			break
		}
		keywordPos += pos

		// 检查关键字前后是否是单词边界
		if keywordPos > 0 && !isSQLKeywordBoundary(queryUpper[keywordPos-1]) {
			pos = keywordPos + len(keyword)
			continue
		}
		if keywordPos+len(keyword) < len(queryUpper) && !isSQLKeywordBoundary(queryUpper[keywordPos+len(keyword)]) {
			pos = keywordPos + len(keyword)
			continue
		}

		// 查找关键字后的第一个标识符
		identPos := keywordPos + len(keyword)
		// 与 findIdentifierEnd 一致地跳过前导空白，避免反引号包到空格里
		for identPos < len(query) && (query[identPos] == ' ' || query[identPos] == '\t' || query[identPos] == '\n') {
			identPos++
		}
		identEnd := findIdentifierEnd(query, identPos)
		if identEnd == -1 {
			pos = identPos
			continue
		}

		identifier := query[identPos:identEnd]
		// 跳过子查询
		if strings.HasPrefix(strings.TrimSpace(identifier), "(") ||
			strings.Contains(identifier, "(") {
			pos = identEnd
			continue
		}

		// 如果标识符已经是反引号包裹，跳过
		if strings.HasPrefix(identifier, "`") && strings.HasSuffix(identifier, "`") {
			pos = identEnd
			continue
		}

		// 添加反引号
		quoted := fmt.Sprintf("`%s`", identifier)
		result = result[:identPos] + quoted + result[identEnd:]

		// 更新位置，避免重复替换
		pos = identPos + len(quoted)
		queryUpper = strings.ToUpper(result)
	}

	return result
}

// isSQLKeywordBoundary 检查是否是 SQL 关键字边界
func isSQLKeywordBoundary(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '(' || c == ')' || c == ','
}

// findIdentifierEnd 查找标识符的结束位置
func findIdentifierEnd(query string, start int) int {
	// 跳过空白字符
	for start < len(query) && (query[start] == ' ' || query[start] == '\t' || query[start] == '\n') {
		start++
	}

	if start >= len(query) {
		return -1
	}

	// 检查是否是子查询
	if query[start] == '(' {
		return -1
	}

	// 查找标识符结束位置
	end := start
	for end < len(query) {
		c := query[end]
		if c == ' ' || c == '\t' || c == '\n' || c == ',' || c == ')' || c == ';' {
			break
		}
		end++
	}

	if end == start {
		return -1
	}

	return end
}

// formatArg 格式化参数值（仅用于日志展示）
func formatArg(arg any) string {
	if arg == nil {
		return "NULL"
	}

	switch v := arg.(type) {
	case string:
		// 转义单引号
		escaped := strings.ReplaceAll(v, "'", "''")
		return "'" + escaped + "'"
	case []byte:
		return fmt.Sprintf("0x%x", v)
	case time.Time:
		return "'" + v.Format("2006-01-02 15:04:05") + "'"
	case bool:
		if v {
			return "TRUE"
		}
		return "FALSE"
	default:
		return fmt.Sprintf("%v", arg)
	}
}
