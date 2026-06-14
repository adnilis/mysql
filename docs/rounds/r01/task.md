# R01: 风险修复 + 错误统一 — Task

> **来源**:R00 审计 + 用户确认(11 项)
> **目标**:消除 7 风险中 6 个(ctx 已 drop)+ 统一错误处理 + 顺手清理
> **不变**:外部公开 API 形状、bench 函数列表、测试框架(Go native + sqlmock)

## 改动项

| # | 文件 | 改动 | 验收 |
|---|------|------|------|
| 1 | `helper.go` | `wrappedMySQLError` 加 `Is()`/`As()` 方法,导出 `MySQLError` 文档类型,新增 `Table()`/`Op()` 访问器 | 新单测 |
| 2 | `errors.go` | 全部 sentinel error 加 godoc 注释,标 `errors.Is` 推荐用法 | `go doc` 可见 |
| 3 | `pool.go` | `Start` 加重入保护(取旧 db → Close → 再 Store);`MinIdleConns` 在 Start 中影响 SetMaxIdleConns | 新单测 |
| 4 | `dsn.go` | `ConnTimeout` → `mysql.Config.Timeout`,`ReadTimeout/WriteTimeout` → `mysql.Config.ReadTimeout/WriteTimeout` | 新单测 DSN 字符串 |
| 5 | `orm.go` | `Get` 未命中也走 `wrapMySQLError("","get",ErrModelNotFound)`,与 `GetByID` 一致 | 单测更新 |
| 6 | `transaction.go` | `MySQLTransaction` 加 `committed` 标志,`Commit` 后再调用 `Commit/Rollback` 返回 `sql.ErrTxDone` 友好提示;`Rollback` 后再 `Rollback` 同理 | 新单测 |
| 7 | `query.go` | `Count` 改用 `qr.buildQuery()`(此前直接 `qr.query` 忽略 wheres/joins);新增 `OrWhere` API | 新单测 |
| 8 | `build.go` | `editWhereInsert/Append` 多条件拼接统一 `AND`(Or/Not 前缀条件除外) | 新单测 |
| 9 | `batch_test.go` | 删除 `TestBatchInsertCustomTable`(SKIP 占位) | 覆盖率上升 |
| 10 | 9 个文件 | `gofmt -w` 顺手修复 | `gofmt -l` 输出空 |
| 11 | `README.md` | 示例占位改用真实包名路径(阶段 3 再细改) | 文档同步 |

## 验收标准

1. `go vet ./...` 0 issue
2. `gofmt -l .` 0 输出
3. `go test -race -count=1 ./...` 全绿,无 SKIP
4. `go test -cover` 覆盖率 ≥ 75%(基线 71.5% + 新单测)
5. `go test -bench="." -benchmem -run=NONE -benchtime=3x ./...` 无回归
6. `errors.Is(err, ErrModelNotFound)` 在所有「未命中」路径都返回 true
7. `Start` 重复调用不泄漏 db 句柄
8. `Count` 链式条件被正确应用

## 执行顺序(避免相互阻塞)

1. helper.go(基础类型) → 测试
2. errors.go(文档) → 不需测试
3. dsn.go + pool.go(一起改更内聚) → 测试
4. orm.go(统一) → 测试
5. transaction.go(独立) → 测试
6. query.go Count/OrWhere + build.go WHERE → 测试
7. 删 SKIP + gofmt
8. 全量验证

## 风险

- `MySQLTransaction` 自动回滚保护会改变 `Rollback` 在已 commit 后的行为(原来返回 nil,可能改为返回错误)。需更新测试预期。
- 链式 `Count` 走 `buildQuery` 会改变既有行为(若有人依赖 ignore wheres)。需测试确认。

## 不做

- ctx 相关任何改造
- 引入新依赖
- 删任何 bench 函数
- 改公开 API 形状(仅内部错误处理)
