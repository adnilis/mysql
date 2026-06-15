# API 参考(R04-R13 累计)

> wma/plugins/mysql 公开 API 一站式参考
>
> 截至 R13。每一节按"API 签名 / 何时用 / 简单示例 / 注意事项"四段呈现。

## 目录

- [0. 生命周期](#0-生命周期lifecycle)
- [1. 链式查询(`MySQLQueryResult`)](#1-链式查询mysqlqueryresult)
- [2. ORM 高层(`MySQLPlugin`)](#2-orm-高层mysqlplugin)
  - [2.1 基础 CRUD](#21-基础-crud)
  - [2.2 原始 SQL](#22-原始-sql)
  - [2.3 批量](#23-批量)
  - [2.4 事务](#24-事务)
  - [2.5 可靠性](#25-可靠性)
- [3. 模型与字段(`IModel` / `fieldScanner`)](#3-模型与字段imodel--fieldscanner)
- [4. 可空列类型(R10)](#4-可空列类型r10)
- [5. Schema 自省(R07 / R08)](#5-schema-自省r07--r08)
- [6. 性能与预编译(R08)](#6-性能与预编译r08)
- [7. 可观测性(R06 / R09 / R10 / R11 / R12 / R13)](#7-可观测性)
- [8. 错误处理](#8-错误处理)
- [9. 池与连接](#9-池与连接)
- [10. 约定与最佳实践](#10-约定与最佳实践)
- [11. 不在 API 内的内部包(仅参考)](#11-不在-api-内的内部包仅参考)

---

## 0. 生命周期(Lifecycle)

| API | 说明 |
|---|---|
| `NewMySQLPlugin(name, cfg) *MySQLPlugin` | 构造函数,cfg 传 nil 用默认 |
| `(*MySQLPlugin).Init(app) error` | wma 框架 Init 钩子 |
| `(*MySQLPlugin).Start(ctx) error` | 建连 + 预热 + 挂可观测性 |
| `(*MySQLPlugin).Stop(ctx) error` | 关连 + 停可观测性 + 状态置 Stopped |
| `(*MySQLPlugin).DB() *sqlx.DB` | 原子读 db 句柄(无锁) |
| `(*MySQLPlugin).Ping(ctx) error` | 健康检查 |
| `(*MySQLPlugin).Stats() MySQLStats` | 指标(R06 增强) |

---

## 1. 链式查询(`MySQLQueryResult`)

| 链式方法 | 说明 |
|---|---|
| `.Select(fields...)` | 替换 SELECT 列 |
| `.Join/InnerJoin/LeftJoin/RightJoin(type, table, on, args...)` | JOIN |
| `.Where/Or/Not(condition, args...)` | WHERE 条件(支持位标志 op) |
| `.OrWhere(condition, args...)` | OrWhere = Or(语义化别名) |
| `.Group(fields...)` | GROUP BY |
| `.Having(condition, args...)` | HAVING |
| `.Order(field)` / `.Asc/Desc(fields...)` | ORDER BY |
| `.Limit(n)` / `.Offset(n)` / `.Page(page, size)` | LIMIT/OFFSET/分页 |
| `.WithTimeout(d)` | 链式超时 |
| `.Distinct(fields...)` | DISTINCT |

| 终端方法 | 说明 |
|---|---|
| `.First(dest) error` | 第一条;dest 可为结构体或标量(*int64/*string/...) |
| `.Take(dest) error` | 第一条(不限 LIMIT) |
| `.Find(dest) error` | 全部;dest 可为 `*[]Model` 或 `*[]map[string]any`(MapScan) |
| `.Count(*int64) error` | 走子查询 COUNT |
| `.Update(col, val) error` | 链式 UPDATE |
| `.Delete() error` | 链式 DELETE |
| `.Exec() (sql.Result, error)` | 链式 Exec |
| `.Pluck(field, dest) error` | 单列到切片 |

---

## 2. ORM 高层(`MySQLPlugin`)

### 2.1 基础 CRUD

| API | 说明 |
|---|---|
| `Insert(ctx, model) (int64, error)` | 插入并返回自增 ID |
| `Update(ctx, model, where, args...) (int64, error)` | 按 WHERE 更新 |
| `Delete(ctx, model, where, args...) (int64, error)` | 按 WHERE 删除 |
| `GetByID(ctx, model, id) error` | 按主键查询(NotFound → ErrModelNotFound) |
| `UpdateByID(ctx, model, id) (int64, error)` | 按主键更新 |
| `DeleteByID(ctx, model, id) (int64, error)` | 按主键删除 |
| `Save(ctx, model) error` | GORM 风格:零值 ID 插入,非零更新 |
| `SaveOnConflict(ctx, model, conflictCols...) (int64, error)` | R07 IModel 版 upsert |

### 2.2 原始 SQL

| API | 说明 |
|---|---|
| `Query(ctx, sql, args...) *MySQLQueryResult` | 链式入口 |
| `Table(name) *MySQLQueryResult` | 链式入口(自动 `SELECT * FROM <name>`) |
| `Model(model) *MySQLQueryResult` | 链式入口(从 IModel 推断表名) |
| `Select(ctx, dest, sql, args...) error` | 原生 SELECT 到切片 |
| `Get(ctx, dest, sql, args...) error` | 原生 SELECT 单条 |
| `Exec(ctx, sql, args...) (int64, error)` | 原生 Exec 返回受影响行数 |
| `ExecReturningID(ctx, sql, args...) (int64, error)` | INSERT 后返回 LastInsertId |
| `Count(ctx, table, where, args...) (int64, error)` | 原生 COUNT |
| `Exists(ctx, table, where, args...) (bool, error)` | 存在性检查 |
| `First(ctx, dest, id) error` | GORM 风格 First(支持标量 dest) |
| `Find(ctx, dest, sql, args...) error` | GORM 风格 Find |

### 2.3 批量

| API | 说明 |
|---|---|
| `BatchInsert(ctx, []IModel, batchSize) ([]int64, error)` | 多 VALUES 批量 INSERT |
| `BatchExec(ctx, table, cols, rows, chunkSize) (int64, error)` | R04 通用多行 INSERT |
| `BatchInsertOnConflict(ctx, []IModel, batchSize, conflictCols...) ([]int64, error)` | R09 批量 upsert |

### 2.4 事务

| API | 说明 |
|---|---|
| `Begin() (*MySQLTransaction, error)` | 显式事务 |
| `RunInTransaction(ctx, fn func(tx) error) error` | R04 自动事务 |

### 2.5 可靠性

| API | 说明 |
|---|---|
| `WithRetry(ctx, policy, fn) error` | R09 死锁重试 |
| `WithRetryTx(ctx, policy, fn func(tx) error) error` | R09 事务内死锁重试 |
| `DefaultRetryPolicy() RetryPolicy` | 默认 5 次 50ms→800ms 指数退避 |

---

## 3. 模型与字段(`IModel` / `fieldScanner`)

| 约定 | 说明 |
|---|---|
| `IModel` 接口 | `TableName() string` |
| 主键 tag | `db:"id"`(默认) / `db:"col,pk"` / `db:"col,primary"`(R04) |
| 反射缓存 | `fieldMeta` 按 type 缓存(R02 引入) |
| 预构建 SQL | `insertSQL` / `updateAllSQL` / `updateByIDSQL` / `deleteByIDSQL` / `selectByIDSQL` |

---

## 4. 可空列类型(R10)

```go
type User struct {
    ID    NullInt64 `db:"id"`
    Score NullInt64 `db:"score"`
}

type NullInt64 struct {
    Valid bool
    Int64 int64
}
type NullString struct {
    Valid bool
    Str   string
}
```

NULL → 零值;非 NULL → 实际值;实现 `sql.Scanner` + `driver.Valuer` 双接口。
工厂:`NewNullInt64(v)` / `NewNullInt64FromPtr(p)` / `NewNullString(...)`。

---

## 5. Schema 自省(R07 / R08)

| API | 返回 |
|---|---|
| `ListTables(ctx) ([]string, error)` | 当前 DB 全部基表 |
| `DescribeTable(ctx, table) (*TableInfo, error)` | 列定义 + 索引 |
| `ListIndexes(ctx) ([]IndexDef, error)` | 全库索引(不含主键) |
| `DescribeIndex(ctx, table) ([]IndexDef, error)` | 单表索引(含主键) |

`TableInfo` / `ColumnDef` / `IndexDef` 结构见 schema.go。

---

## 6. 性能与预编译(R08)

```go
cache := mysql.NewPrepareCache(128) // LRU 容量
stmt, err := cache.Prepare(ctx, db, "SELECT ...")
defer cache.CloseAll()

hits, misses, size := cache.Stats()
```

API:`NewPrepareCache(cap)` / `Prepare(ctx, db, query)` / `CloseAll()` / `Stats()`。

---

## 7. 可观测性(R06 / R09 / R10 / R11)

### 7.1 内存指标(R06)

`Stats()` 暴露的 `MySQLStats` 字段:
- `QueryTotal` / `QueryErrors` / `QuerySlow` / `RowsRead` / `RowsAffected`(5 个 atomic.Int64)
- `OpenConnections` / `InUse` / `Idle` / `WaitCount` 等 db.Stats() 标准指标

### 7.2 慢查询缓冲(R09)

```go
buf := mysql.NewSlowQueryBuffer(100)
plugin.AttachSlowBuffer(buf) // 一次性挂接

snapshot := plugin.SlowQueries()         // []SlowQueryRecord
json, _ := plugin.SlowQueriesJSON()      // []byte for /debug/slow
```

### 7.3 健康检查(R09 / R11)

```go
checker := mysql.NewHealthChecker(30*time.Second, 3)
checker.SetHook(mysql.SlackAlertHandler(os.Getenv("SLACK_WEBHOOK_URL"), "prod-mysql-1"))
plugin.AttachHealthChecker(checker) // Start 自动启动;Stop 自动停止
```

### 7.4 Admin HTTP 端点(R11)

```go
http.ListenAndServe(":8080", plugin.AdminHandler())
// GET /debug/stats        → MySQLStatsJSON
// GET /debug/slow         → []SlowQueryJSON
// GET /debug/table/{name} → TableInfoJSON
```

### 7.5 慢查询钩子(可定制)

```go
ql := plugin.QueryLogger()  // (内部,可经 SetSlowQueryHook 暴露)
ql.SetSlowQueryHook(func(ctx, query, duration, rows, args...) {
    metrics.RecordSlowQuery(...)
})
```

### 7.6 Slack 告警助手(R11)

```go
hook := mysql.SlackAlertHandler("https://hooks.slack.com/...", "prod-mysql-1")
checker.SetHook(hook)
```

---

## 8. 错误处理

| Sentinel | 触发 |
|---|---|
| `ErrMySQLNotEnabled` | Start 前调用 ORM/Query |
| `ErrModelNotFound` | 无匹配行(Get/First/Take/GetByID) |
| `ErrInvalidModel` | Model(nil) / Table("evil;name") / Save 非指针 |
| `ErrDuplicateKey` | 底层 1062 主键冲突 |

`MySQLError` 是 `wrappedMySQLError` 的导出别名,支持 `errors.As`:
```go
var mErr *mysql.MySQLError
if errors.As(err, &mErr) {
    log.Printf("table=%s op=%s", mErr.Table(), mErr.Op())
}
```

---

## 9. 池与连接

| API | 说明 |
|---|---|
| `NewPrepareCache(cap)` | LRU stmt 缓存 |
| `Start` 内置预热 | 主动填充 `MinIdleConns` |
| `AttachHealthChecker` | 后台 ping + 自动重连(R11 自动挂) |

---

## 10. 约定与最佳实践

1. **DAO 优先使用链式 API**:`plugin.Table("users").Where(...).Find(&users)` 优于 `plugin.Exec` 后手 Scan
2. **多步写用 `RunInTransaction`**:避免 loop-Exec 留脏数据
3. **批量写用 `BatchExec` / `BatchInsertOnConflict`**:减少网络往返
4. **大结果集分页用 `Page`**:清晰且自动夹紧
5. **热查询用 `PrepareCache`**:消除 driver 反复 Prepare
6. **慢查询挂 `AttachSlowBuffer`**:postmortem 数据源
7. **健康检查挂 `AttachHealthChecker`**:对接告警

---

## 11. 不在 API 内的内部包(仅参考)

- `mysqlQueryResultPool` `sync.Pool` for `MySQLQueryResult`
- `tableNameCache` `sync.Map` for table name lookups
- `valsPool` `sync.Pool` for `[]any` in write paths
- `argStrsPool` `sync.Pool` for `[]string` in `formatQuery`
- `metaCache` `sync.Map` for `fieldMeta` reflection cache

直接修改这些可能影响所有调用方。
