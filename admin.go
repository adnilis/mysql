package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/adnilis/logger"
)

// SlowQueryJSON 慢查询的 JSON 序列化视图(R11)
//
// 适配 admin HTTP 端点:POST /debug/slow 返回此结构数组
// 比 SlowQueryRecord 更省字段,适合网络传输
type SlowQueryJSON struct {
	Query    string `json:"query"`
	Duration string `json:"duration"` // 人类可读
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

// MarshalJSONTime 工具:把 time.Duration 序列化为 "1.234s" 形式
func durationString(d time.Duration) string {
	return d.String()
}

// SlowQueriesJSON 返回慢查询快照的 JSON 字节(R11)
//
// 适配 admin HTTP 端点:GET /debug/slow → 直接 Write
// 含 trace context 友好字段
func (p *MySQLPlugin) SlowQueriesJSON() ([]byte, error) {
	records := p.SlowQueries()
	out := make([]SlowQueryJSON, 0, len(records))
	for _, r := range records {
		out = append(out, SlowQueryJSON{
			Query:    r.Query,
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

// AdminHandler 返回一个 http.Handler 暴露 R11 admin 端点(R11)
//
// 路由:
//
//	GET /debug/stats        → StatsJSON
//	GET /debug/slow         → SlowQueriesJSON (最近 100 条)
//	GET /debug/table/{name} → DescribeTableJSON
//
// 挂到 wma admin mux 或独立 HTTP server 即可使用
func (p *MySQLPlugin) AdminHandler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/debug/stats", func(w http.ResponseWriter, r *http.Request) {
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
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := p.SlowQueriesJSON()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})

	mux.HandleFunc("/debug/table/", func(w http.ResponseWriter, r *http.Request) {
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

	return mux
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
