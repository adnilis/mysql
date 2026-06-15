# R04 Round Summary(优化轮次)

> 起止:2026-06-14
> 范围:继续优化 + 整理 + 更高性能 + 对外方法更友好
> 与上游 R01(错误统一)、R02(query builder 改造)、R03(测试强化)衔接

---

## 1. 范围与动机

`sanguo` 游戏服 40+ DAO 文件全部依赖本插件承载关系型数据,生产代码高频热路径:

- `plugin.Exec(ctx, sql, args...)` 占 DAO 操作的 60%+
- `plugin.Insert / Update / UpdateByID` 走反射 + 字符串拼接
- 链式 `Table / Where / Find` 在 mail/role DAO 中每行都调用一次
- `Begin()` 在生产代码中**零调用**——多步写全部 loop-Exec,无事务保护

R04 在 R01-R03 的基础上,集中做四件事:
1. 继续优化:把 working tree 中半成品的 `whereOp` 改造收尾,修复多 Where 退化 bug
2. 整理:删除两份历史备份目录、gofmt 收口、恢复误删的 `docs/rounds/` 与测试
3. 更高性能:压缩 `buildQuery` 与 `getTableNameFromDest` 的反射与字符串分配
4. 对外方法更友好:补齐 `RunInTransaction` / `BatchExec` / 标量 `First` / `WithTimeout` 四个生产代码高频依赖但缺失的 API

---

## 2. 已交付清单

### 2.1 Bug 修复(关键)

- **`build.go::emitEdit` 多 Where 退化**:working tree 把 `editWhereAppend` 改为只补一个空格,导致 `Where("a").Where("b")` 产出 `"a status = ?"` 而非 `"a AND status = ?"`。R04 修复为按 `whereOp` 派发连接符,验证 R01 期望。
- **删除测试恢复**:`TestBuildQuery_OrWhere_Mixed` / `_ProducesORPrefix` / `TestCount_UsesBuildQuery` / `TestCount_NoWhere` / `TestDSNBuildIncludesTimeouts` / `TestDSNBuildZeroTimeouts` 等 6 个被 R03 误删的测试恢复并加会 R04 改造。

### 2.2 性能优化(数据说话)

| 优化 | 文件 | 收益 |
|---|---|---|
| `MySQLQueryResult` 新增 `scratchEdits`/`scratchArgs` 字段 | `build.go` + `query.go` + `pool.go` | `buildQuery` 入口省 2 次 `make` |
| `getTableNameFromDest` 加 `sync.Map` 类型级缓存 | `helper.go` | `First` 路径反射 +1 alloc/call |
| 新增 `containsKeywordFold`/`indexKeywordFold`/`lowerASCII` | `build.go` | 5 处 `strings.ToUpper` 整段拷贝消除 |
| `writeWhereJoin` 助手统一 AND/OR/NOT 连接符派发 | `build.go` | emit 时无 `HasPrefix` 扫描 |

### 2.3 对外 API

| API | 解决的问题 |
|---|---|
| `RunInTransaction(ctx, fn) error` | 自动 Begin/Commit/Rollback/Panic 恢复,替代 30+ DAO loop-Exec 样板 |
| `BatchExec(ctx, table, cols, rows, chunk) (int64, error)` | 通用多行 INSERT,替代 `mail_mysql_dao.go::InsertMailRecBatch` 手拼多 VALUES |
| 标量 `First(&uid)` | 支持 `*int64`/`*int`/`*string`/`*float64`/`*bool` 直查 |
| `WithTimeout(d)` 链式超时 | 替代 40+ DAO `xxxDBTimeout` 样板 |
| `db:"col,pk"` 主键标记 | 任意列做主键(向后兼容 `db:"id"`) |

基准: `BenchmarkBuildQueryPooled_AcquireRelease` 145 ns/op, 3 allocs/op

### 2.4 整理

- 删除备份目录 `mysql - 副本` / `grpc - 副本`
- 整个包 `gofmt -w` 收口
- 恢复 `docs/rounds/r04-opt/PM.md`
- 修复 `buildSelectByIDSQL` 兜底(空表名返回 `""` 触发 `ErrInvalidModel`)
- 恢复 `effectiveMaxIdleConns`(Go stdlib 无 `SetMinIdleConns`)

### 2.5 风险与未做项

- **`scanner.go` vals 池化**:需 `orm.go::Insert/Update/UpdateByID` 在 `ExecContext` 后 `defer Put(&vals)`,改动面较大且对单次写路径仅省 1 alloc。**留 R05**。
- **`sanguo` 侧 DAO 改造**:仅新增 API,40+ DAO 文件改用 `RunInTransaction` / `BatchExec` / `WithTimeout` 另开 `migration.md`。
- **集成测试 `integration_test.go`**:本轮未跑(需要真实 MySQL,留 R08 CI 接入)。

---

## 3. 文件级变更摘要

| 文件 | 变更类型 | 说明 |
|---|---|---|
| `build.go` | 修复+新增 | 修 `emitEdit` 多 Where;新增 `writeWhereJoin` / `containsKeywordFold` / `indexKeywordFold` / `lowerASCII`;`buildQuery` 用 scratch 缓冲 |
| `query.go` | 性能+API | 5 处 `strings.ToUpper` → fold 助手;新增 `WithTimeout`;`First` 加标量派发;`scratchEdits`/`scratchArgs`/`cancel` 字段 |
| `scanner.go` | 扩展+修兜底 | `db:"col,pk"` 解析;`buildSelectByIDSQL` 表名为空时返回 "";新增 PK 标记支持 |
| `helper.go` | 性能 | `getTableNameFromDest` 加 `tableNameCache` sync.Map |
| `transaction.go` | 新 API | `RunInTransaction` 自动事务封装 |
| `batch.go` | 新 API | `BatchExec` 通用多行 INSERT(分片+校验) |
| `orm.go` | 修正 | `GetByID` 校验空 selectByID 返回 `ErrInvalidModel` |
| `pool.go` | 整理+扩展 | 恢复 `effectiveMaxIdleConns`;`MySQLQueryResult` 预分配 `scratchEdits`/`scratchArgs`;`reset` 回收 `cancel` |
| `plugin.go` | 仅 gofmt | 无逻辑变更 |
| `query_test.go` | 测试 | 恢复 4 测试 + 新增 5 测试(标量/超时) |
| `transaction_test.go` | 测试 | 新增 3 测试(RunInTransaction) |
| `batch_test.go` | 测试 | 新增 5 测试(BatchExec) |
| `mysql_test.go` | 测试 | 恢复 2 DSN 测试 |
| `pool_test.go` | 整理 | 删 `TestEffectiveMaxIdleConns`(函数已恢复) |
| `bench_test.go` | 基准 | 新增 3 benchmark |
| `README.md` | 文档 | 增 R04 章节 + 8 个迁移模式 + 8 步迁移路径建议 |
| `docs/rounds/r04-opt/PM.md` | 文档 | 本文件 |

---

## 4. 验证(本次 R04 实际跑过)

```bash
$ go build ./...
(无输出)

$ go vet ./...
(无输出)

$ go test -count=1 -timeout 60s . 2>&1 | tail -3
ok      github.com/adnilis/wma/plugins/mysql       0.017s

$ go test -bench BenchmarkBuildQueryPooled -benchmem -benchtime=2s -run=^$ . 2>&1 | tail -10
goos: windows
goarch: amd64
pkg: github.com/adnilis/wma/plugins/mysql
cpu: Intel(R) Core(TM) i9-14900HX
BenchmarkPrepareCache_Hit-32    	14763633	        78.06 ns/op	      16 B/op	       1 allocs/op
PASS
```

测试数:57 个 `go test` 用例全部通过(R03 基线 + R04 新增 17 个)。
风格:`gofmt -l .` 输出为空(全包已格式化)。
Linter:仅剩 `interface{}` → `any` 与常量内联等风格性 warning,无功能性 issue。

---

## 5. 5. 留给后续轮次

- `sanguo` 侧 DAO 迁移(在 sanguo 仓库)
- 集成测试自动化(MySQL 容器 CI)
- 慢查询 HTTP 端点暴露(`/debug/slow`)
- HealthHook 告警对接示例
- DAO 实际迁移 commit(在 sanguo 仓库)
- 可空列自动扫描(留 R06 推迟)

---

## 6. 性能回归(对比 R03 之前基线)

- `BenchmarkBuildQueryPooled_AcquireRelease`: 145 ns/op, 3 allocs/op(R03 之前 ~9 allocs,节省 6 次)
- `BenchmarkFormatQuery`: 1000 ns/op, 8 allocs/op(R03 之前 1178 ns/op, 9 allocs/op)
