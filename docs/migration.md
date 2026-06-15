# DAO 迁移指南(Migration Guide)

> 将 sanguo 现有 DAO 代码改用 R04-R13 新 API 的真实重构示例
>
> 适用:维护 wma-mysql 插件下游业务(sanguo 仓库 40+ DAO 文件)

---

## 0. 迁移原则

1. **R04-R13 的所有新 API 都是纯添加**,不破坏现有调用
2. **按风险从低到高** 逐个迁移,每个 commit 独立可回滚
3. **每步都跑测试**:执行 `go test ./... -tags integration` 验证不破坏

---

## 1. 8 步迁移路径(推荐顺序)

| 步骤 | 风险 | 影响 | 改动量 |
|---|---|---|---|
| 1 | 无 | R06 内存指标接入 | 加 `prometheus.Gauge` 周期采样(无调用方改动) |
| 2 | 低 | R04 `WithTimeout` | 逐 DAO 替换 `xxxDBTimeout` 样板 |
| 3 | 低 | R05 `MapScan` `Find` | 替换 `DB().QueryxContext` 逃逸 |
| 4 | 中 | R04 `RunInTransaction` | 在多步写处启用 |
| 5 | 中 | R04 `BatchExec` | 替换手拼多 VALUES SQL |
| 6 | 中 | R06 `Page` | 替换 `.Limit(20).Offset(20*page)` 算术 |
| 7 | 中 | R07 `SaveOnConflict` | 替换"先 SELECT 再 INSERT/UPDATE"两段式 |
| 8 | 低 | R11 限流 + 鉴权 + 慢查询缓冲 | admin 端点暴露 |

---

## 2. 模式 1:多步写改为事务(R04)

### 2.1 旧代码(`role_mysql_dao.go::SaveHeroes`)

```go
// 旧:loop-Exec,失败留脏数据(无事务保护)
plugin.Exec(ctx, "DELETE FROM heros WHERE rid = ?", uid)
for _, h := range heroes {
    plugin.Exec(ctx, "INSERT INTO heros (...) VALUES (?, ?, ?)", h.Field1, h.Field2, h.Field3)
}
```

### 2.2 新代码

```go
// 新:RunInTransaction(R04) — 自动 Begin/Commit/Rollback/Panic 恢复
err := plugin.RunInTransaction(ctx, func(tx *mysqlplugins.MySQLTransaction) error {
    if _, err := tx.Exec(ctx, "DELETE FROM heros WHERE rid = ?", uid); err != nil {
        return err
    }
    for _, h := range heroes {
        if _, err := tx.Exec(ctx, "INSERT INTO heros (...) VALUES (?, ?, ?)",
            h.Field1, h.Field2, h.Field3); err != nil {
            return err
        }
    }
    return nil
})
```

### 2.3 影响

- **正确性**:失败回滚,无脏数据
- **性能**:同事务,连接复用
- **代码**:每 SaveHeroes 类方法 -10 行手工错误处理

---

## 3. 模式 2:`xxxDBTimeout` 改链式 `WithTimeout`(R04)

### 3.1 旧代码(每个 DAO 顶部)

```go
const xxxDBTimeout = 3 * time.Second

func (d *xxxDao) FindByID(ctx context.Context, id int64) (*Model, error) {
    ctx, cancel := context.WithTimeout(ctx, xxxDBTimeout)
    defer cancel()

    var m Model
    err := d.plugin.GetByID(ctx, &m, id)
    // ...
}
```

### 3.2 新代码(无 const,无 defer)

```go
func (d *xxxDao) FindByID(ctx context.Context, id int64) (*Model, error) {
    var m Model
    err := d.plugin.Table("models").
        Where("id = ?", id).
        WithTimeout(3 * time.Second).
        First(&m)
    // ...
}
```

### 3.3 影响

- 移除 40+ 个 `xxxDBTimeout` 常量定义
- 移除 100+ 处 `context.WithTimeout + defer cancel` 样板
- `WithTimeout` 链式位置可调,贴近使用点

---

## 4. 模式 3:手拼多 VALUES INSERT 改 `BatchExec`(R04)

### 4.1 旧代码(`mail_mysql_dao.go::InsertMailRecBatch`)

```go
// 旧:mail DAO 手拼
var sb strings.Builder
sb.WriteString("INSERT INTO mail_rec (mid, tuid) VALUES ")
args := []any{}
for i, r := range recs {
    if i > 0 { sb.WriteString(", ") }
    sb.WriteString("(?, ?)")
    args = append(args, r.Mid, r.Tuid)
}
plugin.DB().ExecContext(ctx, sb.String(), args...)
```

### 4.2 新代码

```go
// 新:BatchExec(R04) — 通用批量,自动分片校验
rows := make([][]any, len(recs))
for i, r := range recs {
    rows[i] = []any{r.Mid, r.Tuid}
}
affected, err := plugin.BatchExec(ctx, "mail_rec",
    []string{"mid", "tuid"}, rows, 200)
```

### 4.3 影响

- 删除 `mail_mysql_dao.go::InsertMailRecBatch` 50+ 行手拼代码
- 自动按 200 行分片(避免 max_allowed_packet)
- 自动列数校验(每行长度 == len(columns))

---

## 5. 模式 4:`DB().QueryxContext` 逃逸改 MapScan `Find`(R05)

### 5.1 旧代码(`loading/role_mysql_dao.go`)

```go
// 旧:role/loading DAO 用 plugin.DB().QueryxContext 拿 map
rows, _ := plugin.DB().QueryxContext(ctx, sql, args...)
defer rows.Close()
for rows.Next() {
    item := make(map[string]any)
    rows.MapScan(item)
    // 处理 item["xxx"]...
}
```

### 5.2 新代码

```go
// 新:Find(&maps) (R05) — 链式 + 自动 MapScan
var items []map[string]any
plugin.Table("xxx").Where(...).Find(&items)
for _, item := range items {
    // 处理 item["xxx"]...
}
```

### 5.3 影响

- 删除 30+ 个逃逸调用
- 减少手工 defer rows.Close()
- 代码更紧凑,自动 ctx 传播

---

## 6. 模式 5:标量查询免定义空结构体(R04)

### 6.1 旧代码(`role_mysql_dao.go::FindRoleIDByName`)

```go
// 旧:为拿一个 int64 uid 定义空 User 结构
var user User
err := plugin.Table("users").Where("name = ?", name).First(&user)
uid := user.ID
```

### 6.2 新代码

```go
// 新:First(&uid) (R04) — 标量派发
var uid int64
err := plugin.Table("users").Where("name = ?", name).First(&uid)
```

### 6.3 影响

- 删除 10+ 个空 `User` / `*Model` 局部变量
- 无需反射扫描整行,直接 `QueryRow+Scan`

---

## 7. 模式 6:分页算术(R06)

### 7.1 旧代码

```go
// 旧:.Limit(20).Offset(20*page) 算术
err := plugin.Table("orders").Where(...).Limit(20).Offset(20*page).Find(&orders)
```

### 7.2 新代码

```go
// 新:Page(page, 20) (R06) — 自动夹紧 page<1
err := plugin.Table("orders").Where(...).Page(page, 20).Find(&orders)
```

### 7.3 影响

- 移除 20+ 处 `page-1 * size` 算术
- `Page` 自动 page<1 视为 1
- 统一 pageSize=0 不变更 limit 的边界

---

## 8. 模式 7:"有则更新无则插入"两段式(R07)

### 8.1 旧代码

```go
// 旧:先 SELECT 检查再 INSERT/UPDATE
var existing Counter
err := plugin.Table("counters").Where("k = ?", key).First(&existing)
if errors.Is(err, mysqlplugins.ErrModelNotFound) {
    plugin.Exec(ctx, "INSERT INTO counters (k, v) VALUES (?, ?)", key, value)
} else {
    plugin.Exec(ctx, "UPDATE counters SET v = ? WHERE k = ?", value, key)
}
```

### 8.2 新代码

```go
// 新:SaveOnConflict (R07) — IModel 版一行解决
_, err = plugin.SaveOnConflict(ctx, &Counter{K: key, V: value}, "k")
```

### 8.3 影响

- 删除 5+ 个"先 SELECT 再 INSERT/UPDATE"两段式
- 消除竞态(并发场景下原两段式可能重复插入)
- 1 次 SQL,无中间 SELECT

---

## 9. 模式 8:Admin HTTP 端点集成(R11)

### 9.1 旧代码(无可见性端点)

```go
// 旧:排查问题时只能 grep 日志
grep "SELECT" server.log | tail -50
```

### 9.2 新代码(挂接 admin server)

```go
// 选项 A:独立 HTTP server
go http.ListenAndServe(":8080", plugin.AdminHandler())

// 选项 B:wma 框架 admin mux 集成
app.AdminMux().Handle("/mysql/", http.StripPrefix("/mysql", plugin.AdminHandler()))

// 选项 C:Prometheus 抓取
// scrape_configs:
//   - job_name: 'wma-mysql'
//     metrics_path: /metrics
//     static_configs:
//       - targets: ['mysql-host:8080']
```

### 9.3 影响

- SRE 可自助排查慢查询 / 指标 / schema,无需 dev 协助
- 慢查询缓冲对接 Slack 即时告警
- Prometheus 抓取提供 SLO 面板数据源

---

## 10. 模式 9:批量 upsert `BatchInsertOnConflict`(R09)

### 10.1 旧代码(`counter_mysql_dao.go` 之类)

```go
// 旧:每条循环,可能部分失败
for _, c := range counters {
    _, err := plugin.SaveOnConflict(ctx, &c, "k")
    if err != nil { return err }
}
```

### 10.2 新代码

```go
// 新:BatchInsertOnConflict (R09) — 批量 upsert
_, err := plugin.BatchInsertOnConflict(ctx, counters, 200, "k")
```

### 10.3 影响

- N 次 SQL → 1 次
- 自动 200 行分片
- 整体原子性(单批内要么全成功要么全失败)

---

## 11. 模式 10:`WithRetry` 死锁重试(R09)

### 11.1 旧代码

```go
// 旧:无重试,死锁直接报错
_, err := plugin.Exec(ctx, "UPDATE orders SET stock = stock - 1 WHERE id = ?", id)
if err != nil {
    return err
}
```

### 11.2 新代码

```go
// 新:WithRetry 包裹(R09) — 死锁自动重试
err := mysqlplugins.WithRetry(ctx, mysqlplugins.DefaultRetryPolicy(), func(ctx context.Context) error {
    _, err := plugin.Exec(ctx, "UPDATE orders SET stock = stock - 1 WHERE id = ?", id)
    return err
})
```

### 11.3 影响

- 死锁自动恢复(无需人工介入)
- 5 次指数退避 + 抖动
- 默认策略:50ms→100ms→200ms→400ms→800ms

---

## 12. 模式 11:`BulkUpdate` 单条 SQL 批量更新(R08)

### 12.1 旧代码(`inventory_mysql_dao.go`)

```go
// 旧:per-row UPDATE,N 次网络
for _, item := range items {
    _, err := plugin.Exec(ctx, "UPDATE inventory SET stock = ? WHERE id = ?",
        item.Stock, item.ID)
    if err != nil { return err }
}
```

### 12.2 新代码

```go
// 新:BulkUpdate (R08) — 单条 SQL CASE WHEN
ids := make([]any, len(items))
values := make([]any, len(items))
for i, item := range items {
    ids[i] = item.ID
    values[i] = item.Stock
}
affected, err := plugin.BulkUpdate(ctx, "inventory", "id",
    ids, "stock", values)
```

### 12.3 影响

- N 次网络 → 1 次
- 自动分片(CASE WHEN ≤ 16, IN ≤ 256)
- 比 BatchInsertOnConflict 更轻(无 IModel 反射)

---

## 13. 真实迁移 commit 示例

参考 sanguo 仓库 R05 提交 "refactor(dao): 改用 R04-R05 新 API":

```bash
git log --oneline | head -3
# 73b200c 优化
# d89f5a3 优化
# 1be32d9 refactor(mysql): R01 风险修复 + 错误统一
```

典型 commit 流程:

```bash
# 1. 分支
git checkout -b refactor/r05-dao-migration

# 2. 逐文件迁移(每 DAO 一个 commit,便于 review)
git add xapp/module/role/dao/role_mysql_dao.go
git commit -m "refactor(role-dao): 改用 R04 RunInTransaction + WithTimeout"

git add xapp/module/mail/dao/mail_mysql_dao.go
git commit -m "refactor(mail-dao): 改用 R04 BatchExec + R05 MapScan"

# 3. 集成测试
go test ./... -tags integration

# 4. 合并
git checkout main && git merge --no-ff refactor/r05-dao-migration
```

---

## 14. 回滚策略

每个 commit 独立可回滚;若某次迁移引入性能问题:

```bash
# 回滚单个 DAO
git revert <commit-sha> -- xapp/module/role/dao/role_mysql_dao.go

# 完全回滚到迁移前
git revert <merge-commit-sha>
```

R04-R13 的所有 API 都在 `MySQLPlugin` / `MySQLQueryResult` 上加方法,
不修改既有方法签名,所以"撤销迁移"等于"删除新增调用",零破坏。

---

## 15. 验证清单

每次迁移后,跑以下检查:

```bash
# 编译
go build ./...

# 单元测试
go test ./...

# 集成测试(需 -tags=integration + 真实 MySQL)
docker run -d --name mysql-test -p 3306:3306 \
  -e MYSQL_ROOT_PASSWORD=secret -e MYSQL_DATABASE=testdb mysql:8
MYSQL_TEST_USER=root MYSQL_TEST_PASSWORD=secret \
  go test -tags integration -count=1 ./...

# 性能基线对比
go test -bench=. -benchmem -benchtime=3s > bench_after.txt
diff bench_before.txt bench_after.txt
# 期望:无显著回归(< 5%)
```

---

## 16. 进一步阅读

- [architecture.md](architecture.md) — 内部设计
- [performance.md](performance.md) — 性能优化
- [observability.md](observability.md) — 监控 / 告警
- [api-reference.md](api-reference.md) — 完整 API
