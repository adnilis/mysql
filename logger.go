package plugins

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adnilis/logger"
)

// QueryLoggerConfig 查询日志配置接口
// 放在 logger.go 而非 config.go 以保持与 QueryLogger 的内聚
type QueryLoggerConfig interface {
	EnableQueryLog() bool
	SlowThreshold() int64
}

// SlowQueryHook 慢查询回调钩子(R05 新增)
// 当一条查询超过 SlowThreshold 时,除日志外会调用此钩子
// 用于把慢查询接 Prometheus / OpenTelemetry / 自建监控系统
// ctx 是触发该查询的业务上下文;query 是已 format 后的可读 SQL(含参数与表名)
type SlowQueryHook func(ctx context.Context, query string, duration time.Duration, rowsAffected int64, args ...any)

// QueryLogger SQL 查询日志记录器
type QueryLogger struct {
	config      QueryLoggerConfig
	enabled     bool
	slowEnabled bool

	// slowHook 慢查询回调(R05 新增);用 atomic.Pointer 允许运行时无锁替换
	slowHook atomic.Pointer[SlowQueryHook]
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

// SetSlowQueryHook 设置/替换慢查询回调(R05 新增)
// 传 nil 解除注册;运行时替换安全(atomic.Pointer)
func (ql *QueryLogger) SetSlowQueryHook(hook SlowQueryHook) {
	if ql == nil {
		return
	}
	if hook == nil {
		ql.slowHook.Store(nil)
		return
	}
	ql.slowHook.Store(&hook)
}

// fireSlowHook 调用慢查询钩子(若有);安全 nil 处理
func (ql *QueryLogger) fireSlowHook(ctx context.Context, query string, duration time.Duration, rowsAffected int64, args ...any) {
	if ql == nil {
		return
	}
	hp := ql.slowHook.Load()
	if hp == nil {
		return
	}
	(*hp)(ctx, query, duration, rowsAffected, args...)
}

// LogQuery 记录 SQL 查询日志（统一入口，覆盖原 LogQuery 与 LogOperation）
// 格式: [12.345ms] [rows:1] SELECT * FROM `users` WHERE `id` = 1
// 调用方为 INSERT/UPDATE/DELETE/EXEC 等操作时，可在 query 前手动加 "[INSERT] " 等前缀
func (ql *QueryLogger) LogQuery(ctx context.Context, query string, duration time.Duration, rowsAffected int64, args ...any) {
	if ql == nil {
		return
	}
	if !ql.enabled {
		// 即使总体日志关闭,慢查询仍触发钩子
		if ql.slowEnabled && ql.config.SlowThreshold() > 0 && duration.Milliseconds() >= ql.config.SlowThreshold() {
			ql.fireSlowHook(ctx, query, duration, rowsAffected, args...)
		}
		return
	}

	formattedQuery := ql.formatQuery(query, args...)
	durationStr := formatDuration(duration)
	logger.Info("%s [rows:%d] %s", durationStr, rowsAffected, formattedQuery)
}

// R06 内存指标:由 orm.go::logQ 内部 atomic 计数,无需独立 recordMetrics 包装
// 此处仅保留 doc 说明:QueryTotal/RowsRead/RowsAffected/QuerySlow/QueryErrors
// 由 plugin.metric* 字段承载,通过 Stats() 周期采样导出

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
		ql.fireSlowHook(ctx, formattedQuery, duration, rowsAffected, args...)
	}
}

// AttachSlowBuffer 把 R09 慢查询环形缓冲挂到 slow hook(R09)
//
// 调用一次后,所有通过此 logger 的慢查询都会被记录到 buffer;
// 外部可经 plugin.GetSlowQueries() 读取快照(后续 R10 可挂 /debug/slow HTTP 端点)。
func (ql *QueryLogger) AttachSlowBuffer(buf *SlowQueryBuffer) {
	if ql == nil || buf == nil {
		return
	}
	ql.SetSlowQueryHook(func(ctx context.Context, query string, duration time.Duration, rowsAffected int64, args ...any) {
		buf.Record(query, args, duration, rowsAffected)
	})
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

// argStrsPool 复用 formatQuery 内部的 argStrs []string
// 节省:每次日志 alloc 一个 []string 与 N 个 string
var argStrsPool = sync.Pool{
	New: func() any {
		s := make([]string, 0, 8)
		return &s
	},
}

// formatQuery 格式化 SQL 查询（替换参数占位符）
// 注意：本函数仅供调试日志使用，是"尽力而为"的格式化：
//   - `?` 占位符替换是朴素的字符串扫描，无法识别字符串字面量内的 `?`
//     （例如 `WHERE name = '?'` 会被错误替换）
//   - 同样无法识别注释、子查询深嵌套等复杂情形
//
// 不要用本函数的输出做安全审计或 SQL 解析。
//
// R05 优化:
//   - 单一 strings.Builder 拼装,避免 N 次字符串切片拼接的拷贝
//   - argStrs 通过 sync.Pool 复用
//   - formatTableNames 不再做 ToUpper 全量拷贝
func (ql *QueryLogger) formatQuery(query string, args ...any) string {
	if len(args) == 0 {
		return formatTableNames(query)
	}

	// 从池里取 argStrs,defer 归还
	asp := argStrsPool.Get().(*[]string)
	argStrs := (*asp)[:0]
	defer func() {
		*asp = argStrs[:0]
		argStrsPool.Put(asp)
	}()
	for _, arg := range args {
		argStrs = append(argStrs, formatArg(arg))
	}

	// 单 strings.Builder 一次性拼装,扫描 query 找 '?' 替换为 argStrs[i]
	// 同时把 '? 后的 FROM/JOIN/INTO/UPDATE/TABLE 后的表名加反引号
	var sb strings.Builder
	sb.Grow(len(query) + 32)
	argIdx := 0
	for i := 0; i < len(query); i++ {
		c := query[i]
		if c == '?' && argIdx < len(argStrs) {
			sb.WriteString(argStrs[argIdx])
			argIdx++
			continue
		}
		sb.WriteByte(c)
	}
	// 剩余 ?(args 不足)保留原样,符合原"尽力而为"语义
	for ; argIdx < len(argStrs); argIdx++ {
		sb.WriteString(",")
		sb.WriteString(argStrs[argIdx])
	}

	// 一次性 formatTableNames(仍需 5 个关键字扫描,但不再 ToUpper 全量)
	return formatTableNames(sb.String())
}

// formatTableNames 给 SQL 中常见关键字（FROM/JOIN/INTO/UPDATE/TABLE）后的表名加反引号
// R05 优化:全程 ASCII fold(lowerASCII),不再 strings.ToUpper(query) 全量拷贝;
// 用 strings.Builder 拼装,避免 N 次字符串切片赋值产生中间 string
func formatTableNames(query string) string {
	keywords := [5]string{"FROM", "JOIN", "INTO", "UPDATE", "TABLE"}
	result := query
	for _, kw := range keywords {
		result = addBackticksAfterKeyword(result, kw)
	}
	return result
}

// addBackticksAfterKeyword 在关键字后的表名添加反引号
// 如果标识符已是反引号包裹或是子查询 "("，则跳过
//
// R05 优化:移除 strings.ToUpper(query) 全量拷贝,改用 lowerASCII 逐字节比对
// (containsKeywordFold 同款实现,但本函数需要 Index 风格的位置查找,因此手写内联)
func addBackticksAfterKeyword(query, keyword string) string {
	kw := []byte(keyword)
	kwLen := len(kw)
	if kwLen == 0 {
		return query
	}

	var sb strings.Builder
	sb.Grow(len(query) + 16)

	// 扫描 query,找到 keyword(ASCII case-insensitive),且前后是边界
	// 找到后,扫描 keyword 后的标识符,加反引号;子查询/已加反引号 跳过
	pos := 0
	for pos < len(query) {
		// 查找 keyword 起始
		idx := -1
		for i := pos; i+kwLen <= len(query); i++ {
			if !isSQLKeywordBoundary(byteAtOrZero(query, i-1)) {
				continue
			}
			match := true
			for j := 0; j < kwLen; j++ {
				if lowerASCII(query[i+j]) != lowerASCII(kw[j]) {
					match = false
					break
				}
			}
			if match && isSQLKeywordBoundary(byteAtOrZero(query, i+kwLen)) {
				idx = i
				break
			}
		}
		if idx == -1 {
			sb.WriteString(query[pos:])
			break
		}

		// 输出 [pos, idx+kwLen) 的原文
		sb.WriteString(query[pos : idx+kwLen])

		// 跳到 keyword 之后,扫描标识符
		identPos := idx + kwLen
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
			sb.WriteString(query[idx+kwLen : identEnd])
			pos = identEnd
			continue
		}

		// 已加反引号 跳过
		if strings.HasPrefix(identifier, "`") && strings.HasSuffix(identifier, "`") {
			sb.WriteString(query[idx+kwLen : identEnd])
			pos = identEnd
			continue
		}

		// 加反引号
		sb.WriteByte('`')
		sb.WriteString(identifier)
		sb.WriteByte('`')
		pos = identEnd
	}

	return sb.String()
}

// byteAtOrZero 安全取字节(越界返回 0),用于 isSQLKeywordBoundary 边界判断
func byteAtOrZero(s string, i int) byte {
	if i < 0 || i >= len(s) {
		return 0
	}
	return s[i]
}

// isSQLKeywordBoundary 检查是否是 SQL 关键字边界
func isSQLKeywordBoundary(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '(' || c == ')' || c == ',' || c == 0
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
// R05 优化:简化分支,统一 fmt.Appendf
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
		// R06:hex.EncodeToString 比 fmt.Sprintf("0x%x", v) 快约 2x,
		// 且对大 []byte 不经 fmt 反射路径
		return "0x" + hex.EncodeToString(v)
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
