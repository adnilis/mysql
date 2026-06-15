# 可观测性(Observability)

> 指标 / 慢查询 / 健康检查 / Admin HTTP 端点 / PII 脱敏
>
> 面向:需要监控生产 MySQL 集群、排查问题、集成告警的 SRE/运维

---

## 0. 一览

| 组件 | R 轮次 | 暴露方式 | 用途 |
|---|---|---|---|
| 内存指标(atomic) | R06 | `Stats()` / `StatsJSON()` | 实时计数 |
| 慢查询缓冲 | R09 | `SlowQueries()` / `/debug/slow` | postmortem |
| 健康检查 | R09 | `HealthChecker` + `HealthHook` | 故障检测 + 自动重连 |
| Slack 告警 | R11 | `SlackAlertHandler(webhookURL, clusterName)` | 集成 Slack Incoming Webhook |
| Admin HTTP 端点 | R11 | `AdminHandler()` http.Handler | `/debug/*` 调试 |
| Prometheus 输出 | R12+R13 | `MetricsOpenMetrics()` / `/metrics` | SLO 抓取(p50/p95/p99) |
| 限流 | R12+ | `SetAdminRateLimit(rate, burst)` | admin 端点防刷 |
| Auth 鉴权 | R12 | `SetAdminAuthToken(token)` | admin 端点 token 校验 |
| 慢查询 PII 脱敏 | R13 | `SlowQueriesJSON(doRedact)` | 防 password/token 泄露 |
| 熔断器 | R13 | `RetryBudget` + `WithRetryBudget` | 防重试雪崩 |

---

## 1. 内存指标(R06)

### 1.1 指标列表

| 字段 | 类型 | 含义 |
|---|---|---|
| `QueryTotal` | counter | 总查询次数(含成功+失败) |
| `QueryErrors` | counter | 查询错误次数 |
| `QuerySlow` | counter | 慢查询次数(超 SlowThreshold) |
| `RowsRead` | counter | 总读取行数(SELECT 类) |
| `RowsAffected` | counter | 总影响行数(DML) |
| `OpenConnections` | gauge | 池中总打开连接 |
| `InUse` | gauge | 池中正在使用 |
| `Idle` | gauge | 池中空闲 |

### 1.2 Stats()

```go
stats := plugin.Stats()
fmt.Printf("QPS=%.1f Slow=%d Errors=%d\n",
    float64(stats.QueryTotal)/elapsed.Seconds(),
    stats.QuerySlow, stats.QueryErrors)
```

**特性**:`Stats()` 完全无锁,周期采样 0 开销。

### 1.3 接入示例(15s 周期采样)

```go
ticker := time.NewTicker(15 * time.Second)
go func() {
    for range ticker.C {
        s := plugin.Stats()
        prometheus.Gauge("mysql_query_total", float64(s.QueryTotal))
        prometheus.Gauge("mysql_query_slow_total", float64(s.QuerySlow))
        prometheus.Gauge("mysql_query_errors_total", float64(s.QueryErrors))
        prometheus.Gauge("mysql_open_conns", float64(s.OpenConnections))
        prometheus.Gauge("mysql_in_use", float64(s.InUse))
    }
}()
```

---

## 2. 慢查询缓冲(R09)

### 2.1 行为契约

- 容量可配(默认 100);满后覆盖最旧条目
- `Record(query, args, duration, rows)` 由 `QueryLogger.SlowQueryHook` 自动触发
- `Snapshot()` 返回时间倒序列表(最新在前)
- `Reset()` 清空

### 2.2 接入示例

```go
buf := mysql.NewSlowQueryBuffer(100)
plugin.AttachSlowBuffer(buf)

// 后期:
//   1. 周期采样(plugin.SlowQueries())
//   2. HTTP 端点暴露(/debug/slow)
//   3. 自定义钩子(plugin.queryLogger.SetSlowQueryHook(...))
```

### 2.3 性能

| 操作 | 数据 |
|---|---|
| Record(写路径) | 40 ns/op, 1 alloc |
| Snapshot(读 1000 条) | 13586 ns/op, 1 alloc(完整副本) |

读路径无锁(head/full atomic),写路径 mutex 短临界区。

---

## 3. Admin HTTP 端点(R11 + R12 + R13)

### 3.1 路由总览

| 端点 | 输出 | 缓存 |
|---|---|---|
| `GET /debug/stats` | `MySQLStatsJSON` | 100ms TTL(R11-perf) |
| `GET /debug/slow` | `[]SlowQueryJSON`(默认 PII 脱敏) | 无 |
| `GET /debug/table/{name}` | `TableInfoJSON` | 30s TTL(R11-perf) |
| `GET /metrics` | OpenMetrics 1.0 文本 | 无 |
| `GET /metrics?plugin=xxx` | OpenMetrics(占位过滤) | 无 |

### 3.2 集成示例

```go
// 选项 A:独立 HTTP server
go http.ListenAndServe(":8080", plugin.AdminHandler())

// 选项 B:wma 框架 admin mux
app.AdminMux().Handle("/mysql/", http.StripPrefix("/mysql", plugin.AdminHandler()))

// 选项 C:Prometheus 抓取
// scrape_configs:
//   - job_name: 'wma-mysql'
//     metrics_path: /metrics
//     static_configs:
//       - targets: ['mysql-host:8080']
```

### 3.3 鉴权(R12)

```go
plugin.SetAdminAuthToken("my-secret-token")
```

- 设置后,所有端点要求 `X-MySQL-Admin-Token` 头
- 空字符串=禁用鉴权(默认)
- 多次调用安全(后调覆盖前调)

### 3.4 限流(R12+)

```go
plugin.SetAdminRateLimit(10, 20)  // 10 req/s,burst 20
```

- 超限返回 `429 Too Many Requests` + `Retry-After: 1` 头
- 令牌桶:`math.Float64bits` + `atomic.CompareAndSwapUint64`,完全无锁
- 0/0 显式禁用,默认不限流

### 3.5 完整生产配置

```go
plugin := mysql.NewMySQLPlugin("prod-mysql-1", &cfg)
plugin.SetAdminAuthToken(os.Getenv("MYSQL_ADMIN_TOKEN"))
plugin.SetAdminRateLimit(10, 20)
plugin.AttachSlowBuffer(mysql.NewSlowQueryBuffer(200))
checker := mysql.NewHealthChecker(30*time.Second, 3)
checker.SetHook(mysql.SlackAlertHandler(os.Getenv("SLACK_WEBHOOK_URL"), "prod-mysql-1"))
plugin.AttachHealthChecker(checker)
```

---

## 4. Prometheus 指标(R12 + R13)

### 4.1 指标清单

| 名称 | 类型 | 含义 |
|---|---|---|
| `mysql_query_total` | counter | 总查询次数 |
| `mysql_query_slow_total` | counter | 慢查询次数 |
| `mysql_query_errors_total` | counter | 错误查询次数 |
| `mysql_rows_read_total` | counter | 总读取行数 |
| `mysql_rows_affected_total` | counter | 总影响行数 |
| `mysql_connections{state="open\|in_use\|idle"}` | gauge | 连接池状态 |
| `mysql_health` | gauge | 1=up, 0=down |
| `mysql_query_duration_seconds{le="..."}` | histogram | 延迟分布(11 桶) |
| `mysql_query_duration_seconds_sum` | gauge | 总延迟秒数 |
| `mysql_query_duration_seconds_count` | gauge | 总样本数 |

### 4.2 Histogram 桶(R13)

```
1ms / 5ms / 10ms / 50ms / 100ms / 500ms / 1s / 5s / 10s / 30s / 60s
```

覆盖 MySQL 查询常见延迟分布。Prometheus 用 `histogram_quantile(0.95, ...)` 算 p95。

### 4.3 输出示例

```
# HELP mysql_query_total Total number of SQL queries executed by this plugin.
# TYPE mysql_query_total counter
mysql_query_total{plugin="prod-mysql-1"} 12345

# HELP mysql_query_duration_seconds Query latency distribution in seconds.
# TYPE mysql_query_duration_seconds histogram
mysql_query_duration_seconds{plugin="prod-mysql-1",le="0.001"} 8000
mysql_query_duration_seconds{plugin="prod-mysql-1",le="0.005"} 11500
mysql_query_duration_seconds{plugin="prod-mysql-1",le="+Inf"} 12345
mysql_query_duration_seconds_sum{plugin="prod-mysql-1"} 23.4
mysql_query_duration_seconds_count{plugin="prod-mysql-1"} 12345
```

### 4.4 派生 SLO 查询

```promql
# p95 query 延迟(秒)
histogram_quantile(0.95, rate(mysql_query_duration_seconds_bucket{plugin="prod-mysql-1"}[5m]))

# 错误率
rate(mysql_query_errors_total[5m]) / rate(mysql_query_total[5m])

# 慢查询率
rate(mysql_query_slow_total[5m]) / rate(mysql_query_total[5m])

# 池利用率
mysql_connections{state="in_use"} / mysql_connections{state="open"}
```

### 4.5 性能

- 输出 `MetricsOpenMetrics()` 走 `strings.Builder` 拼装
- 桶边界预格式化(`histogramBucketsStr` 在 `init()` 生成)
- histogram 计数全部 `atomic.AddInt64`,无锁

---

## 5. 健康检查(R09 + R11)

### 5.1 行为契约

| 事件 | 动作 |
|---|---|
| 周期 ping(默认 30s) | `db.PingContext(1s timeout)` |
| 单次失败 | 累加 `consecutive` 失败计数 |
| 连续 N 次失败(默认 3) | 标记 `healthy=false`,触发 `HealthHook(false, err)` |
| 持续 ping,首次成功 | 标记 `healthy=true`,触发 `HealthHook(true, nil)` |
| 自动重连 | 重建 db 句柄并替换 `p.db`(事务级) |

### 5.2 接入示例

```go
checker := mysql.NewHealthChecker(30*time.Second, 3)
checker.SetHook(func(healthy bool, err error) {
    if !healthy {
        log.Printf("MySQL DEGRADED: %v", err)
    }
})
plugin.AttachHealthChecker(checker)
// Start 内部自动启动;Stop 自动停止
```

### 5.3 Slack 告警集成(R11)

```go
checker.SetHook(mysql.SlackAlertHandler(os.Getenv("SLACK_WEBHOOK_URL"), "prod-mysql-1"))
```

- 降级时:`:rotating_light: MySQL *prod-mysql-1* DEGRADED: <err>`
- 恢复时:`:white_check_mark: MySQL *prod-mysql-1* recovered`
- 5s 超时;失败仅 `logger.Error`(对接 AlertManager 做可靠重试)
- 全局共享 `slackHTTPClient`(keep-alive 复用)

### 5.4 自定义 Hook

```go
checker.SetHook(func(healthy bool, err error) {
    // 接 PagerDuty / OpsGenie / 企业内部告警
    pager.Notify(healthy, "mysql", err)
    // 同步更新数据库 incident 表
    db.Exec("UPDATE incidents SET healthy=? WHERE service='mysql'", healthy)
})
```

---

## 6. 慢查询 PII 脱敏(R13)

### 6.1 关键词列表

`password` / `passwd` / `pwd` / `token` / `secret` / `api_key` / `apikey` /
`email` / `phone` / `mobile` / `id_card` / `idcard` / `ssn` /
`credit_card` / `creditcard` / `cardno` / `auth` / `credential`

### 6.2 行为

- 对每个 `?` 占位符,检查"前一? 之后到本? 之前"的 SQL 片段
- 命中关键词时,对应 `args[i]` 替换为 `<redacted:N>`
- 默认开启(适合生产);支持 `?redact=0` 关闭(排查时)

### 6.3 输出示例

原始 args:`["alice", "secret123", "a@b.com", "tok_abc"]`
对应 SQL:`SELECT * FROM users WHERE name = ? AND password = ? AND email = ? AND token = ?`

脱敏后 args(默认):
`["alice", "<redacted:1>", "<redacted:2>", "<redacted:3>"]`

### 6.4 已知限制

不做完整 SQL 解析,启发式判断。`INSERT (col1, password) VALUES (?, ?)` 首个 ? 也会脱敏(过保守,安全优先)。

---

## 7. 熔断器(R13)

### 7.1 `RetryBudget` 行为

| 状态 | 行为 |
|---|---|
| 计数 < 阈值 | `Allow()` = true,正常重试 |
| 计数 >= 阈值 | `Allow()` = false,持续 cooldown |
| cooldown 到期 | `Allow()` = true(半开,试探) |
| 半开 + 试探成功 | 关闭熔断器,计数清零 |
| 试探失败 | 重新开启,新一轮 cooldown |

### 7.2 接入示例

```go
err := mysql.WithRetryBudget(ctx, DefaultRetryPolicy(), func(ctx context.Context) error {
    _, err := plugin.Exec(ctx, "UPDATE orders SET stock = stock - 1 WHERE id = ?", id)
    return err
})
```

- 全局默认 50 次/分钟阈值,50 次失败后熔断 1 分钟
- 替换 `WithRetry` 即可启用熔断
- 适配"下游已故障时盲目重试雪崩"场景

### 7.3 调优

```go
// 调高阈值(更激进重试)
globalRetryBudget = mysql.NewRetryBudget(100, 30*time.Second)

// 或在自家包中重新赋值
```

---

## 8. 端到端使用示例

### 8.1 最小生产配置

```go
cfg := &mysql.MySQLPluginConfig{
    Addr:         "10.0.0.1:3306",
    User:         "app",
    Password:     "secret",
    DBName:       "prod",
    PoolSize:     100,
    MinIdleConns: 20,
    MaxIdleConns: 50,
    EnableQueryLog: true,
    SlowThreshold: 100,  // ms
}
plugin := mysql.NewMySQLPlugin("prod-mysql-1", cfg)
plugin.AttachSlowBuffer(mysql.NewSlowQueryBuffer(200))
plugin.AttachHealthChecker(mysql.NewHealthChecker(30*time.Second, 3))
checker := ... // get healthChecker ref
checker.SetHook(mysql.SlackAlertHandler(os.Getenv("SLACK_WEBHOOK_URL"), "prod-mysql-1"))
plugin.SetAdminAuthToken(os.Getenv("MYSQL_ADMIN_TOKEN"))
plugin.SetAdminRateLimit(10, 20)

app.RegisterPlugin(plugin)
app.Run()
```

### 8.2 Prometheus 抓取

```yaml
scrape_configs:
  - job_name: 'wma-mysql'
    metrics_path: /metrics
    static_configs:
      - targets: ['mysql-host:8080']
    relabel_configs:
      - source_labels: [__address__]
        target_label: instance
        replacement: prod-mysql-1
```

### 8.3 告警规则(Prometheus AlertManager)

```yaml
groups:
- name: mysql
  rules:
  - alert: MySQLDown
    expr: mysql_health{plugin="prod-mysql-1"} == 0
    for: 1m
    annotations:
      summary: "MySQL instance {{ $labels.plugin }} is down"
  - alert: MySQLHighErrorRate
    expr: rate(mysql_query_errors_total[5m]) / rate(mysql_query_total[5m]) > 0.01
    for: 5m
    annotations:
      summary: "MySQL error rate > 1% on {{ $labels.plugin }}"
  - alert: MySQLSlowQueries
    expr: rate(mysql_query_slow_total[5m]) > 1
    for: 5m
    annotations:
      summary: "MySQL slow queries detected on {{ $labels.plugin }}"
  - alert: MySQLPoolSaturated
    expr: mysql_connections{state="in_use"} / mysql_connections{state="open"} > 0.9
    for: 1m
    annotations:
      summary: "MySQL pool > 90% utilized on {{ $labels.plugin }}"
```

---

## 9. 进一步阅读

- [architecture.md](architecture.md) — 内部设计
- [performance.md](performance.md) — 性能优化与基准
- [migration.md](migration.md) — DAO 迁移
- [api-reference.md](api-reference.md) — 完整 API
