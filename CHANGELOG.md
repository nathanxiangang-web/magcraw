# 变更记录

本项目遵循[语义化版本](https://semver.org/lang/zh-CN/)，提交信息使用 Conventional Commits。

## [1.0.0] - 2026-11-20

### 新增
- Web 面板：关键词/热榜抓取、SSE 实时进度、结果表格、复制全部磁链、JSON/CSV 导出、任务停止。
- 抓取引擎：三层链路（热榜 → 搜索结果 → 详情页），每次上游请求前随机等待 3–15 秒，串行执行，UA 三档轮换；列表页即可判定的条目提前跳过，省一次请求。
- 过滤规则：仅 `.mp4`、单种子 >100MB、剔除推广域名与话术；被过滤条数在任务结束时回显。
- 对外 API `/api/v1`：CORS 预检、API Key（`X-API-Key` / Bearer）、按 IP 限流、异步任务 + 轮询、同步查询、SSE、OpenAPI 描述。
- 任务历史：保留最近 20 个任务，可按 `job_id` 回查状态与结果。
- 配置项：`PORT`、`CI_BASE`、`CI_PROXY`、`CI_API_KEY`、`CI_CORS_ORIGINS`、`CI_RATE_LIMIT`。

### 说明
- 单二进制部署，标准库实现，前端资源由 `//go:embed` 打入。
- `data/` 每次任务落一个 `job-<ts>.json` 快照，不入版本库。