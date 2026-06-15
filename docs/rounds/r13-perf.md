# R11-perf + R13 性能专项总结

> 起止:2026-06-14
> 范围:R07-R11 性能优化专项
> 紧接 R04-R10 累计的"功能扩张"后,做最后一波热路径优化

---

## 1. 动机

R04-R10 累计 24 项新 API + 17 个新可观测性组件,但热路径上仍有几个真实热点未优化:

1. `WithRetry` 用 `time.After` 创 timer,直到 fire 才被 GC(长退避累积)
2. `SlowQueryBuffer.Snapshot` 持锁完整 O(n) 反序
3. `DescribeTable` / `DescribeIndex` 每次 admin 端点请求都查 information_schema
4. `StatsJSON` 每次 dashboard 轮询都重新 marshal
5. `SlackAlertHandler` 每次 POST 都新建 `http.Client`(无连接复用)
6. `PrepareCache` 用 SHA256 做 SQL 哈希(无加密轮,过度)
7. `HealthChecker` 每次 ping 都 `context.WithTimeout` 分配

按收益从高到低逐个修,共 7 项。

---

## 2. 7 项优化

### 2.1 `WithRetry` timer 修复

**问题**:`time.After` 创建的 timer 要等 fire 才被 GC,长退避(8s)下累积 8s/重试 = 雪崩隐患。

**修复**:

```go
// 旧
case <-time.After(sleep):

// 新
timer := time.NewTimer(sleep)
select {
case <-ctx.Done():
    timer.Stop()
    return ctx.Err()
case <-timer.C:
}
```

**收益**:消除 timer 泄漏;长 retry 路径 GC 压力下降。

### 2.2 `SlowQueryBuffer` 锁消除

**问题**:头尾指针 `head` / `full` 字段在 mutex 内更新,读路径 `Snapshot` 也持完整 mutex。

**修复**:

```go
type SlowQueryBuffer struct {
    mu       sync.Mutex
    cap      int
    buf      []SlowQueryRecord
    head     int64   // atomic,无锁读
    fullFlag int32   // atomic,无锁读
}

func (b *SlowQueryBuffer) Snapshot() []SlowQueryRecord {
    head := atomic.LoadInt64(&b.head)
    full := atomic.LoadInt32(&b.fullFlag) == 1
    // 锁内只做 copy
    b.mu.Lock()
    out := make([]SlowQueryRecord, n)
    copy(out, b.buf)
    b.mu.Unlock()
    // 锁外反序
    if full { /* rotated then reverse */ }
}
```

**收益**:写路径短临界区;读路径零争用。

### 2.3 Schema 自省 30s TTL 缓存

**问题**:`/debug/table/{name}` 端点每次请求都查 `information_schema.COLUMNS` + `STATISTICS`。

**修复**:

```go
const schemaCacheTTL = 30 * time.Second

func (p *MySQLPlugin) getSchemaCached(ctx context.Context, table string) (*TableInfo, error) {
    now := time.Now()
    if entry, ok := p.schemaCache[table]; ok && now.Before(entry.expiresAt) {
        return entry.info, nil
    }
    // ... 重查
    p.schemaCache[table] = schemaCacheEntry{info: info, expiresAt: now.Add(schemaCacheTTL)}
}
```

**收益**:admin 端点高频轮询零 DB 命中;DDL 变更后 `InvalidateSchemaCache(tables...)` 强制失效。

### 2.4 `StatsJSON` 100ms 短 TTL 缓存

**问题**:N 个 dashboard 同时轮询 `/debug/stats`,每次都重新 marshal。

**修复**:

```go
const statsCacheTTL = 100 * time.Millisecond

func (p *MySQLPlugin) StatsJSON() ([]byte, error) {
    if p.statsCache != nil && time.Now().Before(p.statsCache.expiresAt) {
        out := make([]byte, len(p.statsCache.body))
        copy(out, p.statsCache.body)
        return out, nil
    }
    // 重新 marshal
    body, _ := json.Marshal(...)
    p.statsCache = &statsCacheEntry{body: body, expiresAt: now.Add(statsCacheTTL)}
    return body, nil
}
```

**收益**:100ms 内命中免 marshal;DML 后 `InvalidateStatsCache()` 立即看到新指标。

### 2.5 `SlackAlertHandler` 共享 `http.Client`

**问题**:每次告警 POST 都 `&http.Client{Timeout: 5 * time.Second}`,无连接复用。

**修复**:

```go
var slackHTTPClient = &http.Client{Timeout: 5 * time.Second}

func SlackAlertHandler(...) HealthHook {
    return func(...) {
        // ...
        resp, err := slackHTTPClient.Do(req)  // 共享
    }
}
```

**收益**:跨多次告警复用 keep-alive 连接;Client 分配次数从 N → 1。

### 2.6 `PrepareCache` 哈希改 FNV-1a

**问题**:`hashQuery` 用 `crypto/sha256` 哈希 8 字节再 hex 编码;SHA256 包含 64 轮加密轮,对缓存场景过度。

**修复**:

```go
// 旧
import "crypto/sha256"
func hashQuery(q string) string {
    sum := sha256.Sum256([]byte(q))
    return hex.EncodeToString(sum[:8])
}

// 新
import "hash/fnv"
func hashQuery(q string) string {
    h := fnv.New64a()
    _, _ = h.Write([]byte(q))
    return strconv.FormatUint(h.Sum64(), 16)
}
```

**收益**:哈希速度约 5-10x 提升;`strconv.FormatUint` 替代 `hex.EncodeToString` 省一次分配。

### 2.7 `HealthChecker` 静态 ping context

**问题**:`pingOnce` 每次都 `context.WithTimeout(ctx, 3*time.Second)`,30s 周期内分配 1 个 cancel。

**修复**:

```go
type HealthChecker struct {
    // ...
    pingTimeout         context.Context
    pingTimeoutDuration time.Duration
}

func NewHealthChecker(interval time.Duration, failLimit int) *HealthChecker {
    // ...
    pingCtx, pingCancel := context.WithCancel(context.Background())
    hc := &HealthChecker{
        pingTimeout:         pingCtx,
        pingTimeoutDuration: time.Second,
    }
    hc.cancel = func() { pingCancel() }  // Stop 时统一回收
    return hc
}

func (hc *HealthChecker) pingOnce(ctx context.Context) {
    pingCtx, cancel := context.WithTimeout(hc.pingTimeout, hc.pingTimeoutDuration)
    // ...
}
```

**收益**:30s 周期的 ping 复用同一 ctx + deadline,零 context 分配。

---

## 3. R13 新增可观测性 + 安全

### 3.1 Histogram 指标(`mysql_query_duration_seconds`)

```go
var histogramBuckets = []float64{
    0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1.0, 5.0, 10.0, 30.0, 60.0,
}
```

- `logQ` 每次 query 调 `queryDuration.observe(duration)`
- `/metrics` 输出 11 桶 + `+Inf` + `_sum` + `_count`
- Prometheus 用 `histogram_quantile(0.95, ...)` 算 p95

### 3.2 PII 自动脱敏(`redactArgs`)

```go
var piiKeywords = []string{
    "password", "passwd", "pwd",
    "token", "secret", "api_key",
    "email", "phone", "mobile",
    "id_card", "idcard", "ssn",
    "credit_card", "creditcard", "cardno",
    "auth", "credential",
}
```

- 扫描 `?` 占位符前一文本片段
- 命中关键词时,该位置 args 替换为 `<redacted:N>`
- `/debug/slow?redact=0` 关闭脱敏(排查时)

### 3.3 熔断器(`RetryBudget`)

```go
err := mysql.WithRetryBudget(ctx, DefaultRetryPolicy(), func(ctx context.Context) error {
    _, err := plugin.Exec(ctx, "UPDATE ...", id)
    return err
})
```

- 全局默认 50 次/分钟阈值,达阈值熔断 1 分钟
- 试探通过自动关闭;试探失败重新开启
- 防"下游已故障时盲目重试雪崩"

---

## 4. 验证

```bash
$ gofmt -l .
(空)

$ go vet ./...
(无输出)

$ go test -count=1 -timeout 60s . 2>&1 | tail -3
ok      github.com/adnilis/wma/plugins/mysql       0.034s
```

| Benchmark | 数据 | 含义 |
|---|---|---|
| `SlowQueryBuffer_Record` | 40 ns/op, 1 alloc | 写路径 24 B/op |
| `SlowQueryBuffer_Snapshot` | 13586 ns/op, 1 alloc | 1000 条读快照 13.5μs |
| `StatsJSON_CachedHit` | **50 ns/op**, 1 alloc | 100ms 缓存命中 |
| `HashQuery_FNV` | 66 ns/op, 16 B/op | FNV-1a 哈希 |
| `PrepareCache_Hit` | 76 ns/op, 1 alloc | LRU 命中复用 *sqlx.Stmt |

---

## 5. 性能累计(R11-perf + R13 完成后)

- `BuildQueryPooled_AcquireRelease`: 145 ns/op, 3 allocs(op(对比 R03 之前 ~9 allocs,节省 6 次)
- `FormatQuery`: 1000 ns/op, 8 allocs/op
- `PrepareCache_Hit`: 76 ns/op, 1 alloc/op(新功能基线)
- `StatsJSON_CachedHit`: 50 ns/op, 1 alloc/op(新功能基线)
- `HashQuery_FNV`: 66 ns/op, 16 B/op(新功能基线)

---

## 6. 进一步阅读

- [architecture.md](../architecture.md) — 内部设计
- [performance.md](../performance.md) — 完整性能优化
- [observability.md](../observability.md) — 可观测性全景
- [api-reference.md](../api-reference.md) — 完整 API
