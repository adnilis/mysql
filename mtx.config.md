# MTX Loop 项目配置 — MySQL 插件

| 配置项 | 值 |
|--------|-----|
| PROJECT_NAME | sangou-wma-mysql-plugin |
| ROUNDS_DIR | docs/rounds |
| BACKLOG_FILE | docs/backlog.md |
| DEV_SERVER | (无,后端库) |
| APP_MODE | library (no UI) |
| TYPECHECK_CMD | `go vet ./...` |
| TEST_CMD | `go test ./... -race -count=1` |
| BUILD_CMD | `go build ./...` |
| BENCH_CMD | `go test "-bench=." -benchmem -run=NONE -benchtime=3x ./...` |
| COMMIT_PREFIX | `refactor(mysql):` |
| TEST_ENTRY | sqlmock (无 build tag) / `//go:build integration` (可选) |
| BASELINE_DOC | `docs/rounds/r00-baseline/` |
| GO_VERSION | 1.26.0 |
| GO_MOD_PATH | (本地 replace,见 go.mod) |
| LAST_COMMIT | d8cbd89 (R03) |
| NEXT_ROUND | R04 (性能优化) |

## Round 映射(用户确认的 5 个 PR + 阶段 0)

| Round | 阶段 | 标题 | 状态 | 关键交付 |
|-------|------|------|------|---------|
| r00 | 阶段 0 | 基线测量 | ✅ 完成 | bench/test/gofmt/vet 报告 |
| r01 | 阶段 1+2 | 风险修复 + 错误统一 | ✅ 完成 + 已 commit (1be32d9) | 11 项改动 + 17 个新单测 |
| r02 | 阶段 3 | 包/分层/命名重构 | ⏸️ 用户决定 defer 到下次会话 | package mysql + 6 子包 |
| r03 | 阶段 4 | 测试强化 | 🚀 本会话启动 | 表驱动 + Fuzz + 集成 |
| r04 | 阶段 5 | 性能优化 | 待启动 | 仅 ≥10% bench 提升 |
| r05 | 阶段 6 | 文档/发布 | 待启动 | README/CHANGELOG/Makefile |

## Round 适配(后端库,无 UI)

- Step 6 验证改为: `go vet` → `go test ./... -race` → (可选) `go test -bench=...`
- 跳过 Playwright UI 验证
- code-simplifier 仍执行(在 code 完成后、test 前)
