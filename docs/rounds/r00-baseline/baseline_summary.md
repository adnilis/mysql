# R00 综合基线摘要

> **TL;DR**: 测试 338/338 + 1 SKIP,覆盖率 71.5%,race 通过,gofmt 9 个文件待修,bench 12 项全跑出。R01–R05 验证有据可依。

---

## 1. 测试

| 维度 | 数值 |
|------|------|
| 总测试数 | 339(含子测试) |
| PASS | 338 |
| FAIL | 0 |
| SKIP | 1 (`TestBatchInsertCustomTable` — 方法未实现) |
| 含 race detector 用时 | 1.067s |

## 2. 覆盖率

| 维度 | 数值 |
|------|------|
| 总体 | 71.5% statements |
| 0% 函数数 | 21 |
| 最高覆盖函数 | `normalizeMySQLPluginConfig` 91.2% / `buildQuery` 86.9% / `batchInsertSingle` 86.0% / `BatchInsert` 83.3% |

**0% 函数清单**(R04 重点补):
- `plugin.go`: Init, DB, Ping
- `pool.go`: EnableQueryLog, SlowThreshold, Start
- `query.go`: InnerJoin, LeftJoin, RightJoin, Or, Not, Asc, Desc, Count, Update, Delete, Exec, Distinct, Take, Pluck
- `transaction.go`: Get

## 3. 静态检查

| 工具 | 状态 |
|------|------|
| `go vet ./...` | ✅ 0 issue |
| `gofmt -l .` | ⚠️ 9 文件未格式化 |
| `go build ./...` | ✅ 0 error |

**未格式化文件**: `batch_test.go`, `bench_test.go`, `config.go`, `logger.go`, `mysql_test.go`, `plugin.go`, `pool_test.go`, `query_test.go`, `scanner.go`

## 4. Benchmark(ns/op 降序)

| Benchmark | ns/op | B/op | allocs |
|-----------|------:|-----:|-------:|
| DSNBuild | 35,133 | 42,632 | 17 |
| QuerySelect | 17,467 | 5,437 | 30 |
| BatchInsert | 15,900 | 6,016 | 64 |
| GetDBParallel | 11,733 | 4,146 | 30 |
| ObjectPoolAcquire | 866.7 | 2,066 | 3 |
| FieldScannerMetaCache | 566.7 | 138 | 4 |
| FormatQuery | 4,133 | 3,522 | 21 |
| FormatTableNames | 2,567 | 2,610 | 14 |
| QueryBuild | 3,200 | 2,050 | 6 |
| InsertSQLBuild | 2,367 | 138 | 4 |
| UpdateByIDSQLBuild | 600.0 | 128 | 3 |
| QueryBuildCached | 100.0 | 0 | 0 |

## 5. 关键决策(从审计带入,本轮验证)

- 阶段 1 风险:`Start` 重入保护、`GetByID` 错误一致、`MySQLTransaction` 自动回滚 — 全部由 R01 处理
- 阶段 2 风险:`Count` 走 `buildQuery`、Where 多条件统一 AND、DSN 字段全生效 — 全部由 R01 处理
- 阶段 3 改造:`package plugins` → `package mysql` + 6 子包 — 由 R02 处理

## 6. 下一站:R01

9 项改动一次性合并为 PR #1:
1. `Start` 重入保护
2. `GetByID` 未命中统一 `ErrModelNotFound`
3. `MySQLError` 类型扩展(`errors.Is/As`)
4. `MySQLTransaction` 自动回滚
5. sentinel error 文档化
6. 链式 `Count` 走 `buildQuery`
7. Where 多条件 AND 拼接 + `OrWhere`
8. DSN 字段全生效
9. README 示例真实包名占位

每项配 ≥1 新单元测试。R01 完成后覆盖率应上升(因 Count/OrWhere 等会新增 case)。

---

**基线锁定期**: 2026-06-13 20:55
**负责人**: GitHub Copilot (MiniMax-M3)
**状态**: ✅ 阶段 0 完成,等待用户验收 → 进入 R01
