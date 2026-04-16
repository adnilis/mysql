package plugins

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/adnilis/logger"
)

// QueryLogger SQL查询日志记录器
type QueryLogger struct {
	config      QueryLoggerConfig
	enabled     bool
	slowEnabled bool
}

// NewQueryLogger 创建查询日志记录器
func NewQueryLogger(config QueryLoggerConfig) *QueryLogger {
	ql := &QueryLogger{
		config:      config,
		enabled:     config.Debug(),
		slowEnabled: config.SlowThreshold() > 0,
	}
	return ql
}

// IsEnabled 检查日志是否启用
func (ql *QueryLogger) IsEnabled() bool {
	return ql.enabled
}

// shouldLog 判断是否应该记录日志
func (ql *QueryLogger) shouldLog() bool {
	return ql != nil && ql.enabled
}

// LogQuery 记录普通查询日志
// 格式: [12.345ms] [rows:1] SELECT * FROM `users` WHERE `id` = 1
func (ql *QueryLogger) LogQuery(ctx context.Context, query string, duration time.Duration, rowsAffected int64, args ...interface{}) {
	if !ql.shouldLog() {
		return
	}

	formattedQuery := ql.formatQuery(query, args...)
	durationStr := formatDuration(duration)
	logger.Info("%s [rows:%d] %s", durationStr, rowsAffected, formattedQuery)
}

// LogSlowQuery 记录慢查询日志
// 格式: [SLOW 250.789ms] [rows:0] SELECT * FROM `orders` WHERE `status` = 'pending'
func (ql *QueryLogger) LogSlowQuery(ctx context.Context, query string, duration time.Duration, rowsAffected int64, args ...interface{}) {
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

// LogError 记录错误日志
// 格式: [ERROR 5.123ms] [rows:0] INSERT INTO `users` (`name`) VALUES (?)
// Error: Duplicate entry 'John' for key 'idx_name'
func (ql *QueryLogger) LogError(ctx context.Context, query string, duration time.Duration, err error, args ...interface{}) {
	if ql == nil || err == nil {
		return
	}

	formattedQuery := ql.formatQuery(query, args...)
	durationStr := formatDuration(duration)
	logger.Error("[ERROR %s] %s\nError: %v",
		durationStr, formattedQuery, err)
}

// LogOperation 记录数据库操作（insert/update/delete）
// 格式: [8.456ms] [rows:5] INSERT INTO `items` (`rid`, `item_id`, `num`) VALUES (1, 1001, 10)
func (ql *QueryLogger) LogOperation(ctx context.Context, operation string, table string, duration time.Duration, rowsAffected int64, query string, args ...interface{}) {
	if !ql.shouldLog() {
		return
	}

	formattedQuery := ql.formatQuery(query, args...)
	durationStr := formatDuration(duration)
	logger.Debug("%s [rows:%d] %s", durationStr, rowsAffected, formattedQuery)
}

// LogTransaction 记录事务操作
// 格式: [BEGIN] [0.123ms]
func (ql *QueryLogger) LogTransaction(ctx context.Context, operation string, duration time.Duration) {
	if !ql.shouldLog() {
		return
	}

	durationStr := formatDuration(duration)
	logger.Info("[%s] %s", operation, durationStr)
}

// formatDuration 格式化持续时间，类似GORM
// 格式: [12.345ms]
func formatDuration(duration time.Duration) string {
	ms := float64(duration.Nanoseconds()) / 1e6
	return fmt.Sprintf("[%.3fms]", ms)
}

// formatQuery 格式化SQL查询（替换参数占位符）
func (ql *QueryLogger) formatQuery(query string, args ...interface{}) string {
	if len(args) == 0 {
		return formatTableNames(query)
	}

	// 将参数转换为字符串表示
	argStrs := make([]string, len(args))
	for i, arg := range args {
		argStrs[i] = formatArg(arg)
	}

	// 替换占位符（简单实现）
	formatted := query
	for _, arg := range argStrs {
		idx := strings.Index(formatted, "?")
		if idx == -1 {
			break
		}
		formatted = formatted[:idx] + arg + formatted[idx+1:]
	}

	// 格式化表名，添加反引号
	formatted = formatTableNames(formatted)

	return formatted
}

// formatTableNames 格式化SQL中的表名，添加反引号
func formatTableNames(query string) string {
	// 简单的表名识别和格式化
	// 匹配 FROM, JOIN, INTO, UPDATE, TABLE 等关键字后的表名
	patterns := []struct {
		keyword string
		prefix  string
	}{
		{"\nFROM (", "\nFROM ("},
		{" FROM (", " FROM ("},
		{"\nFROM `", "\nFROM `"},
		{" FROM `", " FROM `"},
		{"\nJOIN (", "\nJOIN ("},
		{" JOIN (", " JOIN ("},
		{"\nJOIN `", "\nJOIN `"},
		{" JOIN `", " JOIN `"},
		{" INTO `", " INTO `"},
		{" UPDATE `", " UPDATE `"},
		{" TABLE `", " TABLE `"},
	}

	queryUpper := strings.ToUpper(query)
	for _, pat := range patterns {
		if strings.Contains(query, pat.prefix) || strings.Contains(queryUpper, strings.ToUpper(pat.prefix)) {
			// 已经有反引号，跳过
			return query
		}
	}

	// 简单的替换：在每个关键字后的第一个标识符周围添加反引号
	keywords := []string{"FROM", "JOIN", "INTO", "UPDATE", "TABLE"}
	result := query
	for _, keyword := range keywords {
		result = addBackticksAfterKeyword(result, keyword)
	}
	return result
}

// addBackticksAfterKeyword 在关键字后的表名添加反引号
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

// isSQLKeywordBoundary 检查是否是SQL关键字边界
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

// formatArg 格式化参数值
func formatArg(arg interface{}) string {
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
	default:
		return fmt.Sprintf("%v", arg)
	}
}

// getContextInfo 获取上下文信息（可用于跟踪请求链路）
func getContextInfo(ctx context.Context) string {
	// 可以从context中提取跟踪ID等信息
	// 这里返回key-value对用于调试
	if ctx == nil {
		return "none"
	}
	return fmt.Sprintf("%p", ctx) // 简单返回context指针地址
}
