# 变更记录

本项目遵循[语义化版本](https://semver.org/lang/zh-CN/)，提交信息使用 Conventional Commits。

## [1.1.0] - 2026-11-20

### 变更
- **默认部署方式改为 Linux**：README 的快速开始以 Linux 为主路径（一键 `install.sh` + systemd / 脚本后台 / Docker / 前台运行），Windows 的 `start.bat`、`stop.bat` 降为附录“方式三：Windows（可选）”。
- 产物名统一为 `magcraw`（Linux 无扩展名），README 命令、Dockerfile、脚本一致；Windows 仍为 `magnet-crawler.exe`。

### 新增
- `install.sh`：root 一键安装到 `/opt/magcraw`，建 `data/`（属 `nobody`）、写 systemd unit（`ProtectSystem=strict` + 仅 `data` 可写）、`enable --now`。
- `start.sh` / `stop.sh`：无 systemd 环境下的后台启停，带 PID 文件与健康检查。
- `deploy` 能力内置到 unit 模板：`CI_API_KEY` / `CI_CORS_ORIGINS` / `CI_PROXY` 的注释示例。
- `Dockerfile` + `.dockerignore`：多阶段构建，非 root 运行，`/app/data` 卷。
- README 新增 Nginx 反代 + HTTPS 的示例（`proxy_buffering off` 供 SSE）。

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