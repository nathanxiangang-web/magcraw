# magcraw — 磁链抓取面板

单二进制 Go 服务 + 原生前端：从一个磁力索引站抓取**文件名、大小、磁力链接**，内置「只留 mp4 / 单种子 >100MB / 去广告」筛选，并对外提供带 **CORS + API Key + 限流**的 HTTP 接口，可直接给其他网站调用。

- **拟人化抓取**：串行执行，每次上游请求前随机等待 **3–15 秒**，UA 轮换，不并发轰炸目标站
- **实时反馈**：页面通过 SSE 显示阶段、当前关键词、本轮随机延迟、已抓条数
- **开箱即用**：零第三方依赖，`go build` 出一个 exe，前端用 `//go:embed` 打进二进制
- **可用性兜底**：列表页就能判定的小种子/广告不会再去请求详情页，省掉一次 3–15 秒等待

---

## 1. 快速开始（Linux）

需要 Go 1.22+（只用到标准库，无 `go.sum`）。产物是**单个静态二进制**，不依赖 libc 与运行时。

### 方式一：一键部署到 systemd（推荐）

```bash
git clone https://github.com/nathanxiangang-web/magcraw.git
cd magcraw
go build -trimpath -ldflags "-s -w" -o magcraw .
sudo ./install.sh
```

`install.sh` 做的事：二进制装到 `/opt/magcraw/magcraw`、建 `/opt/magcraw/data`（属 `nobody`，唯一可写目录）、写 `/etc/systemd/system/magcraw.service`、`systemctl enable --now`。也可以指定端口与安装目录：

```bash
sudo PORT=9000 PREFIX=/srv/magcraw ./install.sh
sudo ./install.sh ./magcraw-linux-amd64        # 用现成的发布二进制，不现场编译
```

日常运维：

```bash
systemctl status magcraw          # 状态
journalctl -u magcraw -f          # 日志
sudo systemctl restart magcraw    # 重启（改过 unit 先 daemon-reload）
sudo systemctl stop magcraw       # 停止
```

### 方式二：不用 systemd，脚本后台运行

```bash
go build -o magcraw .
./start.sh      # 后台启动，PID 写 magcraw.pid、日志写 magcraw.log，自带健康检查
./stop.sh       # 停止
```

### 方式三：Docker

```bash
docker build -t magcraw .
docker run -d --name magcraw -p 8900:8900 \
  -e CI_API_KEY="$(openssl rand -hex 24)" \
  -v magcraw-data:/app/data \
  magcraw
```

镜像为多阶段构建（最终层 alpine + 非 root 用户），`/app/data` 建议挂卷持久化任务快照。

### 方式四：前台直接跑

```bash
go build -o magcraw . && PORT=8900 ./magcraw
```

### 端口与监听

默认监听 `:8900`（即 `0.0.0.0:8900`）：本机 `http://127.0.0.1:8900`，局域网/公网 `http://<服务器IP>:8900`。改端口：

```bash
PORT=9000 ./magcraw        # systemd 安装的改 unit 里的 Environment=PORT=
```

> `web/` 目录必须存在（`//go:embed web/index.html` 等三条指令），克隆后直接 `go build` 即可。

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

### 6.1 Linux（默认路径）

见 §1 的方式一 / 方式二。要点：

- `install.sh` 生成的 unit 使用 `User=nobody` + `ProtectSystem=strict` + `ReadWritePaths=/opt/magcraw/data`，除 `data/` 外全部只读；
- 端口、上游、代理、Key 都通过 unit 的 `Environment=` 配置，改完执行 `sudo systemctl daemon-reload && sudo systemctl restart magcraw`；
- 抓取是串行 + 随机延迟，**单实例即可**，不要为提速起多副本（会互相 `409`，也会加重上游负载）。

### 6.2 反向代理 + HTTPS（给外部网站调用）

```nginx
server {
    listen 443 ssl http2;
    server_name crawler.example.com;

    location / {
        proxy_pass http://127.0.0.1:8900;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    }

    location /api/v1/ {
        proxy_pass http://127.0.0.1:8900;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_buffering off;        # SSE 与长轮询必须关闭缓冲
        proxy_read_timeout 130s;    # 同步接口 wait 上限 120s
    }
}
```

外露前**务必**：`CI_API_KEY` 设为随机串（`openssl rand -hex 24`）、`CI_CORS_ORIGINS` 只放自己的站点、`CI_RATE_LIMIT` 收紧。没有 key 的服务等于把出口公开给别人刷。

### 6.3 Windows（可选，兼容保留）

```bat
cd magnet-crawler
start.bat            :: 或直接双击 magnet-crawler.exe
stop.bat
```

Windows 产物名为 `magnet-crawler.exe`（`go build -o magnet-crawler.exe .`）；开机自启把 `start.bat` 的快捷方式放进 `shell:startup`。

---

## 7. 常见问题

| 现象 | 原因与处理 |
|---|---|
| 打不开 8900 | 服务没起来：`systemctl status magcraw`、`journalctl -u magcraw -n 50`；查端口 `ss -ltnp \| grep 8900` |
| 抓取一直 `no magnet` / 全被过滤 | 该关键词确实没有合格条目；看进度区 `note` 的具体原因（`ad` / `under 100MB` / `no mp4`） |
| `string` 层面的连接被重置（Linux 上跑） | 目标站对部分地区链路会 `Recv failure`；用 `CI_PROXY=socks5://...` 指定可用出口，或在能直连的机器上跑 |
| 任务跑到一半 `409 job_running` | 单任务串行是刻意设计；等当前任务结束再提交 |
| 同步接口 `504 timeout` | 抓取本质要 20–40 秒；改走 `POST /api/v1/jobs` + 轮询，或把 `wait` 调大 |
| 想放宽过滤 | 改 `minSeedBytes`（阈值）或 `filterSeed()`（扩展名/规则），重新 `go build` |

---

## 8. 目录结构

```
magcraw/                       # 仓库根即项目根
├── main.go                    路由、SSE、导出、静态资源嵌入
├── crawler.go                 抓取引擎、解析、随机限速、过滤规则
├── api_v1.go                  对外 API：CORS / API Key / 限流 / 任务历史 / OpenAPI
├── go.mod
├── web/
│   ├── index.html             页面结构（语义化 id）
│   ├── app.js                 EventSource 消费事件、表格渲染、复制与导出
│   └── styles.css             --cs-* 设计令牌 + cs- BEM 组件
├── install.sh                 Linux 一键部署（systemd，装到 /opt/magcraw）
├── start.sh / stop.sh         无 systemd 时的后台启停（PID + 健康检查）
├── Dockerfile / .dockerignore 多阶段构建镜像（非 root 运行）
├── start.bat / stop.bat / run-service.bat   Windows 可选
├── data/                      运行时产物（每次任务一个 job-<ts>.json，已 gitignore）
└── README.md / CHANGELOG.md
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