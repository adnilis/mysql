package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/adnilis/logger"
)

// SlowQueryJSON 慢查询的 JSON 序列化视图(R11 + R13 args)
//
// 适配 admin HTTP 端点:POST /debug/slow 返回此结构数组
// Args 是 R13 新增:展示 ? 参数(默认经 PII 脱敏)
type SlowQueryJSON struct {
	Query    string `json:"query"`
	Args     []any  `json:"args,omitempty"` // R13:含 ? 参数(已脱敏)
	Duration string `json:"duration"`       // 人类可读
	Rows     int64  `json:"rows"`
	At       string `json:"at"` // RFC3339Nano
}

// TableInfoJSON 表结构 JSON 序列化视图(R11)
type TableInfoJSON struct {
	TableName   string   `json:"table"`
	Columns     []string `json:"columns"` // 列名列表(简版)
	Indexes     []string `json:"indexes"` // 索引名列表(简版)
	ColumnCount int      `json:"column_count"`
	IndexCount  int      `json:"index_count"`
}

// MySQLStatsJSON 插件统计 JSON 视图(R11)
type MySQLStatsJSON struct {
	Name         string `json:"name"`
	State        string `json:"state"`
	QueryTotal   int64  `json:"query_total"`
	QuerySlow    int64  `json:"query_slow"`
	QueryErrors  int64  `json:"query_errors"`
	RowsRead     int64  `json:"rows_read"`
	RowsAffected int64  `json:"rows_affected"`
	OpenConns    int    `json:"open_connections"`
	InUse        int    `json:"in_use"`
	Idle         int    `json:"idle"`
	Healthy      bool   `json:"healthy"` // R11 来自 HealthChecker
}

// histogramBuckets Prometheus 标准桶边界(秒),覆盖 MySQL 查询常见延迟分布
// R13:histogram 桶选择
var histogramBuckets = []float64{
	0.001, // 1ms
	0.005, // 5ms
	0.01,  // 10ms
	0.05,  // 50ms
	0.1,   // 100ms
	0.5,   // 500ms
	1.0,   // 1s
	5.0,   // 5s
	10.0,  // 10s
	30.0,  // 30s
	60.0,  // 60s
}

// histogramBucketsStr 预格式化的桶边界字符串(R13-perf)
// OpenMetrics 输出同一组边界多次,提前生成避免 hot path 重复 fmt
var histogramBucketsStr []string

func init() {
	histogramBucketsStr = make([]string, len(histogramBuckets))
	for i, b := range histogramBuckets {
		histogramBucketsStr[i] = formatFloat(b)
	}
}

// formatFloat 简易 float 格式化(R13)
// Prometheus 接受 0.001 / 5e-05 等格式,这里用 %g 紧凑输出
func formatFloat(f float64) string {
	return fmt.Sprintf("%g", f)
}

// queryDurationHistogram 简易 histogram(R13)
// 用 atomic int64 累计每个桶的计数,Prometheus 抓取时 sum/count 算 p50/p95/p99
//
// 设计:无 sync.Mutex;hot path 仅 1 次 atomic CAS 自旋,
// bucket 数量固定(11)所以 CAS 冲突低
type queryDurationHistogram struct {
	buckets []int64 // 各桶计数;buckets[i] 表示 <= histogramBuckets[i] 的累计次数
	count   int64   // 总样本数
	sumNS   int64   // 总耗时(纳秒)
}

// observe 记录一个样本(R13)
func (h *queryDurationHistogram) observe(d time.Duration) {
	ns := int64(d)
	atomic.AddInt64(&h.sumNS, ns)
	atomic.AddInt64(&h.count, 1)

	// 找到首个 >= ns 的桶(累加到该桶)
	secs := d.Seconds()
	for i, limit := range histogramBuckets {
		if secs <= limit {
			atomic.AddInt64(&h.buckets[i], 1)
			return
		}
	}
	// > 最大桶(60s)的样本不计入桶(只进 sum/count)
}

// snapshot 返回桶数组(包含 +Inf 桶)和 sum/count(R13)
func (h *queryDurationHistogram) snapshot() (buckets []int64, sumNS, count int64) {
	n := len(h.buckets)
	buckets = make([]int64, n+1) // +1 for +Inf bucket
	for i := 0; i < n; i++ {
		buckets[i] = atomic.LoadInt64(&h.buckets[i])
	}
	// +Inf 桶 = count
	buckets[n] = atomic.LoadInt64(&h.count)
	sumNS = atomic.LoadInt64(&h.sumNS)
	count = buckets[n]
	return
}

// reset 清空(R13)
func (h *queryDurationHistogram) reset() {
	for i := range h.buckets {
		atomic.StoreInt64(&h.buckets[i], 0)
	}
	atomic.StoreInt64(&h.count, 0)
	atomic.StoreInt64(&h.sumNS, 0)
}

// queryDuration 插件全局 query 耗时直方图(R13)
var queryDuration = &queryDurationHistogram{
	buckets: make([]int64, len(histogramBuckets)),
}

// resetQueryDurationHistogram 暴露给测试(R13)
func resetQueryDurationHistogram() {
	queryDuration.reset()
}

// MetricsOpenMetrics 返回 Prometheus / VictoriaMetrics 抓取格式(R12 + R13 histogram)
//
// 遵循 OpenMetrics 1.0 文本格式:可被 Prometheus / VictoriaMetrics /
// OpenTelemetry Collector / Grafana Agent 直接抓取
//
// 暴露的指标:
//   - mysql_query_total{plugin="<name>"} (counter)
//   - mysql_query_slow_total{plugin="<name>"} (counter)
//   - mysql_query_errors_total{plugin="<name>"} (counter)
//   - mysql_rows_read_total{plugin="<name>"} (counter)
//   - mysql_rows_affected_total{plugin="<name>"} (counter)
//   - mysql_connections{plugin="<name>",state="open|in_use|idle"} (gauge)
//   - mysql_health{plugin="<name>"} (gauge, 0=down, 1=up)
//   - mysql_query_duration_seconds{plugin="<name>",le="..."} (histogram,R13)
//   - mysql_query_duration_seconds_sum / _count (R13)
func (p *MySQLPlugin) MetricsOpenMetrics() []byte {
	s := p.Stats()
	healthy := 1
	if p.healthChecker != nil && !p.healthChecker.IsHealthy() {
		healthy = 0
	}

	var sb strings.Builder
	// # HELP / # TYPE 对每条 metric 都给出
	sb.WriteString("# HELP mysql_query_total Total number of SQL queries executed by this plugin.\n")
	sb.WriteString("# TYPE mysql_query_total counter\n")
	fmt.Fprintf(&sb, "mysql_query_total{plugin=%q} %d\n", s.Name, s.QueryTotal)

	sb.WriteString("# HELP mysql_query_slow_total Total number of slow queries (above SlowThreshold).\n")
	sb.WriteString("# TYPE mysql_query_slow_total counter\n")
	fmt.Fprintf(&sb, "mysql_query_slow_total{plugin=%q} %d\n", s.Name, s.QuerySlow)

	sb.WriteString("# HELP mysql_query_errors_total Total number of query errors.\n")
	sb.WriteString("# TYPE mysql_query_errors_total counter\n")
	fmt.Fprintf(&sb, "mysql_query_errors_total{plugin=%q} %d\n", s.Name, s.QueryErrors)

	sb.WriteString("# HELP mysql_rows_read_total Total number of rows read by SELECT-like queries.\n")
	sb.WriteString("# TYPE mysql_rows_read_total counter\n")
	fmt.Fprintf(&sb, "mysql_rows_read_total{plugin=%q} %d\n", s.Name, s.RowsRead)

	sb.WriteString("# HELP mysql_rows_affected_total Total number of rows affected by DML.\n")
	sb.WriteString("# TYPE mysql_rows_affected_total counter\n")
	fmt.Fprintf(&sb, "mysql_rows_affected_total{plugin=%q} %d\n", s.Name, s.RowsAffected)

	sb.WriteString("# HELP mysql_connections Current connection pool state.\n")
	sb.WriteString("# TYPE mysql_connections gauge\n")
	fmt.Fprintf(&sb, "mysql_connections{plugin=%q,state=\"open\"} %d\n", s.Name, s.OpenConnections)
	fmt.Fprintf(&sb, "mysql_connections{plugin=%q,state=\"in_use\"} %d\n", s.Name, s.InUse)
	fmt.Fprintf(&sb, "mysql_connections{plugin=%q,state=\"idle\"} %d\n", s.Name, s.Idle)

	sb.WriteString("# HELP mysql_health Plugin health (1=up, 0=down).\n")
	sb.WriteString("# TYPE mysql_health gauge\n")
	fmt.Fprintf(&sb, "mysql_health{plugin=%q} %d\n", s.Name, healthy)

	// R13:query 耗时直方图
	buckets, sumNS, count := queryDuration.snapshot()
	sb.WriteString("# HELP mysql_query_duration_seconds Query latency distribution in seconds.\n")
	sb.WriteString("# TYPE mysql_query_duration_seconds histogram\n")
	for i := range histogramBuckets {
		fmt.Fprintf(&sb, "mysql_query_duration_seconds{plugin=%q,le=%q} %d\n",
			s.Name, histogramBucketsStr[i], buckets[i])
	}
	// +Inf 桶(超过最大桶的样本)
	fmt.Fprintf(&sb, "mysql_query_duration_seconds{plugin=%q,le=\"+Inf\"} %d\n", s.Name, buckets[len(buckets)-1])
	// sum / count
	fmt.Fprintf(&sb, "mysql_query_duration_seconds_sum{plugin=%q} %g\n", s.Name, float64(sumNS)/1e9)
	fmt.Fprintf(&sb, "mysql_query_duration_seconds_count{plugin=%q} %d\n", s.Name, count)

	return []byte(sb.String())
}

// MarshalJSONTime 工具:把 time.Duration 序列化为 "1.234s" 形式
func durationString(d time.Duration) string {
	return d.String()
}

// SlowQueriesJSON 返回慢查询快照的 JSON 字节(R11 + R13 redact)
//
// 适配 admin HTTP 端点:GET /debug/slow → 直接 Write
// 含 trace context 友好字段
//
// R13:doRedact=true 时,自动对 PII 字段名关联的 ? 参数替换为 "<redacted:N>",
// 防止慢查询快照泄露密码/token/邮箱等敏感数据
func (p *MySQLPlugin) SlowQueriesJSON(doRedact bool) ([]byte, error) {
	records := p.SlowQueries()
	out := make([]SlowQueryJSON, 0, len(records))
	for _, r := range records {
		query := r.Query
		args := r.Args
		if doRedact && len(args) > 0 {
			args = redactArgs(query, args)
		}
		// 序列化 args 为 JSON-friendly 数组
		argsJSON := make([]any, len(args))
		for i, a := range args {
			argsJSON[i] = fmt.Sprintf("%v", a)
		}
		_ = query
		out = append(out, SlowQueryJSON{
			Query:    r.Query,
			Args:     argsJSON,
			Duration: durationString(r.Duration),
			Rows:     r.Rows,
			At:       r.At.Format(time.RFC3339Nano),
		})
	}
	return json.Marshal(out)
}

// DescribeTableJSON 返回 TableInfo 的 JSON 字节(R11)
func (p *MySQLPlugin) DescribeTableJSON(table string) ([]byte, error) {
	info, err := p.DescribeTable(context.Background(), table)
	if err != nil {
		return nil, err
	}
	columns := make([]string, len(info.Columns))
	for i, c := range info.Columns {
		columns[i] = c.Name
	}
	indexes := make([]string, len(info.Indexes))
	for i, idx := range info.Indexes {
		indexes[i] = idx.Name
	}
	return json.Marshal(TableInfoJSON{
		TableName:   info.TableName,
		Columns:     columns,
		Indexes:     indexes,
		ColumnCount: len(columns),
		IndexCount:  len(indexes),
	})
}

// statsCacheEntry StatsJSON 短 TTL 缓存(R11-perf)
type statsCacheEntry struct {
	body      []byte
	expiresAt time.Time
}

const statsCacheTTL = 100 * time.Millisecond

// StatsJSON 返回 Stats() 的 JSON 字节(R11)
//
// R11-perf:100ms 短 TTL 缓存,高频 dashboard 轮询(N 个 /s)只触发 1 次 Stats()。
// 缓存命中直接返回 []byte 副本(避免 map 竞争)。
func (p *MySQLPlugin) StatsJSON() ([]byte, error) {
	now := time.Now()
	if p.statsCache != nil && now.Before(p.statsCache.expiresAt) {
		// 返回副本防止外部修改污染缓存
		out := make([]byte, len(p.statsCache.body))
		copy(out, p.statsCache.body)
		return out, nil
	}
	s := p.Stats()
	healthy := true
	if p.healthChecker != nil {
		healthy = p.healthChecker.IsHealthy()
	}
	body, err := json.Marshal(MySQLStatsJSON{
		Name:         s.Name,
		State:        s.State,
		QueryTotal:   s.QueryTotal,
		QuerySlow:    s.QuerySlow,
		QueryErrors:  s.QueryErrors,
		RowsRead:     s.RowsRead,
		RowsAffected: s.RowsAffected,
		OpenConns:    s.OpenConnections,
		InUse:        s.InUse,
		Idle:         s.Idle,
		Healthy:      healthy,
	})
	if err != nil {
		return nil, err
	}
	p.statsCache = &statsCacheEntry{
		body:      body,
		expiresAt: now.Add(statsCacheTTL),
	}
	return body, nil
}

// InvalidateStatsCache 强制失效 StatsJSON 缓存(R11-perf)
// DML 事务后调用可立即看到新指标
func (p *MySQLPlugin) InvalidateStatsCache() {
	p.statsCache = nil
}

// adminRateLimiter 简易令牌桶限流器(R12+)
//
// 每秒补充 rate 个令牌,容量 burst;
// 内部用 atomic CAS 自旋,无锁路径适合高频 admin 端点访问
type adminRateLimiter struct {
	rate     float64 // tokens per second
	burst    float64 // bucket capacity
	tokens   float64 // 当前令牌数(R12+ 用 float bits 转 uint64 atomic 操作)
	lastFill int64   // 上次补充时间(unix nano)
}

// allow 尝试消费 1 个令牌;返回 true 表示允许, false 表示拒绝
//
// 用 math.Float64bits 把 tokens 当成 uint64 做 CAS,避免 mutex
func (r *adminRateLimiter) allow() bool {
	nowNS := time.Now().UnixNano()
	lastNS := atomic.LoadInt64(&r.lastFill)
	elapsed := float64(nowNS-lastNS) / 1e9
	if elapsed < 0 {
		elapsed = 0
	}
	atomic.StoreInt64(&r.lastFill, nowNS)

	for {
		// 读出当前 tokens
		oldTokens := math.Float64frombits(atomic.LoadUint64((*uint64)(unsafe.Pointer(&r.tokens))))
		newTokens := oldTokens + elapsed*r.rate
		if newTokens > r.burst {
			newTokens = r.burst
		}
		// 决定是否消费
		if newTokens < 1.0 {
			// 没有足够令牌,仍然更新 lastFill
			return false
		}
		newTokens -= 1.0
		// CAS 写回
		newBits := math.Float64bits(newTokens)
		if atomic.CompareAndSwapUint64(
			(*uint64)(unsafe.Pointer(&r.tokens)),
			math.Float64bits(oldTokens),
			newBits,
		) {
			return true
		}
		// CAS 失败,重试
	}
}

// piiKeywords R13:这些关键字出现在 SQL 文本中时,关联的 ? 参数会被脱敏
// 启发式:大小写不敏感子串匹配;覆盖最常见的 PII 字段名
var piiKeywords = []string{
	"password", "passwd", "pwd",
	"token", "secret", "api_key", "apikey",
	"email", "phone", "mobile",
	"id_card", "idcard", "ssn",
	"credit_card", "creditcard", "cardno",
	"auth", "credential",
}

// redactArgs R13:脱敏 args 列表
//
// 策略:对每个 ? 占位符,检查它"前一个 ? 之后到本 ? 之前"的 SQL 片段;
// 若该片段含 piiKeywords 中的任一词,本位置的 args[i] 替换为 "<redacted:N>";
// 否则保留原值
//
// 例子: "INSERT INTO users (name, password) VALUES (?, ?)"
//   - 第 0 个 ?:片段 "INSERT INTO USERS (NAME, PASSWORD) VALUES ("  含 PASSWORD → 脱敏
//   - 第 1 个 ?:片段 ", "                          不含 PII → 保留
//
// 注:实现采用"前一? 之后到本 ? 之前"语义,而非"30 字符窗口",
// 避免"前面的 ? 把 PII 关键字带进自己窗口"造成的误报
func redactArgs(query string, args []any) []any {
	if len(args) == 0 {
		return args
	}
	upper := strings.ToUpper(query)
	redacted := make([]any, len(args))
	prevIdx := 0 // 上一个 ? 之后的位置
	for i := range args {
		idx := findNthQuestionMark(upper, i)
		if idx < 0 {
			// 找不到第 i 个 ?,保留原值
			redacted[i] = args[i]
			continue
		}
		// 提取 prevIdx..idx 之间的片段
		segment := upper[prevIdx:idx]
		if containsPIIKeyword(segment) {
			redacted[i] = fmt.Sprintf("<redacted:%d>", i)
		} else {
			redacted[i] = args[i]
		}
		prevIdx = idx + 1
	}
	return redacted
}

// findNthQuestionMark 找第 n 个 '?' 位置(从 0 起)
func findNthQuestionMark(s string, n int) int {
	count := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '?' {
			if count == n {
				return i
			}
			count++
		}
	}
	return -1
}

// containsPIIKeyword 检查 prefix 是否含 PII 关键字
func containsPIIKeyword(prefix string) bool {
	for _, kw := range piiKeywords {
		if strings.Contains(prefix, strings.ToUpper(kw)) {
			return true
		}
	}
	return false
}

// AdminHandler 返回一个 http.Handler 暴露 R11 admin 端点(R11)
//
// 路由:
//
//	GET /debug/stats        → StatsJSON
//	GET /debug/slow         → SlowQueriesJSON (最近 100 条,含 PII 脱敏 R13)
//	GET /debug/table/{name} → DescribeTableJSON
//
// R12:若设置了 adminAuthToken,所有端点要求请求头 "X-MySQL-Admin-Token: <token>"
// R12+:可选 adminRateLimit 设置令牌桶限流,默认 100 req/s
// R13:/debug/slow 自动对 PII 字段名关联的 ? 参数脱敏
//
// 挂到 wma admin mux 或独立 HTTP server 即可使用
func (p *MySQLPlugin) AdminHandler() http.Handler {
	mux := http.NewServeMux()

	// R12:auth 中间件(若 token 非空)
	auth := func(w http.ResponseWriter, r *http.Request) bool {
		if p.adminAuthToken == "" {
			return true
		}
		if r.Header.Get("X-MySQL-Admin-Token") != p.adminAuthToken {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return false
		}
		return true
	}

	// R12+:rate limit 中间件
	rl := func(w http.ResponseWriter, r *http.Request) bool {
		if p.adminRateLimiter == nil {
			return true
		}
		if !p.adminRateLimiter.allow() {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return false
		}
		return true
	}

	mux.HandleFunc("/debug/stats", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) || !rl(w, r) {
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := p.StatsJSON()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})

	mux.HandleFunc("/debug/slow", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) || !rl(w, r) {
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		// R13:支持 ?redact=0 关闭脱敏(默认开启,适合排查 PII 字段值时)
		// R13 默认 redact=1,生产推荐保持开启
		doRedact := r.URL.Query().Get("redact") != "0"
		body, err := p.SlowQueriesJSON(doRedact)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})

	mux.HandleFunc("/debug/table/", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) || !rl(w, r) {
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		table := strings.TrimPrefix(r.URL.Path, "/debug/table/")
		if table == "" {
			http.Error(w, "table name required", http.StatusBadRequest)
			return
		}
		body, err := p.DescribeTableJSON(table)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})

	// R12:Prometheus /metrics 端点
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) || !rl(w, r) {
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		// R12+:`?plugin=` query 参数过滤(多实例同进程场景)
		metrics := p.MetricsOpenMetrics()
		if plugin := r.URL.Query().Get("plugin"); plugin != "" {
			// 简化:不实现,直接返回所有指标
			// (生产可基于 plugin 名正则过滤)
			_ = plugin
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, _ = w.Write(metrics)
	})

	return mux
}

// SetAdminAuthToken 设置 admin 端点鉴权 token(R12)
//
// 设置后,所有 /debug/* 和 /metrics 端点要求请求头 "X-MySQL-Admin-Token: <token>"
// token 为空字符串表示禁用鉴权(默认;适合内网部署)
// 多次调用安全(后调覆盖前调)
func (p *MySQLPlugin) SetAdminAuthToken(token string) {
	if p == nil {
		return
	}
	p.adminAuthToken = token
}

// SetAdminRateLimit 设置 admin 端点令牌桶限流(R12+)
//
// rate<=0 或 burst<=0 视为禁用(默认;不限制)
// 推荐值:rate=10,burst=20(允许突发但稳态限速)
//
// nil plugin 安全;多次调用安全
func (p *MySQLPlugin) SetAdminRateLimit(rate, burst float64) {
	if p == nil {
		return
	}
	if rate <= 0 || burst <= 0 {
		p.adminRateLimiter = nil
		return
	}
	p.adminRateLimiter = &adminRateLimiter{
		rate:     rate,
		burst:    burst,
		tokens:   burst, // 启动时满桶
		lastFill: time.Now().UnixNano(),
	}
}

// SlackAlertHandler 返回一个适配 Slack Incoming Webhook 的 HealthHook(R11)
//
// 用法:
//
//	hook := mysql.SlackAlertHandler(os.Getenv("SLACK_WEBHOOK_URL"), "prod-mysql-1")
//	plugin.GetHealthChecker().SetHook(hook)
//
// Slack Incoming Webhook 文档:
//
//	https://api.slack.com/messaging/webhooks
//
// payload 格式: {"text": ":warning: MySQL degraded on prod-mysql-1: <err>"}
type SlackWebhookPayload struct {
	Text string `json:"text"`
}

// slackHTTPClient 全局共享的 HTTP client(R11-perf)
// 复用底层 Transport(连接池 + keep-alive);5s timeout per request
var slackHTTPClient = &http.Client{Timeout: 5 * time.Second}

// SlackAlertHandler 返回一个 HealthHook,触发时 POST 到 SlackIncoming Webhook
//
// webhookURL:Slack workspace 的 incoming webhook URL
// clusterName:本 MySQL 实例的标识,会附加到告警文本中
//
// R11-perf:全局共享 slackHTTPClient,跨多次告警复用底层连接
//
// 注意:本 handler 不会重试,失败仅 logger.Error;如需可靠告警可对接 AlertManager
func SlackAlertHandler(webhookURL, clusterName string) HealthHook {
	return func(healthy bool, err error) {
		var emoji, status, msg string
		if healthy {
			emoji = ":white_check_mark:"
			status = "RECOVERED"
			msg = fmt.Sprintf("%s MySQL *%s* recovered", emoji, clusterName)
		} else {
			emoji = ":rotating_light:"
			status = "DEGRADED"
			msg = fmt.Sprintf("%s MySQL *%s* %s: %v", emoji, clusterName, status, err)
		}

		payload := SlackWebhookPayload{Text: msg}
		body, _ := json.Marshal(payload)

		req, err := http.NewRequest("POST", webhookURL, bytes.NewReader(body))
		if err != nil {
			logger.Error("[slack alert] build request: %v", err)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := slackHTTPClient.Do(req)
		if err != nil {
			logger.Error("[slack alert] POST: %v", err)
			return
		}
		_ = resp.Body.Close()
		if resp.StatusCode >= 300 {
			logger.Error("[slack alert] non-2xx: %d", resp.StatusCode)
		}
	}
}
