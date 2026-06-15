# 架构设计(Architecture)

> wma/plugins/mysql 内部设计深度参考
>
> 面向:需要扩展/调试/理解内部行为的维护者

---

## 0. 包结构总览

```
plugin.go      — MySQLPlugin 生命周期 + 公共入口 + atomic.Pointer db 句柄
pool.go        — Start/Stop + 连接池预热 + 短时 ctx
orm.go         — ORM 高层(Insert/Update/...) + logQ 指标入口
query.go       — 链式 MySQLQueryResult DSL
build.go       — SQL 拼装:edit-list 算法 + 反射缓存
scanner.go     — 模型反射(fieldMeta + SQL 模板预构建)
helper.go      — 表名推断 + 标识符校验 + 错误包装
batch.go       — BatchInsert + BatchExec + BatchInsertOnConflict
transaction.go — Begin/RunInTransaction/WithMockTx + MySQLTransaction
health.go      — HealthChecker 后台 ping + 自动重连
retry.go       — WithRetry 死锁重试
retry_budget.go — RetryBudget 熔断器(R13)
slow_query_buffer.go — 慢查询环形缓冲
prepare_cache.go — LRU 预编译语句缓存
schema.go      — ListTables / DescribeTable / ListIndexes / DescribeIndex
admin.go       — AdminHandler / JSON 视图 / OpenMetrics / PII 脱敏 / 限流
nullable.go    — NullInt64 / NullString
logger.go      — QueryLogger + 慢查询钩子 + SlowQueryHook
config.go      — MySQLPluginConfig 校验
errors.go      — 错误 sentinel
stats.go       — MySQLStats DTO
model.go       — IModel 接口
```

---

## 1. 连接池模型(Connection Pool)

### 1.1 db 句柄的原子发布

```go
type MySQLPlugin struct {
    db atomic.Pointer[sqlx.DB]  // 无锁读
    ...
}
```

设计动机:DAO 高频调用 `plugin.DB()` 或内部 `getDB()`,若用 `sync.RWMutex` 包裹会有锁竞争。
改用 `atomic.Pointer[sqlx.DB]` 后:
- 读路径:`db.Load()` 单条 atomic 指令,无锁
- 写路径(`Start`):`db.Store(db)`,极少调用

### 1.2 Start 时的池预热(R07)

```go
if p.config.MinIdleConns > 0 {
    go p.warmupPool(db, p.config.MinIdleConns)
}
```

`warmupPool` 后台串行 `Ping` N 次,填充 MinIdleConns 个空闲连接。
失败不致命 — 池子按需懒分配。

### 1.3 effectiveMaxIdleConns

```go
func effectiveMaxIdleConns(cfg *MySQLPluginConfig) int {
    maxIdle := cfg.MaxIdleConns
    if cfg.MinIdleConns > maxIdle {
        maxIdle = cfg.MinIdleConns
    }
    return maxIdle
}
```

**关键决策**:Go stdlib `database/sql` 没有 `SetMinIdleConns`(至今未补)。
变通:把 `SetMaxIdleConns` 设为 `max(MaxIdleConns, MinIdleConns)`,保证空闲连接池至少能保留 MinIdleConns 个不被回收。

### 1.4 R07-perf:startup 后的 ping context 静态化

```go
pingCtx, pingCancel := context.WithCancel(context.Background())
hc.pingTimeout = pingCtx
hc.pingTimeoutDuration = time.Second
```

`HealthChecker` 周期 ping 复用静态 context,避免每次 `WithTimeout` 分配。

### 1.5 自动重连(R09 / R11)

```go
func (hc *HealthChecker) reconnect(p *MySQLPlugin) error {
    db, err := sqlx.Connect("mysql", buildDSN(&p.config))
    // ... 配置池 + Ping + 替换
    oldDB := p.db.Swap(db)
    if oldDB != nil { _ = oldDB.Close() }
}
```

熔断后,`pingOnce` 主动调用 `reconnect`;成功后 plugin 透明恢复。

---

## 2. 查询构建(edit-list 算法)

### 2.1 设计动机

链式 API `.Where().Join().Order().Limit()` 不能直接拼字符串 — 各种顺序、覆盖、替换都会让 naive 拼接产生:
- `... WHERE a = ? AND b = ? WHERE c = ?` — 重复 WHERE
- `... LIMIT 10 LIMIT 20` — 重复 LIMIT
- `... WHERE id = ? GROUP BY x WHERE id = ?` — 覆盖错位置

**edit-list 算法**:在原 query 文本上预计算"插入/替换"区间,排序后一次性 emit。

### 2.2 数据结构

```go
const (
    editJoin = iota
    editWhereInsert
    editWhereAppend
    editGroupInsert
    editGroupAppend
    editHavingInsert
    editHavingAppend
    editOrderInsert
    editOrderAppend
    editLimitInsert
    editLimitReplace
    editOffsetInsert
    editOffsetReplace
)

type edit struct {
    start, end int    // 原 query 上的绝对位置
    op         int    // 子句类型
    // 数据字段(判别式联合,避免 closure 堆逃逸)
    joins   []joinClause
    wheres  []whereClause
    groups  []string
    havings []havingClause
    orders  []string
    limit   int
    offset  int
}
```

### 2.3 emitEdit

```go
func emitEdit(e *edit, b *strings.Builder, allArgs *[]interface{}) {
    switch e.op {
    case editWhereInsert:
        b.WriteString(" " + sqlWhere + " ")
        for i, w := range e.wheres { ... }
    case editWhereAppend:
        for _, w := range e.wheres {
            writeWhereJoin(b, w.op)
            b.WriteString(w.condition)
        }
    // ...
    }
}
```

单一函数 + switch dispatch,无 closure,所有调用栈可内联。

### 2.4 whereOp 位标志(R04)

```go
type whereOp uint8
const (
    whereOpNone whereOp = iota  // AND
    whereOpOr                  // OR
    whereOpNot                 // NOT
)

type whereClause struct {
    condition string
    args      []any
    op        whereOp
}
```

替代 `Or("a")` 早期做法 "OR " + condition — 每次 `emitEdit` 都要 `strings.HasPrefix` 扫描。
位标志:emit 时根据 `op` 选连接符,无字符串扫描。

### 2.5 edit-list 排序

```go
// 插入排序,n ≤ 7 时比 sort.Slice 更轻
for i := 1; i < len(edits); i++ {
    for j := i; j > 0 && edits[j].start < edits[j-1].start; j-- {
        edits[j], edits[j-1] = edits[j-1], edits[j]
    }
}
```

n ≤ 7 固定(7 类子句),插入排序无 closure 分配。

---

## 3. 对象池(Object Pools)

### 3.1 MySQLQueryResult Pool(R02)

```go
var mysqlQueryResultPool = sync.Pool{
    New: func() interface{} {
        return &MySQLQueryResult{
            joins:   make([]joinClause, 0, 4),
            wheres:  make([]whereClause, 0, 8),
            groups:  make([]string, 0, 2),
            havings: make([]havingClause, 0, 2),
            orders:  make([]string, 0, 4),
            args:    make([]interface{}, 0, 16),
            // R11-perf:scratch 缓冲也在这里预分配
            scratchEdits: make([]edit, 0, 7),
            scratchArgs:  make([]any, 0, 24),
        }
    },
}
```

**目的**:避免每次链式入口 `Query()` / `Table()` 都 new 一个 MySQLQueryResult。
**生命周期**:`acquireMySQLQueryResult` 取 → 链式填充 → 终端方法调 `releaseMySQLQueryResult` 还。

### 3.2 MySQLQueryResult.reset

```go
func (qr *MySQLQueryResult) reset() {
    qr.plugin = nil
    qr.ctx = nil
    qr.query = ""
    qr.args = qr.args[:0]
    qr.joins = qr.joins[:0]
    // ... 所有切片截断到 0,保留底层数组
    qr.cancel()  // R11:回收 WithTimeout 注入的 cancel
    if qr.cancel != nil {
        qr.cancel()
        qr.cancel = nil
    }
}
```

关键:`qr.args[:0]` 而非 `qr.args = nil` — 保留底层数组容量,下次 acquire 时直接 append,零分配。

### 3.3 R11-perf:scratchEdits / scratchArgs

```go
func (qr *MySQLQueryResult) buildQuery() (string, []interface{}) {
    // 复用对象池的 scratch 缓冲
    edits := qr.scratchEdits[:0]
    allArgs := qr.scratchArgs[:0]
    // ... 填充
}
```

之前每次 buildQuery 都 `make([]edit, 0, 7)` + `make([]any, 0, 24)`,2 次分配/call。
复用后,稳态 0 分配。

### 3.4 valsPool / argStrsPool

| Pool | 文件 | 目的 |
|---|---|---|
| `mysqlQueryResultPool` | pool.go | MySQLQueryResult 复用 |
| `valsPool` | scanner.go | `[]any` in buildXxxSQL 复用 |
| `argStrsPool` | logger.go | `[]string` in formatQuery 复用 |

---

## 4. 反射缓存(Reflection Cache)

### 4.1 fieldMeta 缓存(R02)

```go
var metaCache sync.Map  // reflect.Type → *fieldMeta

type fieldMeta struct {
    tableName  string
    columns    []string
    idIndex    int
    pkColumn   string
    fieldInfos []fieldInfo
    // R02:预构建 SQL 模板(同 type 跨调用复用)
    sqlOnce       sync.Once
    insertSQL     string
    updateAllSQL  string
    updateByIDSQL string
    deleteByIDSQL string
    selectByIDSQL string
}
```

**关键决策**:反射是 O(n²) 开销(model 字段多时尤其严重)。fieldMeta 把一次性反射结果缓存,跨实例共享。

### 4.2 db:"col,pk" 主键标记(R04)

```go
name, opt, _ := strings.Cut(tag, ",")
if opt == "pk" || opt == "primary" {
    meta.idIndex, meta.pkColumn = i, name
}
if name == "id" && meta.idIndex < 0 {
    // 向后兼容:无标记但列名是 "id" 也视为 PK
    meta.idIndex, meta.pkColumn = i, "id"
}
```

向后兼容旧约定 `db:"id"`,允许新约定 `db:"col,pk"`。

### 4.3 tableNameCache

```go
var tableNameCache sync.Map  // reflect.Type → string

func getTableNameFromDest(dest any) string {
    t := reflect.TypeOf(dest)
    if t.Kind() == reflect.Ptr { t = t.Elem() }
    if t.Kind() == reflect.Slice { t = t.Elem() }
    if v, ok := tableNameCache.Load(t); ok { return v.(string) }
    // ... 计算
    tableNameCache.LoadOrStore(t, name)
    return name
}
```

每次 `First(&user)` 调用都反射计算表名,缓存后 O(1)。

---

## 5. 错误处理

### 5.1 sentinel 错误

```go
var (
    ErrMySQLNotEnabled = errors.New("mysql is not enabled")
    ErrModelNotFound   = errors.New("model not found")
    ErrInvalidModel    = errors.New("invalid model")
    ErrDuplicateKey    = errors.New("duplicate key")
)
```

### 5.2 wrapMySQLError

```go
type wrappedMySQLError struct {
    table string
    op    string
    err   error
}

func (e *wrappedMySQLError) Error() string {
    return e.table + ": " + e.op + " failed: " + e.err.Error()
}
func (e *wrappedMySQLError) Unwrap() error { return e.err }
func (e *wrappedMySQLError) Is(target error) bool {
    return errors.Is(e.err, target)
}
```

`errors.Is(err, ErrModelNotFound)` 和 `errors.As(err, &mErr)` 都能工作。

### 5.3 错误链示例

```go
err := plugin.GetByID(ctx, &user, 999)
// errors.Is(err, ErrModelNotFound) → true
// errors.As(err, &mErr) → mErr.Op() = "select", Table() = "users"
```

---

## 6. 锁策略

| 资源 | 锁类型 | 原因 |
|---|---|---|
| `p.db` | `atomic.Pointer[sqlx.DB]` | 高频读,零锁 |
| `p.state` | `sync.RWMutex` | 极低频读(RLock)+ 偶尔写 |
| `p.slowBuffer.head/fullFlag` | `atomic.Int64/32` (R11-perf) | 高频写 |
| `p.slowBuffer.buf` | `sync.Mutex` (读路径仅复制切片) | 锁长度缩短 |
| `healthChecker.mu` | `sync.Mutex` | 短临界区(状态变化通知) |
| `PrepareCache` | `sync.Mutex` | 短临界区,带 LRU 链表维护 |
| `RetryBudget` | `sync.Mutex` | 失败计数 + 状态机 |

**原则**:热路径 atomic,冷路径 mutex,临界区尽可能短。

---

## 7. 启动 / 关闭流程

### 7.1 Start

```
1. buildDSN(cfg) → "user:pass@tcp(host)/db?..."
2. sqlx.Connect("mysql", dsn)  // 建首连 + 验证密码
3. db.SetMaxOpenConns / SetMaxIdleConns / SetConnMaxLifetime / SetConnMaxIdleTime
4. db.Ping() // 验证可连
5. p.db.Store(db)  // 原子发布句柄
6. if MinIdleConns > 0: go p.warmupPool(db, MinIdleConns)
7. 持锁创建 queryLogger
8. 释放锁
9. if p.slowBuffer != nil: queryLogger.AttachSlowBuffer(p.slowBuffer)
10. if p.healthChecker != nil: healthChecker.Start(p, ctx) // 启动 ping goroutine
11. 后台 goroutine 监听 ctx.Done() → close(p.stopCh)
12. logger.Info(...)
```

### 7.2 Stop

```
1. p.stopOnce.Do { close(p.done); close(p.stopCh) }
2. p.db.Swap(nil) // 原子取空
3. if db != nil: db.Close()
4. 持锁置 state = Stopped
5. if p.healthChecker != nil: healthChecker.Stop() // 取消 ping goroutine
6. logger.Info(...)
```

### 7.3 状态机

```
Ready ──Init()──▶ Ready
Ready ──Start()──▶ Running ──Stop()──▶ Stopped
Running ──Stop()──▶ Stopped
```

---

## 8. 与 wma 框架的集成

### 8.1 Plugin 接口实现

```go
type wma.Plugin interface {
    Type() wma.PluginType
    Name() string
    Init(app *wma.App) error
    Start(ctx context.Context) error
    Stop(ctx context.Context) error
}
```

`MySQLPlugin` 实现上述 5 个方法。`Type()` 恒返回 `wma.PluginTypeCustom`。

### 8.2 共享 plugin 句柄(典型用法)

```go
// 在 sanguo 仓库的 xengine/shared_plugins.go
func GetMySQLPlugin(app *wma.App) (*mysqlplugins.MySQLPlugin, error) {
    p, ok := app.Plugins().Get("mysql").(*mysqlplugins.MySQLPlugin)
    if !ok || p == nil {
        return nil, errors.New("mysql plugin not registered")
    }
    return p, nil
}
```

### 8.3 DAO 层典型集成

```go
type mysqlRoleDao struct {
    plugin *mysqlplugins.MySQLPlugin  // 由 xengine 启动时注入
}

func (d *mysqlRoleDao) FindByID(ctx context.Context, id int64) (*RoleModel, error) {
    var role RoleModel
    if err := d.plugin.GetByID(ctx, &role, id); err != nil {
        if errors.Is(err, mysqlplugins.ErrModelNotFound) {
            return nil, nil
        }
        return nil, fmt.Errorf("find role: %w", err)
    }
    return &role, nil
}
```

---

## 9. 可扩展性

### 9.1 添加新 ORM 方法

```go
// 在 orm.go 加:
func (p *MySQLPlugin) FindByField(ctx context.Context, dest any, field string, value any) error {
    if !isValidIdentifier(field) {
        return wrapMySQLError("", "find by field", ErrInvalidModel)
    }
    return p.Table(getTableNameFromDest(dest)).
        Where(fmt.Sprintf("%s = ?", field), value).
        First(dest)
}
```

复用 `isValidIdentifier` + `getTableNameFromDest` + `Table().Where().First()` 链式。

### 9.2 添加新链式子句

```go
// 在 query.go 加:
func (qr *MySQLQueryResult) HavingRaw(sql string, args ...any) *MySQLQueryResult {
    if qr.err != nil { return qr }
    qr.havings = append(qr.havings, havingClause{condition: sql, args: args})
    qr.dirty = true
    return qr
}
```

需要时也加 `editHavingInsert/Append` 触发路径(参考 `emitEdit`)。

### 9.3 添加新可观测性指标

```go
// 在 plugin.go MySQLPlugin struct 加:
myNewMetric atomic.Int64

// 在 orm.go logQ 内部:
p.myNewMetric.Add(1)

// 在 Stats() 加字段:
type MySQLStats struct {
    // ...
    MyNewMetric int64
}
```

---

## 10. 调试技巧

### 10.1 慢查询排查

```bash
# 启慢查询缓冲
buf := mysql.NewSlowQueryBuffer(100)
plugin.AttachSlowBuffer(buf)
plugin.SetAdminAuthToken("dev")
go http.ListenAndServe(":8080", plugin.AdminHandler())
# 浏览器: GET http://localhost:8080/debug/slow
#   Header: X-MySQL-Admin-Token: dev
#   ?redact=0 关闭脱敏
```

### 10.2 指标监控

```bash
# Prometheus 抓取
curl http://localhost:8080/metrics | head -50
# 关键指标:
#   mysql_query_duration_seconds{...,le="0.005"} → p95 延迟桶
#   mysql_query_slow_total → 慢查询计数
#   mysql_query_errors_total → 错误计数
#   mysql_connections{state="in_use"} → 池利用率
```

### 10.3 Schema 自省

```bash
# 列出全部表
curl http://localhost:8080/debug/table/users | jq
# 看具体表的列定义 + 索引
```

### 10.4 启用 SQL 调试日志

```go
plugin, _ := mysql.NewMySQLPlugin("mysql", &mysql.MySQLPluginConfig{
    // ...
    EnableQueryLog:  true,
    SlowThreshold:   50,  // ms
})
// 慢查询自动记录到 slowBuffer(若 AttachSlowBuffer)
```

---

## 11. 已知限制

| 限制 | 替代方案 |
|---|---|
| Go stdlib 无 `SetMinIdleConns` | `effectiveMaxIdleConns` 代理 |
| 链式更新子句覆盖 | 通过 `editList` 算法处理 |
| 可空列 `int64` 自动 NULL→0 | 需用 `NullInt64` (R10) |
| `Begin()` 跨 ctx 取消 | `RunInTransaction` 用 `context.Background()` Rollback |
| `Where` "前 N 字符"窗口 | 用更精确的 SQL 解析(留 R14+) |
| PII 脱敏过保守 | 同上 |

---

## 12. 进一步阅读

- [performance.md](performance.md) — R04-R13 性能优化与基准数据
- [observability.md](observability.md) — 指标 / 慢查询 / 健康检查 / Admin 端点
- [migration.md](migration.md) — DAO 迁移指南
- [api-reference.md](api-reference.md) — 完整公开 API
- [rounds/r04-opt/PM.md](rounds/r04-opt/PM.md) — R04 轮次总结
- [rounds/r13-perf.md](rounds/r13-perf.md) — R11-perf + R13 perf 总结
