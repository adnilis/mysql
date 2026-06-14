# R03: 测试强化 — Task

> **来源**:用户决策(跳过 R02,直接 R03)
> **目标**:补齐测试覆盖 + 加 Fuzz + 集成测试 stub
> **基准**:R01 覆盖率 73.5%,目标核心模块 ≥85%

## 范围

| # | 改动 | 文件 | 验收 |
|---|------|------|------|
| 1 | 3 个 Fuzz 函数(buildQuery / identifier / scanner) | 新建 `*_fuzz_test.go` | `go test -fuzz=FuzzXxx -fuzztime=5s` 通过 |
| 2 | `integration_test.go`(`//go:build integration`) | 新建 | 默认不编译;带 tag 编译通过 |
| 3 | 补 0% 函数覆盖率:Init/DB/Ping/Start/Or | 加测试 | 覆盖率上升 |
| 4 | 关键模块表驱动化(选 2-3 个) | 重构 | 测试数上升,可读性提升 |

## R02 defer 说明

R02(包/分层/命名重构)用户决定 defer 到下次会话,本次不做。
Fuzz 函数将放在现有文件结构(非子包),后续 R02 实施时再迁移。

## 验收标准

1. `go vet ./...` 0 issue
2. `gofmt -l .` 0 输出
3. `go test -race -count=1 ./...` 全绿
4. `go test -fuzz=FuzzXxx -fuzztime=5s ./...` 全过(分别跑 3 个 Fuzz)
5. `go test -tags=integration -run=^$ ./...` 编译通过(无 MySQL 时单元测试不依赖)
6. 覆盖率:核心模块 ≥85%

## 执行顺序

1. 补 0% 函数(Init/DB/Ping/Start)的测试
2. 加 3 个 Fuzz 函数
3. 加 integration_test.go stub
4. 表驱动化 2-3 个(ad-hoc → t.Run)
5. 三件套 + 覆盖率 + Fuzz 验证

## 风险

- Fuzz 可能产生 panic,需仔细处理
- Integration test 必须以 build tag 隔离,默认不参与 `go test ./...`
- 表驱动重构不应破坏已有测试

## 不做

- 子包拆分(等 R02)
- 性能优化(R04)
- README/CHANGELOG(R05)
- 任何 ctx 改造
