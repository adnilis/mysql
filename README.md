# MySQL 插件

WMA 框架的 MySQL 数据库插件，提供连接池管理、ORM 操作和链式查询功能。

## 安装

```bash
go get github.com/adnilis/wma/plugins/mysql
```

## 快速开始

```go
import "github.com/adnilis/wma/plugins/mysql"

// 创建插件
cfg := &mysql.MySQLPluginConfig{
    Addr:     "localhost:3306",
    User:     "root",
    Password: "password",
    DBName:   "testdb",
    PoolSize: 10,
}
plugin := mysql.NewMySQLPlugin("mysql", cfg)

// 注册到应用
app.RegisterPlugin(plugin)

// 启动应用后即可使用
```

## 配置说明

```go
type MySQLPluginConfig struct {
    Addr         string        // MySQL 地址，默认 "localhost:3306"
    User         string        // 用户名，必填
    Password     string        // 密码
    DBName       string        // 数据库名，必填
    PoolSize     int           // 连接池大小，默认 10
    MinIdleConns int           // 最小空闲连接数，默认 3
    MaxIdleConns int           // 最大空闲连接数，默认 5
    MaxLifetime  time.Duration // 连接最大生命周期，默认 0（无限制）
    MaxIdleTime  time.Duration // 空闲连接存活时间，默认 5 分钟
    ConnTimeout  time.Duration // 连接超时，默认 5 秒
    ReadTimeout  time.Duration // 读取超时，默认 3 秒
    WriteTimeout time.Duration // 写入超时，默认 3 秒
    ParseTime    bool          // 是否解析时间，默认 true
    Loc          string        // 时区，默认 "Local"
}
```

## 模型定义

实现 `IModel` 接口来定义数据库表模型：

```go
type User struct {
    ID        int64     `db:"id"`
    Name      string    `db:"name"`
    Age       int       `db:"age"`
    Email     string    `db:"email"`
    CreatedAt time.Time `db:"created_at"`
}

func (u *User) TableName() string {
    return "users"
}
```

## ORM 基础操作

### 插入记录

```go
// 插入单条记录，返回自增 ID
id, err := mysql.Insert(ctx, &User{
    Name:  "张三",
    Age:   25,
    Email: "zhangsan@example.com",
})

// GORM 风格插入
err := mysql.Create(ctx, &User{Name: "李四", Age: 30})
```

### 查询记录

```go
// 根据 ID 查询
err := mysql.GetByID(ctx, &user, 1)

// GORM 风格 - 获取第一条
err := mysql.First(ctx, &user, 1)

// 原生查询
err := mysql.Get(ctx, &user, "SELECT * FROM users WHERE id = ?", 1)

// 条件查询
err := mysql.Select(ctx, &users, "SELECT * FROM users WHERE age > ?", 18)
```

### 更新记录

```go
// 根据 ID 更新
_, err := mysql.UpdateByID(ctx, &User{ID: 1, Name: "王五", Age: 28}, 1)

// 条件更新
_, err := mysql.Update(ctx, &User{Name: "王五"}, "id = ?", 1)

// GORM 风格保存（自动判断插入或更新）
err := mysql.Save(ctx, &User{ID: 1, Name: "更新后的名字"})
```

### 删除记录

```go
// 根据 ID 删除
_, err := mysql.DeleteByID(ctx, &User{}, 1)

// 条件删除
_, err := mysql.Delete(ctx, &User{}, "age < ?", 18)
```

## 链式查询

支持 GORM 风格的链式查询 API：

```go
// 基本查询
mysql.Table("users").
    Where("age > ?", 18).
    Where("status = ?", 1).       // 多个 Where 条件
    Order("created_at DESC").
    Limit(10).
    Offset(0).
    Find(&users)

// 指定查询字段
mysql.Table("users").
    Select("id", "name", "age").
    Where("age > ?", 18).
    Find(&users)

// JOIN 查询
mysql.Table("orders").
    Select("orders.id", "users.name").
    LeftJoin("users", "orders.user_id = users.id").
    Where("orders.status = ?", 1).
    Find(&results)

// 去重查询
mysql.Table("users").
    Distinct("name", "age").
    Where("status = ?", 1).
    Find(&results)

// 统计数量
var count int64
mysql.Table("users").
    Where("age > ?", 18).
    Count(&count)

// 单列查询
var names []string
mysql.Table("users").
    Where("age > ?", 18).
    Pluck("name", &names)

// 更新操作
mysql.Table("users").
    Where("id = ?", 1).
    Update("name", "新名字")

// 删除操作
mysql.Table("users").
    Where("id = ?", 1).
    Delete()

// 执行原始 SQL
result, err := mysql.Query(ctx, "UPDATE users SET name = ? WHERE id = ?", "新名字", 1).
    Exec()
```

### 链式方法说明

| 方法 | 说明 |
|------|------|
| `Query(ctx, sql, args...)` | 创建链式查询 |
| `Table(name)` | 指定表名 |
| `Model(model)` | 根据模型推断表名 |
| `Select(fields...)` | 指定查询字段 |
| `Where(cond, args...)` | 添加 WHERE 条件 |
| `Or(cond, args...)` | 添加 OR 条件 |
| `Not(cond, args...)` | 添加 NOT 条件 |
| `Join(type, table, on, args...)` | 添加 JOIN |
| `InnerJoin(table, on, args...)` | 添加 INNER JOIN |
| `LeftJoin(table, on, args...)` | 添加 LEFT JOIN |
| `RightJoin(table, on, args...)` | 添加 RIGHT JOIN |
| `Group(fields...)` | 添加 GROUP BY |
| `Having(cond, args...)` | 添加 HAVING 条件 |
| `Order(field)` | 添加 ORDER BY |
| `Asc(fields...)` | 添加 ASC 排序 |
| `Desc(fields...)` | 添加 DESC 排序 |
| `Limit(n)` | 限制返回行数 |
| `Offset(n)` | 设置偏移量 |
| `Distinct(fields...)` | 去重查询 |
| `Take(dest)` | 获取任意一条记录 |
| `First(dest)` | 获取第一条记录 |
| `Find(dest)` | 获取所有记录 |
| `Pluck(field, dest)` | 查询单列到切片 |
| `Count(dest)` | 统计数量 |
| `Update(col, value)` | 更新记录 |
| `Delete()` | 删除记录 |
| `Exec()` | 执行原始 SQL，返回 sql.Result |

## 批量操作

```go
// 批量插入
users := []IModel{
    &User{Name: "用户1", Age: 20},
    &User{Name: "用户2", Age: 21},
    &User{Name: "用户3", Age: 22},
}
ids, err := mysql.BatchInsert(ctx, users, 100) // 100条/批

// 检查记录是否存在
exists, err := mysql.Exists(ctx, "users", "name = ?", "张三")

// 统计记录数
count, err := mysql.Count(ctx, "users", "age > ?", 18)
```

## 事务支持

```go
// 开启事务
tx, err := mysql.Begin()
if err != nil {
    return err
}

// 在事务中执行操作
_, err = tx.Exec(ctx, "UPDATE users SET name = ? WHERE id = ?", "新名字", 1)
if err != nil {
    tx.Rollback()
    return err
}

// 提交事务
err = tx.Commit()
```

## 统计信息

```go
stats := mysql.Stats()
fmt.Printf("连接状态: %s\n", stats.State)
fmt.Printf("打开连接数: %d\n", stats.OpenConnections)
fmt.Printf("使用中连接: %d\n", stats.InUse)
fmt.Printf("空闲连接: %d\n", stats.Idle)
```

## 插件接口

实现 `wma.Plugin` 接口：

```go
// Type 返回插件类型
func (p *MySQLPlugin) Type() wma.PluginType {
    return wma.PluginTypeCustom
}

// Name 返回插件名称
func (p *MySQLPlugin) Name() string {
    return p.name
}

// Init 初始化插件
func (p *MySQLPlugin) Init(app *wma.App) error

// Start 启动插件
func (p *MySQLPlugin) Start(ctx context.Context) error

// Stop 停止插件
func (p *MySQLPlugin) Stop(ctx context.Context) error
```

## 错误处理

插件定义了以下错误：

```go
ErrMySQLNotEnabled // MySQL 未启用或未初始化
ErrModelNotFound   // 模型记录不存在
ErrInvalidModel    // 无效的模型
ErrDuplicateKey    // 重复键错误
```

## 安全说明

### SQL 注入防护

插件对表名进行验证，只允许合法的 SQL 标识符（字母或下划线开头，后跟字母、数字或下划线）：

```go
// 安全的表名
plugin.Table("users")     // ✓ 通过
plugin.Table("user_orders") // ✓ 通过

// 不安全的表名会被拒绝
plugin.Table("users; DROP TABLE users;--") // ✗ 返回错误
plugin.Table("users' OR '1'='1")           // ✗ 返回错误
```

### 参数化查询

所有用户输入都使用参数化查询，避免 SQL 注入：

```go
// 参数会被正确转义
plugin.Table("users").Where("name = ?", "'; DROP TABLE users;--")
// 实际执行: SELECT * FROM users WHERE name = '...'
```

## 测试

插件包含完整的单元测试和基准测试：

```bash
# 运行所有测试
go test -v ./...

# 运行基准测试
go test -bench=. -benchmem ./...

# 运行特定测试
go test -v -run "TestQuery" ./...
```

测试使用 [go-sqlmock](https://github.com/DATA-DOG/go-sqlmock) 模拟数据库连接，无需真实数据库。

## 性能特性

- **连接池管理**：支持配置最大连接数、最小空闲连接数、连接生命周期等
- **对象池复用**：QueryResult 对象使用 `sync.Pool` 复用，减少内存分配
- **元数据缓存**：使用 `sync.Map` 缓存字段元数据，避免重复反射
- **查询缓存**：相同查询结构只构建一次，结果被缓存
- **字符串预分配**：使用 `strings.Builder` 预分配容量，减少动态分配

错误可通过 `errors.Is()` 进行检查：

```go
err := mysql.GetByID(ctx, &user, 1)
if errors.Is(err, mysql.ErrModelNotFound) {
    // 记录不存在
}
```

## 性能优化

1. **对象池** - `MySQLQueryResult` 使用 `sync.Pool` 复用，减少 GC 压力
2. **查询缓存** - 链式调用时缓存已构建的 SQL，避免重复构建
3. **预分配容量** - 字符串构建使用 `strings.Builder.Grow()` 减少内存分配
4. **连接池** - 内置连接池管理，支持配置大小、空闲超时等
5. **批量插入** - 支持分批插入，优化大数据量场景

## 完整示例

```go
package main

import (
    "context"
    "fmt"

    "github.com/adnilis/wma"
    "github.com/adnilis/wma/plugins/mysql"
)

type User struct {
    ID   int64  `db:"id"`
    Name string `db:"name"`
    Age  int    `db:"age"`
}

func (u *User) TableName() string {
    return "users"
}

func main() {
    // 创建应用
    app, _ := wma.NewApp(&wma.AppConfig{Name: "myapp"})

    // 创建 MySQL 插件
    cfg := &mysql.MySQLPluginConfig{
        Addr:     "localhost:3306",
        User:     "root",
        Password: "password",
        DBName:   "testdb",
    }
    mysqlPlugin := mysql.NewMySQLPlugin("mysql", cfg)
    app.RegisterPlugin(mysqlPlugin)

    // 启动应用
    app.Run()

    ctx := context.Background()

    // 插入
    id, _ := mysqlPlugin.Insert(ctx, &User{Name: "张三", Age: 25})
    fmt.Println("插入ID:", id)

    // 查询
    var user User
    mysqlPlugin.GetByID(ctx, &user, id)
    fmt.Printf("查询结果: %+v\n", user)

    // 链式查询
    var users []User
    mysqlPlugin.Table("users").
        Select("id", "name", "age").
        Where("age > ?", 20).
        Order("id DESC").
        Limit(10).
        Find(&users)
    fmt.Printf("用户列表: %+v\n", users)

    // 去重查询
    var names []string
    mysqlPlugin.Table("users").
        Distinct("name").
        Pluck("name", &names)

    // 统计
    var count int64
    mysqlPlugin.Table("users").
        Where("age > ?", 20).
        Count(&count)

    // 更新
    mysqlPlugin.Table("users").
        Where("id = ?", id).
        Update("name", "新名字")

    // 删除
    mysqlPlugin.Table("users").
        Where("id = ?", id).
        Delete()

    // 关闭应用
    app.Stop()
}
```

## R04 新增 API(R04 起)

### 主键标记约定

R04 起,`db` tag 支持可选主键标记,显式声明 PK 列:

```go
// 旧约定:db:"id" 自动识别为主键(向后兼容)
type OldModel struct {
    ID int64 `db:"id"`
    Name string `db:"name"`
}

// R04 新增:db:"<col>,pk" 显式声明任意列为主键
type UserModel struct {
    UserID int64  `db:"user_id,pk"`     // user_id 是主键
    Name   string `db:"name"`
}

// 也接受 db:"<col>,primary"
type OrderModel struct {
    OrderNo string `db:"order_no,primary"`
}
```

向后兼容:所有现有 `db:"id"` 标记继续工作。

### 标量 `First`

链式 `First` 现在接受标量指针,无需先定义结构体:

```go
// 旧:必须用 *User + errors.Is 判 not found
var user User
err := plugin.Table("users").Where("name = ?", name).First(&user)

// R04:直接拿 *int64 / *string / *bool
var uid int64
err := plugin.Table("users").Where("name = ?", name).First(&uid)
if errors.Is(err, ErrModelNotFound) {
    return nil, nil
}

var userName string
err = plugin.Table("users").Where("id = ?", 1).First(&userName)
```

支持的标量类型:`*int8`/`*int32`/`*int`/`*int64`/`*string`/`*float32`/`*float64`/`*bool`。

### `WithTimeout` 链式超时

替代 DAO 层 `xxxDBTimeout + context.WithTimeout` 样板:

```go
// 旧:每个 DAO 都要定义 const xxxDBTimeout = 3*time.Second
ctx, cancel := context.WithTimeout(ctx, xxxDBTimeout)
defer cancel()
err := plugin.Exec(ctx, sql, args...)

// R04:在链式上挂超时
err := plugin.Table("users").
    Where("age > ?", 18).
    WithTimeout(3*time.Second).
    Find(&users)
// 多次链式 WithTimeout 会回收前一次的 cancel,不会泄漏
```

### `RunInTransaction` 事务封装

替代 6+ 处 `SaveHeroes`/`SaveBuilds`/... 的 loop-Exec 样板,提供自动 Begin/Commit/Rollback:

```go
// 旧:循环 Exec 无事务,失败会留下脏数据
plugin.Exec(ctx, "DELETE FROM heros WHERE rid = ?", uid)
for _, h := range heros {
    plugin.Exec(ctx, "INSERT INTO heros ...", h.Field1, h.Field2, ...)
}

// R04:RunInTransaction 自动管理事务
err := plugin.RunInTransaction(ctx, func(tx *MySQLTransaction) error {
    if _, err := tx.Exec(ctx, "DELETE FROM heros WHERE rid = ?", uid); err != nil {
        return err
    }
    for _, h := range heros {
        if _, err := tx.Exec(ctx, "INSERT INTO heros ...", h.Field1, h.Field2, ...); err != nil {
            return err
        }
    }
    return nil
})
// fn 返回 nil → 自动 Commit
// fn 返回 error → 自动 Rollback,error 透传
// fn panic → 自动 Rollback 后重新 panic
```

### `BatchExec` 通用多行 INSERT

替代 `mail_mysql_dao.go` 等场景下手写多 VALUES 样板:

```go
// 旧:手拼 SQL,易写错
var sb strings.Builder
sb.WriteString("INSERT INTO mail_rec (mid, tuid) VALUES ")
for i, r := range recs {
    if i > 0 { sb.WriteString(", ") }
    sb.WriteString("(?, ?)")
}
plugin.DB().ExecContext(ctx, sb.String(), args...)

// R04:通用批量
rows := [][]any{}
for _, r := range recs {
    rows = append(rows, []any{r.Mid, r.Tuid})
}
affected, err := plugin.BatchExec(ctx, "mail_rec",
    []string{"mid", "tuid"}, rows, 200)
// chunkSize=200 → 自动分片
```

## 性能说明(R04)

R04 通过以下手段减少热路径分配:

- **对象池扩展**:`MySQLQueryResult` 的 `edits` / `allArgs` 缓冲下沉到结构体,由 `sync.Pool` 复用,`buildQuery` 入口不再 `make`。
- **类型级反射缓存**:`getTableNameFromDest` 引入 `sync.Map` 缓存,`First` 调用 O(1)。
- **零分配关键字检测**:`containsKeywordFold` / `indexKeywordFold` 取代 `strings.ToUpper` 整段拷贝,链式 `Select`/`Update`/`Distinct`/`Pluck`/`First` 节省每调用 1 次大字符串 alloc。
- **`MySQLQueryResult.err` 字段保留**:`Table`/`Model` 入口用其做"sticky error"传播,非死字段。

`BenchmarkBuildQueryPooled_AcquireRelease` 实测 145ns/op,3 allocs/op(对比之前 buildQuery 内部 `make([]edit, 0, 7)` + `make([]any, 0, N)` 节省约 6 allocs/call)。

## R05 / R06 / R07 新增 API

### R05:MapScan `Find`

```go
// 直接拿 map 列表(替代 plugin.DB().QueryxContext 逃逸)
var logs []map[string]any
plugin.Table("logs").Where("level = ?", "info").Find(&logs)
for _, l := range logs {
    fmt.Println(l["msg"])
}
```

### R05:慢查询回调钩子

```go
plugin.SetSlowQueryHook(func(ctx context.Context, query string, duration time.Duration, rows int64, args ...any) {
    metrics.RecordSlowQuery(ctx, query, duration, rows) // 接 Prometheus / OTel
})
```

### R06:`Page(page, pageSize)`

```go
// 替换 .Limit(20).Offset(20*(page-1)) 样板
err := plugin.Table("orders").
    Where("status = ?", "paid").
    Page(page, 20).
    Find(&results)
```

### R06:`Upsert` 通用多行 INSERT ... ON DUPLICATE KEY UPDATE

```go
rows := [][]any{
    {"alice", 100},
    {"bob",   200},
}
affected, err := plugin.Upsert(ctx, "rankings",
    []string{"name", "score"}, rows, nil, 200)
// updateCols=nil 时默认除 "id"/"ID" 外都更新
```

### R06:内存级指标通过 `Stats()` 暴露

```go
stats := plugin.Stats()
fmt.Printf("QPS=%.1f Slow=%d Errors=%d\n",
    float64(stats.QueryTotal)/elapsed.Seconds(),
    stats.QuerySlow, stats.QueryErrors)
```

5 个 atomic 计数器:`QueryTotal` / `QueryErrors` / `QuerySlow` / `RowsRead` / `RowsAffected`。

### R07:`SaveOnConflict` IModel 版 upsert

```go
// 替代 DAO 中"先 SELECT 检查再 INSERT/UPDATE"两段式样板
affected, err := plugin.SaveOnConflict(ctx, &user, "phone")
// conflictCols="phone":该列有 UNIQUE 索引,触发 ON DUPLICATE KEY UPDATE
// 冲突时更新除 phone 外的所有列
```

### R07:连接池预热

`Start` 后台 goroutine 主动 `Ping` 填充 `MinIdleConns` 个空闲连接,消除冷启动首波 P99 飙升。
失败不致命,池子按需懒分配。

### R07:Schema 自省 `ListTables` / `DescribeTable`

```go
tables, _ := plugin.ListTables(ctx)
for _, t := range tables {
    info, _ := plugin.DescribeTable(ctx, t)
    fmt.Println(info)
}
```

`DescribeTable` 返回 `TableInfo{Columns []ColumnDef, Indexes []IndexDef}`,可对接代码生成器、迁移工具、文档生成。

## DAO 迁移示例(R04-R07 综合)

将 sanguo 现有 DAO 模式改为 R04-R07 新 API 的真实重构示例。

### 模式 1:多步写改为事务

```go
// 旧:loop-Exec,失败留脏数据(无事务)
plugin.Exec(ctx, "DELETE FROM heros WHERE rid = ?", uid)
for _, h := range heroes {
    plugin.Exec(ctx, "INSERT INTO heros ...", h.Field1, h.Field2, ...)
}

// 新:RunInTransaction(R04) — 自动 Begin/Commit/Rollback/Panic 恢复
err := plugin.RunInTransaction(ctx, func(tx *MySQLTransaction) error {
    if _, err := tx.Exec(ctx, "DELETE FROM heros WHERE rid = ?", uid); err != nil {
        return err
    }
    for _, h := range heroes {
        if _, err := tx.Exec(ctx, "INSERT INTO heros ...", h.Field1, h.Field2, ...); err != nil {
            return err
        }
    }
    return nil
})
```

### 模式 2:`xxxDBTimeout` 改链式 `WithTimeout`

```go
// 旧:每个 DAO 顶部 const xxxDBTimeout = 3*time.Second
ctx, cancel := context.WithTimeout(ctx, xxxDBTimeout)
defer cancel()
err := plugin.Exec(ctx, "UPDATE ...", ...)

// 新:WithTimeout(R04) — 一次学习,全 DAO 通用
err := plugin.Table("orders").Where(...).WithTimeout(3*time.Second).Find(&orders)
```

### 模式 3:手拼多 VALUES INSERT 改 `BatchExec`

```go
// 旧:mail_mysql_dao.go::InsertMailRecBatch 手拼
var sb strings.Builder
sb.WriteString("INSERT INTO mail_rec (mid, tuid) VALUES ")
args := []any{}
for i, r := range recs {
    if i > 0 { sb.WriteString(", ") }
    sb.WriteString("(?, ?)")
    args = append(args, r.Mid, r.Tuid)
}
plugin.DB().ExecContext(ctx, sb.String(), args...)

// 新:BatchExec(R04) — 通用批量,自动分片校验
rows := make([][]any, len(recs))
for i, r := range recs { rows[i] = []any{r.Mid, r.Tuid} }
affected, err := plugin.BatchExec(ctx, "mail_rec",
    []string{"mid", "tuid"}, rows, 200)
```

### 模式 4:`DB().QueryxContext` 逃逸改 MapScan `Find`

```go
// 旧:role/loading DAO 用 plugin.DB().QueryxContext 拿 map
rows, _ := plugin.DB().QueryxContext(ctx, sql, args...)
defer rows.Close()
for rows.Next() {
    item := make(map[string]any)
    rows.MapScan(item)
    // 处理 item["xxx"]...
}

// 新:Find(&maps) (R05) — 链式 + 自动 MapScan
var items []map[string]any
plugin.Table("xxx").Where(...).Find(&items)
for _, item := range items { /* 处理 item["xxx"]... */ }
```

### 模式 5:标量查询免定义空结构体

```go
// 旧:为拿一个 int64 uid 定义空 User 结构
var user User
err := plugin.Table("users").Where("name = ?", name).First(&user)
uid := user.ID

// 新:First(&uid) (R04) — 标量派发
var uid int64
err := plugin.Table("users").Where("name = ?", name).First(&uid)
```

### 模式 6:分页

```go
// 旧:.Limit(20).Offset(20*page) 算术
err := plugin.Table("orders").Where(...).Limit(20).Offset(20*page).Find(&orders)

// 新:Page(page, 20) (R06) — 自动夹紧 page<1
err := plugin.Table("orders").Where(...).Page(page, 20).Find(&orders)
```

### 模式 7:"有则更新无则插入"

```go
// 旧:先 SELECT 检查再 INSERT/UPDATE
var existing Counter
err := plugin.Table("counters").Where("k = ?", key).First(&existing)
if errors.Is(err, ErrModelNotFound) {
    plugin.Exec(ctx, "INSERT INTO counters (k, v) VALUES (?, ?)", key, value)
} else {
    plugin.Exec(ctx, "UPDATE counters SET v = ? WHERE k = ?", value, key)
}

// 新:SaveOnConflict (R07) — 一行解决
_, err = plugin.SaveOnConflict(ctx, &Counter{K: key, V: value}, "k")
```

## 迁移路径建议

R04-R07 的所有新 API 都是**纯添加**,不破坏现有调用。推荐迁移顺序:

1. **R06 指标接入**:在 bootstrap 周期采样 `Stats()`,接 Prometheus(无调用方改动)
2. **R04 `WithTimeout`**:逐 DAO 替换 `xxxDBTimeout` 样板(机械替换,无风险)
3. **R04 `RunInTransaction`**:在 `SaveHeroes/SaveBuilds/...` 等多步写处启用(影响范围大,需 review 错误处理)
4. **R05 `MapScan` Find**:替换 `DB().QueryxContext` 逃逸(机械替换)
5. **R04 `BatchExec`**:替换 `mail_mysql_dao.go::InsertMailRecBatch` 等手拼 SQL
6. **R06 `Page`**:替换 `.Limit(20).Offset(20*page)` 算术
7. **R07 `SaveOnConflict`**:替换"先 SELECT 再 INSERT/UPDATE"两段式
8. **R07 Schema 自省**:仅在代码生成 / 迁移工具需要时使用
