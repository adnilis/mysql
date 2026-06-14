# R00: 阶段 0 基线测量 — 工程说明

> **本轮无用户可感知变化**。本轮为内部工程基线,目的是锁定 R01–R05 各阶段验证依据。

## 范围

- 跑通 `go test`、`go test -bench`、`go vet`、`gofmt`、`go build`,记录所有数值
- 锁定 R01 风险修复时的覆盖率基线(71.5% → R03/R04 目标 ≥85%)
- 锁定 R04 性能优化时的 bench 基线
- 落临时文件到 `docs/rounds/r00-baseline/`,阶段 0 结束后视情清理

## 依赖与命令

| 工具 | 版本/配置 |
|------|-----------|
| Go | 1.26.0 windows/amd64 |
| 模块 | `github.com/adnilis/wma/plugins/mysql` |
| 替换 | `github.com/adnilis/wma => E:\sangou\wma` (本地 replace) |
| CGO | 启用(因 race detector 与覆盖率需要) |

## 关键命令(PowerShell 适配版)

```powershell
# 1. 测试 + race
$env:CGO_ENABLED=1
go test -race -count=1 -timeout 180s -v ./... `
  | Out-File -FilePath test_raw.txt -Encoding utf8

# 2. Bench
go test "-bench=." -benchmem -run=NONE -benchtime=3x -timeout=300s ./... `
  | Out-File -FilePath bench_raw.txt -Encoding utf8

# 3. 覆盖率
go test -coverprofile cover.out -count=1 ./...
go tool cover -func cover.out | Out-File -FilePath cover_funcs.txt -Encoding utf8

# 4. 静态检查
gofmt -l . | Out-File -FilePath gofmt_raw.txt -Encoding utf8
go vet ./...
go build ./...
```

> **PowerShell 注意点**:
> - `.` 在 `-bench=.` 中是属性访问符,必须用 `-bench="."` 包裹
> - `-coverprofile=path` 中的 `=` 在某些 shell 下会被拆分,改用 `-coverprofile path` 空格分隔
> - `Out-File` 默认 UTF-16,务必显式 `-Encoding utf8`

## 交付物

| 文件 | 用途 |
|------|------|
| `bench_raw.txt` | bench 原始输出(1.3 KB) |
| `test_raw.txt` | 测试 verbose 输出(17.4 KB) |
| `cover.out` | 覆盖率原始 profile(44.6 KB) |
| `cover_funcs.txt` | 逐函数覆盖率表(7.4 KB,106 行) |
| `gofmt_raw.txt` | 未格式化文件列表 |
| `bench_baseline.md` | bench 数值汇总与解读 |
| `test_baseline.txt` | 测试结果与覆盖率摘要 |

## 基线数字(R01-R05 通用对照)

| 指标 | 当前 | 目标(R04 完) | 备注 |
|------|-----:|-------------:|------|
| 测试通过 | 338/338 + 1 SKIP | 全部通过 | SKIP 处置由 R01 决定 |
| 覆盖率 | 71.5% | ≥85% | 见 cover_funcs.txt |
| gofmt issue | 9 文件 | 0 | R01 一并修复 |
| go vet issue | 0 | 0 | 已干净 |
| go build error | 0 | 0 | 已干净 |
| race detector | 通过(1.07s) | 通过 | 已干净 |

## 阶段 0 完成检查

- [x] go test -race 全绿
- [x] go test -bench=. 12 个 bench 全部跑出
- [x] cover.out 生成 + 逐函数覆盖率
- [x] gofmt/vet/build 全检
- [x] 所有数据落到 docs/rounds/r00-baseline/
- [x] bench_baseline.md / test_baseline.txt 按用户要求命名

## 进入 R01 前的待澄清

1. **TestBatchInsertCustomTable** 的 SKIP:是补实现 `BatchInsertCustomTable` 方法,还是删测试?(R01 内决定)
2. **9 个 gofmt 文件**:是否在 R01 顺手 `gofmt -w`?(建议是,无风险)
3. **DSN 字段 17 次分配** 35 μs:是否纳入 R04 性能优化候选?

## 风险登记

- 本地 `replace github.com/adnilis/wma => E:\sangou\wma` 在跨机器复现时会断链,但不影响本机测量
- bench 用 3 次迭代,数值偏少;R05 优化对比时建议改 `-benchtime=1s` 取稳定均值
