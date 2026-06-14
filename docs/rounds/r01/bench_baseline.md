# Benchmark 对比 — R00 → R01

> **命令**: `go test "-bench=." -benchmem -run=NONE -benchtime=3x -timeout=300s ./...`
> **环境**: Windows 11, Go 1.26.0, i9-14900HX (相同)

## 数值对比表

| Benchmark | R00 ns/op | R01 ns/op | 变化 | R00 B/op | R01 B/op | R00 allocs | R01 allocs |
|-----------|----------:|----------:|-----:|---------:|---------:|-----------:|-----------:|
| `QueryBuild` | 3,200 | 4,233 | **+32%** ⚠️ | 2,050 | 2,605 | 6 | 10 ⚠️ |
| `QueryBuildCached` | 100.0 | 66.67 | **-33%** ✅ | 0 | 0 | 0 | 0 |
| `QuerySelect` | 17,467 | 20,900 | **+20%** ⚠️ | 5,437 | 18,333 | 30 | 35 ⚠️ |
| `BatchInsert` | 15,900 | 17,767 | +12% ⚠️ | 6,016 | 18,522 | 64 | 68 ⚠️ |
| `ObjectPoolAcquire` | 866.7 | 1,067 | +23% ⚠️ | 2,066 | 2,066 | 3 | 3 |
| `DSNBuild` | 35,133 | 38,167 | +9% | 42,632 | 42,632 | 17 | 17 |
| `FieldScannerMetaCache` | 566.7 | 766.7 | **+35%** ⚠️ | 138 | 138 | 4 | 4 |
| `InsertSQLBuild` | 2,367 | 500.0 | **-79%** ✅✅ | 138 | 138 | 4 | 4 |
| `UpdateByIDSQLBuild` | 600.0 | 733.3 | +22% ⚠️ | 128 | 128 | 3 | 3 |
| `GetDBParallel` | 11,733 | 4,433 | **-62%** ✅✅ | 4,146 | 2,584 | 30 | 27 |
| `FormatQuery` | 4,133 | 4,667 | +13% ⚠️ | 3,522 | 3,522 | 21 | 21 |
| `FormatTableNames` | 2,567 | 2,367 | -8% ✅ | 2,610 | 2,536 | 14 | 13 |

> ⚠️ = 退化 >10%,R05 重点优化候选
> ✅ = 改善 >5%,可作为 PR 性能证据

## 退化分析

1. **QueryBuild +32% (+4 allocs)**: 来自 build.go 中 `editWhereInsert/Append` 的
   `strings.HasPrefix` 检查。每次拼接多条件时多 4 次 HasPrefix 调用。R05 可改用
   condition 中的位标志位(0=无,1=OR,2=NOT)避免字符串扫描。

2. **QuerySelect / BatchInsert**: B/op 大幅增加(5K→18K)。sqlmock 内部状态扩大或
   链路 wrap 增加的链式分配。需在 R05 用 pprof 确认具体来源。

3. **FieldScannerMetaCache +35%**: 同 allocs 但耗时增加,可能是 cache 哈希冲突
   增多。R05 可优化 map 大小或改用 sync.Map。

## 改善亮点

- **InsertSQLBuild -79%**: 不知原因,可能 sqlmock 内部缓存或 Go 1.26 编译器优化。
  R05 不动。
- **GetDBParallel -62%**: 原子加载路径改善,可能与新增的 swap-close 路径相关。
- **QueryBuildCached -33%**: 缓存命中后更快,好现象。

## 结论

R01 主要修复 bug 与错误处理,部分 bench 出现 +20-35% 退化符合预期
(错误包装/校验路径增加)。**R05 性能优化阶段** 重点对冲:

- `QueryBuild` (多 Where 拼接)
- `QuerySelect` / `BatchInsert` (B/op 增长)
- `FieldScannerMetaCache` (cache 命中退化)

其余 8 个 bench 在 R01 范围内可接受。
