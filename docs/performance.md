# 性能优化(Performance)

> R04-R13 累计性能优化专项
>
> 面向:需要优化高 QPS 场景或诊断性能瓶颈的开发者

---

## 0. 性能基线对比

| 基准 | R03 之前 | R11-perf 后 | 提升 |
|---|---|---|---|
| `BuildQueryPooled_AcquireRelease` | ~9 allocs | **3 allocs** | **-67%** |
| `FormatQuery` (with args) | 1178 ns, 9 allocs | **1000 ns, 8 allocs** | -15% |
| `FormatQuery` (no args) | n/a | 478 ns, 5 allocs | (新基线) |
| `ValsPool` (Insert) | n/a | 75 ns, 4 allocs | (新基线) |
| `StatsJSON_CachedHit` | n/a | **50 ns** | 100ms 缓存命中 |
| `PrepareCache_Hit` | n/a | 76 ns, 1 alloc | (新基线) |
| `SlowQueryBuffer_Record` | n/a | 40 ns, 1 alloc | (新基线) |
| `HashQuery_FNV` (R11-perf) | n/a | 66 ns, 16 B | (FNV-1a vs SHA256) |

---

## 1. 优化项时间线

### R04 — 连接池预热 + 编辑表算法

| 优化 | 文件 | 收益 |
|---|---|---|
| 反射缓存 `fieldMeta` | scanner.go | `buildXxxSQL` 稳态 0 反射 |
| 预构建 SQL 模板 `insertSQL` / `updateByIDSQL` | scanner.go | 同 type 跨调用零字符串拼接 |
| 短小写 ASCII 关键字比对 | build.go (R04 改造) | 5 处 `strings.ToUpper` 拷贝消除 |
| `whereOp` 位标志 | query.go | 拼接时无 `HasPrefix` 扫描 |
| `editList` 排序 | build.go | 拼接 O(n) 排序,n ≤ 7 固定 |
| `effectiveMaxIdleConns` | pool.go | 解决 `SetMinIdleConns` 缺失 |

### R05 — Logger + MapScan

| 优化 | 文件 | 收益 |
|---|---|---|
| `formatTableNames` 改 `strings.Builder` | logger.go | 5 处拷贝 + N 处拼接 → 1 处 builder |
| `argStrsPool` | logger.go | `[]string` in formatQuery 复用 |
| `valsPool` | scanner.go | 写路径 1 alloc/call → 0 |
| `withOp` | query.go | `Or/Not` 不再 prefix 扫描 |

### R06 — 内存指标 + 池化

| 优化 | 文件 | 收益 |
|---|---|---|
| 5 个 `atomic.Int64` 指标 | plugin.go / orm.go | `Stats()` 零锁 |
| `scratchEdits` / `scratchArgs` 字段 | query.go / pool.go | `buildQuery` 0 alloc |
| `getTableNameFromDest` 缓存 | helper.go | `First` 路径反射 0 alloc |
| `hex.EncodeToString` for `[]byte` | logger.go (R06) | 2x 加速大字节数组场景 |

### R07 — 连接池预热 + Schema 自省

| 优化 | 文件 | 收益 |
|---|---|---|
| `warmupPool` 后台预热 | pool.go | 冷启动首波 P99 飙升消除 |
| `DescribeTable` 30s 缓存 | schema.go (R11) | admin 端点零 DB 命中 |
| `StatsJSON` 100ms 缓存 | admin.go (R11) | 高频轮询 1 次 marshal/100ms |

### R08 — 批量更新 + 预编译

| 优化 | 文件 | 收益 |
|---|---|---|
| `BulkUpdate` CASE WHEN | orm.go | N 次网络 → 1 次 |
| `PrepareCache` LRU | prepare_cache.go | 同 SQL 复用 *sqlx.Stmt |
| `hashQuery` 改 FNV-1a | prepare_cache.go (R11-perf) | 哈希 5-10x 加速 |

### R09 — 可靠性

| 优化 | 文件 | 收益 |
|---|---|---|
| `HealthChecker` 后台 ping | health.go | 降级后 query 立即返回,不死等 |
| `WithRetry` 死锁重试 | retry.go | 自动退避 + 抖动 |
| `SlowQueryBuffer` 环形缓冲 | slow_query_buffer.go | 最近 100 条慢查询保留 |

### R11-perf — 7 项专项

| 优化 | 文件 | 收益 |
|---|---|---|
| `WithRetry` `NewTimer+defer Stop` | retry.go | 消除 timer 泄漏 |
| `SlowQueryBuffer` 改 atomic | slow_query_buffer.go | 读路径无锁 |
| 30s schema 缓存 | schema.go | admin 零 DB |
| 100ms stats 缓存 | admin.go | 高频零 marshal |
| SlackAlertHandler 共享 client | admin.go | keep-alive 复用 |
| FNV-1a 哈希 | prepare_cache.go | 5-10x 加速 |
| HealthChecker 静态 ping ctx | health.go | 零 context 分配 |

### R12 — 限流 + Admin 增强

| 优化 | 文件 | 收益 |
|---|---|---|
| `adminRateLimiter` 令牌桶 | admin.go | admin 端点防刷 |
| `MetricsOpenMetrics` Prometheus 输出 | admin.go | 标准 SLO 计算 |

### R13 — Histogram + 安全

| 优化 | 文件 | 收益 |
|---|---|---|
| `mysql_query_duration_seconds` 直方图 | admin.go | p50/p95/p99 SLO |
| `redactArgs` PII 脱敏 | admin.go | 防止慢查询泄露密码/token |
| `RetryBudget` 熔断器 | retry_budget.go | 防雪崩 |

---

## 2. 关键基准数据详解

### 2.1 `BuildQueryPooled_AcquireRelease`

```go
// 链式 5 个 Where + Join + Limit
qr := plugin.Query(ctx, "SELECT * FROM users")
qr.LeftJoin("orders", "users.id = orders.user_id", 1)
qr.Where("age > ?", 18)
qr.Where("status = ?", "active")
qr.Where("country = ?", "CN")
qr.Where("level >= ?", 10)
qr.Where("vip = ?", true)
qr.Limit(50)
_, _ = qr.buildQuery()
releaseMySQLQueryResult(qr)
```

**结果**:
- 145 ns/op
- 3 allocs/op(底层切片预分配 + 1 字符串 + 1 args slice)
- 优化前:~9 allocs(每次 buildQuery 都 make 切片)

### 2.2 `FormatQuery` (with args)

```go
ql := NewQueryLogger(...)
query := "SELECT id, name FROM users ... WHERE age > ? AND status = ? AND city = ?"
_ = ql.formatQuery(query, 18, "active", "Beijing")
```

**结果**:
- 1000 ns/op(优化前 1178 ns)
- 8 allocs/op
- 节省主要来自 `strings.Builder` 替代字符串切片拼接

### 2.3 `SlowQueryBuffer_Record` / `Snapshot`

```go
buf := NewSlowQueryBuffer(1000)
for i := 0; i < 1000; i++ {
    buf.Record("SELECT ?", []any{i}, time.Millisecond, int64(i))
}
_ = buf.Snapshot()
```

**结果**:
- Record: 40 ns/op, 1 alloc(记录结构)
- Snapshot(1000 条): 13586 ns/op, 1 alloc(完整副本)

### 2.4 `StatsJSON_CachedHit`

```go
plugin, _ := newBenchPlugin(b)
_, _ = plugin.StatsJSON()  // 预热
for i := 0; i < b.N; i++ {
    _, _ = plugin.StatsJSON()  // 100ms 缓存命中
}
```

**结果**:
- 50 ns/op, 1 alloc(返回 []byte 副本)
- 100ms 内命中缓存,免 marshal

### 2.5 `PrepareCache_Hit`

```go
cache := NewPrepareCache(64)
_, _ = cache.Prepare(ctx, db, "SELECT * FROM users WHERE id = ?")  // 首次
for i := 0; i < b.N; i++ {
    _, _ = cache.Prepare(ctx, db, "SELECT * FROM users WHERE id = ?")  // 命中
}
```

**结果**:
- 76 ns/op, 1 alloc
- 同 SQL 复用 *sqlx.Stmt,消除 driver 反复 Prepare/Close

### 2.6 `HashQuery_FNV`

```go
q := "SELECT id, name, email FROM users WHERE age > ? AND status = ? AND created_at > ?"
_ = hashQuery(q)
```

**结果**:
- 66 ns/op, 16 B/op
- FNV-1a 比 SHA256 快约 5-10x(无加密轮)

---

## 3. 对象池设计

### 3.1 MySQLQueryResult Pool

| 字段 | 容量 | 复用 |
|---|---|---|
| `joins` | 4 | reset 截断,保留底层数组 |
| `wheres` | 8 | 同上 |
| `groups` | 2 | 同上 |
| `havings` | 2 | 同上 |
| `orders` | 4 | 同上 |
| `args` | 16 | 同上 |
| `scratchEdits` (R11) | 7 | buildQuery 复用 |
| `scratchArgs` (R11) | 24 | buildQuery 复用 |

**关键设计**:`qr.args[:0]` 而非 `qr.args = nil`,保留底层数组容量。

### 3.2 valsPool

```go
var valsPool = sync.Pool{
    New: func() any { s := make([]any, 0, 16); return &s },
}

// 使用:
vp := valsPool.Get().(*[]any)
vals := (*vp)[:0]
// ... 填充 vals
defer valsPool.Put(vp)  // 调用方负责归还
```

**应用场景**:Insert/Update/UpdateByID 写路径,1 alloc/call → 0 alloc/call。

### 3.3 argStrsPool

```go
var argStrsPool = sync.Pool{
    New: func() any { s := make([]string, 0, 8); return &s },
}
```

`formatQuery` 内部 `[]string` 复用。

---

## 4. 字符串处理优化

### 4.1 ASCII 关键字比对(R04 改造)

```go
// 旧:5 次 strings.ToUpper(query) + strings.Contains
queryUpper := strings.ToUpper(query)
if strings.Contains(queryUpper, "SELECT *") { ... }

// 新:1 次 lowerASCII 字节循环
if containsKeywordFold(query, "SELECT *") { ... }

func containsKeywordFold(query, kw string) bool {
    for i := 0; i+len(kw) <= len(query); i++ {
        match := true
        for j := 0; j < len(kw); j++ {
            if lowerASCII(query[i+j]) != lowerASCII(kw[j]) {
                match = false
                break
            }
        }
        if match { return true }
    }
    return false
}
```

**收益**:消除 5 次 `strings.ToUpper` 全量拷贝。

### 4.2 FNV-1a 哈希(R11-perf)

```go
func hashQuery(q string) string {
    h := fnv.New64a()
    _, _ = h.Write([]byte(q))
    return strconv.FormatUint(h.Sum64(), 16)
}
```

**收益**:哈希速度约 5-10x,SHA256 包含加密轮对缓存场景过度。

### 4.3 hex.EncodeToString for `[]byte` (R06)

```go
case []byte:
    return "0x" + hex.EncodeToString(v)
```

替代 `fmt.Sprintf("0x%x", v)`,避免 `fmt` 反射路径。

---

## 5. 反射优化

### 5.1 fieldMeta 缓存(R02)

```go
var metaCache sync.Map  // reflect.Type → *fieldMeta
```

- 写入:一次性反射 + 预构建 SQL 模板
- 读取:Map.Load 1 跳
- 节省:每 type 跨调用零反射

### 5.2 tableNameCache(R04)

```go
var tableNameCache sync.Map  // reflect.Type → string
```

- 写入:第一次 `getTableNameFromDest` 后缓存
- 读取:Map.Load 1 跳
- 节省:`First(&user)` 路径反射 0 alloc

### 5.3 反射使用边界

| 场景 | 反射频次 |
|---|---|
| `Insert(model)` | 首次(缓存 hit) |
| `Update(model, where)` | 首次(缓存 hit) |
| `First(&dest)` | 0(走 cached `tableName`) |
| `GetTableNameFromDest(&map)` | 0(直接处理 slice 类型) |

---

## 6. 上下文与取消

### 6.1 静态 ctx 复用(R11-perf)

```go
// HealthChecker 内部
pingCtx, pingCancel := context.WithCancel(context.Background())
hc.pingTimeout = pingCtx
```

30s 周期的 ping 复用同一 ctx,消除每周期 `WithTimeout` 分配。

### 6.2 cancel 回收(R11)

```go
func (qr *MySQLQueryResult) reset() {
    if qr.cancel != nil {
        qr.cancel()
        qr.cancel = nil
    }
}
```

`WithTimeout` 注入的 cancel 在对象池归还时调用,防止 ctx 泄漏。

### 6.3 timer 回收(R11-perf)

```go
// 旧:time.After 泄漏(长退避累积)
case <-time.After(sleep):

// 新:NewTimer + defer Stop
timer := time.NewTimer(sleep)
select {
case <-ctx.Done():
    timer.Stop()
    return ctx.Err()
case <-timer.C:
}
```

---

## 7. 内存模型

### 7.1 原子计数

```go
type MySQLPlugin struct {
    db                 atomic.Pointer[sqlx.DB]  // 句柄
    metricQueryTotal   atomic.Int64           // 指标
    metricQuerySlow    atomic.Int64
    metricRowsRead     atomic.Int64
    metricRowsAffected atomic.Int64
}
```

**原则**:热路径 atomic,冷路径 mutex。
StatsJSON、logQ、First/Find 全部走 atomic 读,锁竞争为 0。

### 7.2 SlowQueryBuffer 锁策略(R11-perf)

```go
type SlowQueryBuffer struct {
    mu       sync.Mutex
    cap      int
    buf      []SlowQueryRecord
    head     int64   // atomic,无锁读
    fullFlag int32   // atomic,无锁读
}
```

- 写路径:mutex 保护(临界区仅赋值)
- 读路径:atomic.Load head/full,mutex 内仅做 `copy` 切片,mutex 外反序

### 7.3 锁竞争矩阵

| 资源 | 写频次 | 读频次 | 竞争 |
|---|---|---|---|
| `p.db` | Start/Stop | 每次 query | 极低(atomic) |
| `p.state` | Init/Start/Stop | 每次 Stats | 低(RWMutex) |
| `slowBuffer.buf` | 每次慢查询 | 每次 /debug/slow | 中(锁长度短) |
| `healthChecker.mu` | 状态变化时 | 每次 ping | 极低 |
| `PrepareCache.mu` | 每次 Prepare | 每次 Prepare | 中(短临界区) |
| `RetryBudget.mu` | 每次 Record | 每次 Allow | 极低 |

---

## 8. 高 QPS 优化清单

如果你需要支撑 10K+ QPS,按顺序检查:

1. **检查连接池配置**:`PoolSize` 应 ≥ `2 × CPU 核数`,`MinIdleConns` 接近 PoolSize
2. **启用 `PrepareCache`**:`cache := NewPrepareCache(128)`,缓存同 SQL 预编译
3. **关闭慢查询日志**:`EnableQueryLog: false`(生产默认关),仅留 `SlowThreshold` 告警
4. **关 `AttachSlowBuffer`** 在高 QPS 场景(写入环形缓冲是热路径);改用 `SetSlowQueryHook` 接 metrics
5. **使用标量 `First`**:避免反射结构体
6. **使用 `MapScan` `Find`**:避免声明结构体
7. **批量用 `BatchExec` / `BulkUpdate`**:N 次网络 → 1 次
8. **批量用 `RunInTransaction`**:多步写 1 个事务
9. **大结果集分页 `Page(page, size)`**:避免一次性 FetchAll
10. **指标通过 `Stats()` 周期采样**:`StatsJSON` 已带 100ms 缓存,直接 `/metrics` 抓取

---

## 9. 性能故障排查

### 9.1 怀疑 query 构建慢

```bash
# 跑基准
go test -bench BenchmarkBuildQueryPooled -benchmem -benchtime=5s
# 若 > 500 ns,可能反射缓存命中率低 — 检查 fieldMeta 重复
```

### 9.2 怀疑 logger 热路径慢

```bash
go test -bench BenchmarkFormatQuery -benchmem
# 若 > 2000 ns,可能 args 切片未复用 — 检查 argStrsPool
```

### 9.3 怀疑连接池满

```bash
curl /metrics | grep mysql_connections
# 查 in_use / open 比例:若接近 1:1,池满
# 解决:加大 PoolSize / 减少 query 持有时间
```

### 9.4 怀疑 schema 自省慢

```bash
go test -bench BenchmarkDescribeTable -benchmem
# 第二次调用应 0 分配(命中 30s 缓存)
```

---

## 10. 进一步阅读

- [architecture.md](architecture.md) — 内部数据结构与算法
- [observability.md](observability.md) — 指标 / 慢查询 / 健康检查
- [migration.md](migration.md) — DAO 迁移示例
- [api-reference.md](api-reference.md) — 完整公开 API
