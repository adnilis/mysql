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
