# magcraw — 磁链抓取面板

单二进制 Go 服务 + 原生前端：从一个磁力索引站抓取**文件名、大小、磁力链接**，内置「只留 mp4 / 单种子 >100MB / 去广告」筛选，并对外提供带 **CORS + API Key + 限流**的 HTTP 接口，可直接给其他网站调用。

- **拟人化抓取**：串行执行，每次上游请求前随机等待 **3–15 秒**，UA 轮换，不并发轰炸目标站
- **实时反馈**：页面通过 SSE 显示阶段、当前关键词、本轮随机延迟、已抓条数
- **开箱即用**：零第三方依赖，`go build` 出一个 exe，前端用 `//go:embed` 打进二进制
- **可用性兜底**：列表页就能判定的小种子/广告不会再去请求详情页，省掉一次 3–15 秒等待

---

## 1. 快速开始

### 方式一：直接运行（Windows）

```bat
cd magnet-crawler
start.bat
```

然后浏览器打开 <http://127.0.0.1:8900>。停止：`stop.bat`。

### 方式二：源码构建

需要 Go 1.22+（本项目只用到标准库，无 `go.sum`）：

```bash
cd magnet-crawler
go build -o magnet-crawler.exe .      # Linux/macOS: go build -o magnet-crawler .
./magnet-crawler.exe
```

> `web/` 目录必须存在（`//go:embed web/index.html` 等三条嵌入指令），源码克隆后即可编译。

### 端口与监听

默认监听 `:8900`（即 `0.0.0.0:8900`），本机访问 `http://127.0.0.1:8900`，局域网访问 `http://<本机IP>:8900`。改端口：

```bash
PORT=9000 ./magnet-crawler.exe        # PowerShell: $env:PORT=9000; .\magnet-crawler.exe
```

---

## 2. 页面使用

1. **关键词**：文本框里每行一个（也可用逗号分隔），例如 `abf-399`；留空则用**热榜模式**；
2. **热榜数量**：填 N 即取热榜前 N 个关键词；
3. **每词条数**：`limit` 是**合格结果的目标条数**（1–24），不是原始结果条数；
4. 点「开始抓取」→ 进度区依次出现 `list`（搜结果页）、`detail`（取磁链）阶段，并显示下一次请求的随机等待秒数；
5. 结果表实时追加行：关键词 / 标题 / 文件名 / 列表大小 / 大小 / 日期 / BTIH / 磁力链接；
6. 右上角可「复制全部磁链」「导出 JSON」「导出 CSV」；点「停止」会在当前等待结束后收尾，已抓到的结果保留。

任务结束若有过被过滤的条目，进度区提示会显示 `N 条被过滤（广告 / 小于100MB / 非 mp4）`。

**耗时估算**：每条合格结果 = 1 次列表请求 + 1 次详情请求 ≈ 2 × (3–15) 秒；`limit=2` 通常 20–40 秒，`limit=10` 三分钟起步。这是限速设计的必然结果，不建议调快。

---

## 3. 过滤规则

只保留**同时满足**以下三条的条目，其余不入表格、不入导出：

| 规则 | 判定方式 |
|---|---|
| 只要 `.mp4` | 详情页文件列表中存在以 `.mp4` 结尾的文件；解析不到文件列表时回退看列表页文件名是否含 `.mp4` |
| 单种子 > 100MB | 优先用详情页「文件大小」，缺失时回退列表页显示大小；解析不到大小的条目按保守策略剔除 |
| 剔除广告 | 标题/文件名命中推广域名（`xxx.xyz`、`xxx.com` 等）或话术（手机搜、加群、发布页、最新地址、永久域名、官网、福利、扫码、下载…）即剔除 |

站点把邮箱混淆成的 `[email protected]` 占位**不算广告**，展示前会被清掉，否则会误杀正常种子。

阈值位置：`crawler.go` 中的 `minSeedBytes = 100 << 20`；mp4 判定在 `filterSeed()`；广告词表在 `adWords`。

---

## 4. 配置（环境变量）

| 变量 | 默认 | 作用 |
|---|---|---|
| `PORT` | `8900` | 监听端口 |
| `CI_BASE` | `https://cilisousuo.co` | 上游站点基址 |
| `CI_PROXY` | 空 | 上游代理，例如 `socks5://127.0.0.1:11080`；能直连时留空 |
| `CI_API_KEY` | 空 | 设置后 `/api/v1/*` 必须带 `X-API-Key` 或 `Authorization: Bearer`；空则不校验 |
| `CI_CORS_ORIGINS` | `*` | 允许的来源，逗号分隔；设置后只回显白名单内的 Origin |
| `CI_RATE_LIMIT` | `60` | 每 IP 每分钟请求数（仅 `/api/v1/*`），`0` 关闭 |

Windows 下临时设置（当前窗口有效）：

```bat
set PORT=8900
set CI_API_KEY=换成你自己的随机串
magnet-crawler.exe
```

---

## 5. HTTP 接口

### 5.1 页面接口（无鉴权，供本机前端用）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/`、`/app.js`、`/styles.css` | 内嵌前端资源 |
| POST | `/api/job` | `{"keywords":["abf-399"],"hot":0,"limit":2,"detail":true}` → `201 {"job_id":"..."}`；已有任务在跑 → `409 job_running` |
| POST | `/api/job/{id}/stop` | 当前等待结束后停止 |
| GET | `/api/events` | SSE：`status` / `delay` / `item` / `done`，20 秒心跳 |
| GET | `/api/items` | 最近一次任务的结果 |
| GET | `/api/export?format=json\|csv` | 附件下载（CSV 表头 `keyword,title,filename,list_size,size,date,btih,magnet`，UTF-8 BOM） |

### 5.2 对外接口 `/api/v1`（给其他网站调用）

已开 CORS（预检 `OPTIONS` 返回 `204`），机器可读描述：`GET /api/v1/openapi.json`。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/v1/health` | 返回 `ok`、`job_running`、`auth_required` |
| POST | `/api/v1/jobs` | 异步建任务 → `202` 返回 `job_id`、`poll_url`、`results_url`；已有任务在跑 → `409` |
| GET | `/api/v1/jobs/{id}` | 轮询状态：`state/stage/keyword/done/total/count/note/created_at`；任务保留最近 20 个 |
| GET | `/api/v1/jobs/{id}/results` | 结果数组；id 不存在或过期 → `404 job_not_found` |
| GET | `/api/v1/jobs/{id}/events` | 该任务的 SSE 事件流 |
| GET | `/api/v1/search?q=&limit=&wait=` | **同步**抓取：`limit ≤ 5`，`wait ≤ 120s`（默认 45），超时 `504` 且带已抓部分 |
| GET | `/api/v1/hot?n=10&wait=45` | 热榜关键词（`keywords` 字段） |

结果条目字段：

```json
{
  "keyword": "abf-399",
  "page": "m7Ru",
  "title": "ABF-399",
  "filename": "ABF-399/ABF-399.mp4",
  "list_size": "4.8GB",
  "size": "4.8GB",
  "date": "2026-11-12",
  "btih": "365ffe38326c21d24f9e36b089afd844bb817f78",
  "magnet": "magnet:?xt=urn:btih:365ffe...&dn=..."
}
```

#### 示例：curl 同步查询

```bash
curl -s "http://127.0.0.1:8900/api/v1/search?q=snos-422&limit=1&wait=90" \
  -H "X-API-Key: $CI_API_KEY"
```

#### 示例：JavaScript（推荐异步 + 轮询）

```js
const base = "http://127.0.0.1:8900";
const headers = { "Content-Type": "application/json", "X-API-Key": "<key>" };

const { job_id } = await fetch(`${base}/api/v1/jobs`, {
  method: "POST",
  headers,
  body: JSON.stringify({ keywords: ["snos-422"], limit: 2, detail: true }),
}).then((r) => r.json());

let st;
do {
  await new Promise((r) => setTimeout(r, 3000));
  st = await fetch(`${base}/api/v1/jobs/${job_id}`, { headers }).then((r) => r.json());
} while (st.state === "running");

const { items } = await fetch(`${base}/api/v1/jobs/${job_id}/results`, { headers })
  .then((r) => r.json());
console.log(items.map((i) => [i.filename, i.size, i.magnet]));
```

#### 错误码

| HTTP | `error` | 含义 |
|---|---|---|
| 400 | `bad_request` / `no_keywords` / `bad_limit` / `bad_hot` | 请求体或参数不合法 |
| 401 | `unauthorized` | 未带或带错 API Key |
| 404 | `job_not_found` | 任务 id 不存在或超出最近 20 个 |
| 409 | `job_running` | 已有任务在跑（body 里带当前 `job_id`） |
| 429 | `rate_limited` | 超过每 IP 每分钟配额 |
| 504 | `timeout` | 同步接口等待超时，body 里带已抓部分或已取到的关键词 |

---

## 6. 部署

### 本机 / 局域网

直接跑二进制即可，`0.0.0.0:8900` 局域网可达。Windows 开机自启动可把 `start.bat` 放进「启动」文件夹（`shell:startup`）。

### 公网给其他网站调用

```nginx
location /api/v1/ {
    proxy_pass http://127.0.0.1:8900;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;   # 限流按真实 IP 需要它
}
```

外露前**务必**：`CI_API_KEY` 设为随机串（`openssl rand -hex 24`）、`CI_CORS_ORIGINS` 只放你的站点、`CI_RATE_LIMIT` 按需收紧。没有 key 的服务等于把出口公开给别人刷。

### Linux 服务器常驻（systemd）

```ini
# /etc/systemd/system/magcraw.service
[Unit]
Description=magcraw magnet crawler
After=network-online.target

[Service]
WorkingDirectory=/opt/magcraw
ExecStart=/opt/magcraw/magcraw
Restart=always
Environment=PORT=8900
Environment=CI_API_KEY=替换成你的随机串
Environment=CI_RATE_LIMIT=30

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload && sudo systemctl enable --now magcraw
```

---

## 7. 常见问题

| 现象 | 原因与处理 |
|---|---|
| 浏览器打不开 8900 | 进程没起来：本机双击 `start.bat`；查端口占用 `netstat -ano \| findstr :8900` |
| 抓取一直 `no magnet` / 全被过滤 | 该关键词确实没有合格条目；看进度区 `note` 的具体原因（`ad` / `under 100MB` / `no mp4`） |
| `string` 层面的连接被重置（Linux 上跑） | 目标站对部分地区链路会 `Recv failure`；用 `CI_PROXY=socks5://...` 指定可用出口，或在能直连的机器上跑 |
| 任务跑到一半 `409 job_running` | 单任务串行是刻意设计；等当前任务结束再提交 |
| 同步接口 `504 timeout` | 抓取本质要 20–40 秒；改走 `POST /api/v1/jobs` + 轮询，或把 `wait` 调大 |
| 想放宽过滤 | 改 `minSeedBytes`（阈值）或 `filterSeed()`（扩展名/规则），重新 `go build` |

---

## 8. 目录结构

```
magnet-crawler/
├── main.go           路由、SSE、导出、静态资源嵌入
├── crawler.go         抓取引擎、解析、随机限速、过滤规则
├── api_v1.go          对外 API：CORS / API Key / 限流 / 任务历史 / OpenAPI
├── go.mod
├── web/
│   ├── index.html     页面结构（语义化 id）
│   ├── app.js         EventSource 消费事件、表格渲染、复制与导出
│   └── styles.css     --cs-* 设计令牌 + cs- BEM 组件
├── start.bat / stop.bat / run-service.bat
├── data/              运行时产物（每次任务一个 job-<ts>.json，已在 .gitignore）
└── README.md
```

---

## 9. 版本与提交约定

- 版本遵循[语义化版本](https://semver.org/lang/zh-CN/)，变更见 [CHANGELOG.md](CHANGELOG.md)；
- 提交信息用 Conventional Commits 前缀：`feat:` 新功能、`fix:` 修复、`refactor:` 重构、`docs:` 文档、`chore:` 杂项；
- 发布时打 tag：`git tag v1.0.0 && git push --tags`；
- 二进制与 `data/` 不入库，见 `.gitignore`。

---

## 10. 合规与免责

本工具仅对**第三方公开页面**做字段提取（文件名、大小、磁力链接），本身不包含下载、存储或分发内容的功能。抓取到的磁力链接所属资源的版权与合法性由使用者自行判断并承担相应责任；请遵守目标站点的 `robots.txt` 与当地法律法规，并把限速保持在默认的 3–15 秒，不要用于高频批量采集。

许可：未指定（作者保留权利），如需开源授权请补充 LICENSE 文件。