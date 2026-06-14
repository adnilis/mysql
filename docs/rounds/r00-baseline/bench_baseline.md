# Benchmark 基线 — Round R00

> **日期**: 2026-06-13
> **环境**: Windows 11, Go 1.26.0 windows/amd64
> **CPU**: Intel(R) Core(TM) i9-14900HX
> **包**: `github.com/adnilis/wma/plugins/mysql`
> **命令**: `go test -bench="." -benchmem -run=NONE -benchtime=3x -timeout=300s ./...`
> **Go 版本**: go1.26.0 windows/amd64

## 完整输出

```
goos: windows
goarch: amd64
pkg: github.com/adnilis/wma/plugins/mysql
cpu: Intel(R) Core(TM) i9-14900HX
BenchmarkQueryBuild-32                         3              3200 ns/op           2050 B/op           6 allocs/op
BenchmarkQueryBuildCached-32                   3               100.0 ns/op            0 B/op           0 allocs/op
BenchmarkQuerySelect-32                        3             17467 ns/op           5437 B/op          30 allocs/op
BenchmarkBatchInsert-32                        3             15900 ns/op           6016 B/op          64 allocs/op
BenchmarkObjectPoolAcquire-32                  3               866.7 ns/op         2066 B/op           3 allocs/op
BenchmarkDSNBuild-32                           3             35133 ns/op          42632 B/op          17 allocs/op
BenchmarkFieldScannerMetaCache-32              3               566.7 ns/op          138 B/op           4 allocs/op
BenchmarkInsertSQLBuild-32                     3              2367 ns/op            138 B/op           4 allocs/op
BenchmarkUpdateByIDSQLBuild-32                 3               600.0 ns/op          128 B/op           3 allocs/op
BenchmarkGetDBParallel-32                      3             11733 ns/op           4146 B/op          30 allocs/op
BenchmarkFormatQuery-32                        3              4133 ns/op           3522 B/op          21 allocs/op
BenchmarkFormatTableNames-32                   3              2567 ns/op           2610 B/op          14 allocs/op
PASS
ok      github.com/adnilis/wma/plugins/mysql    0.046s
```

## 数值汇总表(按耗时降序)

| Benchmark | ns/op | B/op | allocs/op | 说明 |
|-----------|------:|-----:|----------:|------|
| `BenchmarkDSNBuild` | 35,133 | 42,632 | 17 | DSN 构造,含 17 次分配(待优化) |
| `BenchmarkQuerySelect` | 17,467 | 5,437 | 30 | 链式查询 + sqlmock 执行 |
| `BenchmarkBatchInsert` | 15,900 | 6,016 | 64 | 批量插入 |
| `BenchmarkGetDBParallel` | 11,733 | 4,146 | 30 | 并发取 db(原子加载) |
| `BenchmarkObjectPoolAcquire` | 866.7 | 2,066 | 3 | QueryResult 对象池获取 |
| `BenchmarkFieldScannerMetaCache` | 566.7 | 138 | 4 | scanner 元数据缓存查找 |
| `BenchmarkFormatQuery` | 4,133 | 3,522 | 21 | SQL 文本格式化 |
| `BenchmarkFormatTableNames` | 2,567 | 2,610 | 14 | 表名格式化 |
| `BenchmarkQueryBuild` | 3,200 | 2,050 | 6 | 链式查询构建 |
| `BenchmarkInsertSQLBuild` | 2,367 | 138 | 4 | Insert SQL 模板 |
| `BenchmarkUpdateByIDSQLBuild` | 600.0 | 128 | 3 | UpdateByID SQL 模板 |
| `BenchmarkQueryBuildCached` | 100.0 | 0 | 0 | 缓存命中后构建 |

## 关注点(供阶段 5 性能优化参考)

1. **`BenchmarkDSNBuild`**: 35 μs + 42 KB,17 次分配。可考虑预编译 DSN 字符串,或延迟到第一次连接。
2. **`BenchmarkQuerySelect` / `BenchmarkBatchInsert`**: 每次 30-64 次分配,主要在 SQL 拼接与 sqlmock。优化空间有限(测试 mock 本身有开销)。
3. **`BenchmarkQueryBuildCached` vs `BenchmarkQueryBuild`**: 缓存命中后快 32 倍(100 ns vs 3.2 μs),缓存有效。
4. **`BenchmarkGetDBParallel`**: 11.7 μs,30 次分配。`atomic.Value.Store/Load` + `sqlx.NewDb` 包装可能贡献分配。

> **基线锁定**: 任何阶段 5 优化必须相对本表展示 ≥10% 提升,且全量测试不退化。

## 重跑命令

```powershell
cd e:\sangou\wma\plugins\mysql
$env:CGO_ENABLED=1
go test "-bench=." -benchmem -run=NONE -benchtime=3x -timeout=300s ./... `
  | Out-File -FilePath bench_raw.txt -Encoding utf8
```

> **PowerShell 注意**: `-bench=.` 中的 `.` 会被 PowerShell 视为属性访问,需用 `-bench="."`(双引号)包裹。
