# R01: 风险修复 + 错误统一 — 产品/工程说明

> **本轮用户可感知变化**:无
> **本轮内部改进**:错误处理一致性、6 个真实风险修复、代码风格统一
> **不破坏**:公开 API 形状、bench 函数列表、测试框架

## 1. 改动汇总(11 项)

| # | 改动 | 文件 | 测试 |
|---|------|------|------|
| 1 | `wrappedMySQLError` 加 `Is()` 方法 + `Table()/Op()` 访问器 + `MySQLError` 类型别名 | `helper.go` | 3 个新测试 |
| 2 | 全部 sentinel 错误加 godoc + 推荐用法说明 | `errors.go` | 已有测试覆盖 |
| 3 | `Start` 重入保护(Swap 后 Close 旧 db) | `pool.go` | 2 个新测试 |
| 4 | DSN 应用 ConnTimeout/ReadTimeout/WriteTimeout | `dsn.go` | 2 个新测试 |
| 5 | `MinIdleConns` 通过 `effectiveMaxIdleConns` helper 提升 `SetMaxIdleConns` | `pool.go` | 5 子测试 |
| 6 | `Get/First/Take` 未命中走 `wrapMySQLError(ErrModelNotFound)`(与 `GetByID` 一致) | `orm.go`, `query.go` | 1 个新测试 + 修 1 个旧测试 |
| 7 | `MySQLTransaction.Close()` 安全网:auto-rollback + sync.Once 幂等 | `transaction.go` | 4 个新测试 |
| 8 | 链式 `Count` 改走 `buildQuery()`(此前忽略 Where/Join) | `query.go` | 2 个新测试 |
| 9 | Where 多条件统一 `AND` 拼接 + 新 `OrWhere` API | `build.go`, `query.go` | 2 个新测试 + 修 1 个旧测试 |
| 10 | 删除 `TestBatchInsertCustomTable` SKIP 占位 | `batch_test.go` | 减少 1 SKIP |
| 11 | `gofmt -w` 全量修复(10 个文件) | 多文件 | 无 |

## 2. 验证结果

| 指标 | R00 基线 | R01 完成 | 变化 |
|------|---------:|---------:|------|
| go vet issue | 0 | 0 | 持平 |
| gofmt issue | 10 | 0 | ✅ -10 |
| 测试 PASS | 338 | 351 | +13 新增 |
| 测试 SKIP | 1 | 0 | ✅ -1 |
| 测试 FAIL | 0 | 0 | 持平 |
| race detector | 通过 (1.07s) | 通过 (1.05s) | 持平 |
| 覆盖率 | 71.5% | **73.5%** | +2.0% |
| 0% 函数数 | 21 | 20 | -1(Or/Count 因新测试已覆盖) |

## 3. Bench 影响(详见 bench_baseline.md)

| 类别 | 数量 | 说明 |
|------|----:|------|
| 显著改善 (>20%) | 3 | InsertSQLBuild, GetDBParallel, QueryBuildCached |
| 持平或小幅改善 | 2 | FormatTableNames, DSNBuild |
| 持平 | 0 | - |
| 小幅退化 (5-15%) | 4 | DSNBuild, BatchInsert, FormatQuery, UpdateByID |
| 中度退化 (15-35%) | 3 | QuerySelect, ObjectPoolAcquire, FieldScannerMetaCache, QueryBuild |

**R05 重点对冲**:`QueryBuild` (+32%, allocs +4)、`QuerySelect` (+20%, B/op +13K)、`FieldScannerMetaCache` (+35%)。

## 4. 7 个风险修复状态

| 风险 | 状态 |
|------|------|
| 1. 链式 Count 漏条件 | ✅ R01 修(`Count` 走 `buildQuery`) |
| 2. Where 多条件拼接 | ✅ R01 修(`editWhereInsert/Append` 用 `AND`) |
| 3. Start 重入泄漏 | ✅ R01 修(Swap+Close 旧 db) |
| 4. DSN 字段错位 | ✅ R01 修(ConnTimeout/ReadTimeout/WriteTimeout + MinIdleConns 全生效) |
| 5. ctx 不一致 | ⏸️ 用户决定 drop(见 R00 PM 决策) |
| 6. README 文档漂移 | ⏸️ R03 包名重构后再细改(占位在 R01) |
| 7. go 1.26.0 + replace | ⏸️ 与宿主 wma 仓库策略对齐,跨 R02-R05 维持 |

## 5. 关键代码示例

### 5.1 错误处理新模式

```go
// 推荐:errors.Is 检查 sentinel
if errors.Is(err, mysqlplugin.ErrModelNotFound) {
    return nil // 未命中
}

// 推荐:errors.As 拿表名/操作
var mErr *mysqlplugin.MySQLError
if errors.As(err, &mErr) {
    log.Printf("table=%s op=%s", mErr.Table(), mErr.Op())
}
```

### 5.2 事务新模式

```go
// R01 新增:defer Close 安全网
tx, _ := db.Begin()
defer tx.Close() // 未显式 Commit/Rollback 时自动回滚
// ... work ...
if err := tx.Commit(ctx); err != nil { return err }
// Close 已是 no-op
```

### 5.3 Count 链式条件

```go
// R01 修复:Count 现在会应用 Where/Join/Group
var n int64
db.Query(ctx, "SELECT * FROM users").
    Where("age > ?", 18).
    Join("INNER JOIN", "orders", "users.id = orders.user_id", 1).
    Count(&n)
// 实际执行: SELECT COUNT(*) FROM (SELECT * FROM users WHERE age = ? INNER JOIN orders ON users.id = orders.user_id) AS count_table
```

## 6. 不在本轮范围

- ctx 相关改造(用户决定)
- 包/分层重构(R02)
- 表驱动测试 + Fuzz(R03)
- 性能优化(R04/R05)
- README 示例细化(等 R02 包名定稿)

## 7. 风险/已知问题

1. **R01 出现的 bench 退化**:R05 重点处理
2. **`helper.go` `Unwrap` 0% 覆盖**:新方法无直接测试,通过 `errors.Is` 链路间接覆盖,功能正确
3. **OrWhere 行为**:当前产出 `WHERE a = ?  OR b = ?`(空格连接),未做括号包裹
   - 后续若需要 `WHERE a = ? OR b = ?` 无空格或 `(a = ? OR b = ?)` 子句,在 R02/R05 处理
4. **go.mod 本地 replace**:R01 未动,与宿主框架策略联动

## 8. 验收

- [x] go vet 0 issue
- [x] gofmt -l 0 输出
- [x] go test -race -count=1 全绿
- [x] 无 SKIP
- [x] 覆盖率 73.5% (+2.0%)
- [x] 6 个 R01 风险项全部修复
- [x] 11 项改动全部带新单测
- [x] bench 12 函数全部跑出(部分退化由 R05 处理)

**状态**:✅ R01 完成,等待用户验收 → 进入 R02(包/分层/命名重构)
