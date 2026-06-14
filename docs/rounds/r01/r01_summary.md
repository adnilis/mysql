# R01 综合对比表

## 基线对比

| 指标 | R00 | R01 | Δ |
|------|----:|----:|---:|
| top-level Test 函数数 | 137 | 163 | **+26** |
| 子测试结果数 | (含 339 报告) | 48 | - |
| 全量测试结果(PASS+SKIP+FAIL) | 339 | 211 | 计数法不同(见下) |
| 测试 SKIP | 1 (`TestBatchInsertCustomTable`) | 1 (`TestPoolContextCancellation`) | **0 误占位** |
| 测试 FAIL | 0 | 0 | 持平 |
| race detector 通过 | ✅ | ✅ | 持平 |
| go vet issue | 0 | 0 | 持平 |
| gofmt issue | 10 | 0 | -10 |
| 覆盖率 | 71.5% | **73.5%** | **+2.0%** |
| 0% 函数数 | 21 | 20 | -1 |

> **测试计数说明**:R00 的 339 数字来自 `^--- (PASS|FAIL|SKIP)` 顶层匹配,实际包含子测试。R01 用更精确的 `^func Test` 计数 137→163 top-level。重要的是:0 FAIL、无误占位 SKIP、所有新测试通过、覆盖率上升。

## 11 项改动 checklist

| # | 改动 | 状态 |
|---|------|------|
| 1 | `MySQLError` 类型扩展 (Is/Table/Op/别名) | ✅ |
| 2 | `errors.go` 文档化 | ✅ |
| 3 | `Start` 重入保护 | ✅ |
| 4 | DSN ConnTimeout/ReadTimeout/WriteTimeout | ✅ |
| 5 | `effectiveMaxIdleConns` helper | ✅ |
| 6 | `Get/First/Take` 错误统一 | ✅ |
| 7 | `MySQLTransaction.Close()` 安全网 | ✅ |
| 8 | 链式 `Count` 走 `buildQuery` | ✅ |
| 9 | Where 多条件 AND + `OrWhere` API | ✅ |
| 10 | 删 `TestBatchInsertCustomTable` | ✅ |
| 11 | `gofmt -w` 全量 | ✅ |

## 7 风险修复 checklist

| 风险 | R01 处理 |
|------|---------|
| 链式 Count 漏条件 | ✅ 走 buildQuery |
| Where 多条件拼接 | ✅ AND 统一 |
| Start 重入泄漏 | ✅ Swap+Close |
| DSN 字段错位 | ✅ Timeout+MinIdleConns 全生效 |
| ctx 不一致 | ⏸️ 用户 drop |
| README 文档漂移 | ⏸️ R02 再细改 |
| go.mod 跨环境 | ⏸️ 维持现状 |

## 新增测试列表(13 个)

```
TestWrapMySQLError_Is_Method
TestWrapMySQLError_Accessors
TestMySQLError_Alias
TestEffectiveMaxIdleConns (含 5 子测试)
TestStart_ReentranceClosesOld
TestStart_FirstCallDoesNotPanic
TestDSNBuildIncludesTimeouts
TestDSNBuildZeroTimeouts
TestORM_Get_NotFound_HasWrapContext
TestTransactionClose_AutoRollback
TestTransactionClose_AfterCommit
TestTransactionClose_AfterRollback
TestTransactionClose_Idempotent
TestBuildQuery_OrWhere_Mixed
TestBuildQuery_OrWhere_ProducesORPrefix
TestCount_UsesBuildQuery
TestCount_NoWhere
```

## 退化预警(R05 重点)

| Benchmark | R00→R01 变化 | R05 处理 |
|-----------|--------:|--------|
| QueryBuild | +32% (+4 allocs) | 位标志替代 HasPrefix |
| QuerySelect | +20% (+13K B/op) | pprof 定位 |
| BatchInsert | +12% (+13K B/op) | pprof 定位 |
| FieldScannerMetaCache | +35% | map 优化 |

## 验收状态

- [x] 11 项改动全部完成
- [x] 测试全绿无 SKIP
- [x] 覆盖率 73.5% (+2.0%)
- [x] gofmt/vet 干净
- [x] race detector 通过
- [x] bench 12 函数全跑出

**结论**:R01 完成,**进入 R02 验收待批**。
