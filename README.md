# Tavily 代理池 & MCP 多端点

简体中文 | [English](./README_EN.md)

一个为多 AI Agent 提供共享 Tavily Key 池的反向代理 —— **部署一次,多个 agent 共享 N 把 Tavily Key,自动轮换 + 故障转移**。

---

## ✨ 核心功能

- **6 个 MCP 工具**(HTTP MCP 端点,适配 Claude Code / OpenClaw / Cursor / Cline 等所有 MCP 客户端):
  - `tavily-search` 实时 Web 搜索
  - `tavily-extract` 从 URL 提取清洁内容
  - `tavily-crawl` 整站爬取
  - `tavily-map` 网站结构地图
  - `tavily-research` 深度研究报告(异步,内部 polling)
  - `tavily-usage` 配额用量查询(本地聚合,不消耗 credits)
- **透明代理**:完整转发至 `https://api.tavily.com`,所有路径和方法。
- **Master Key 鉴权**:客户端通过 `Authorization: Bearer <MasterKey>` 访问。
- **智能 Key 池**:
  - 优先使用剩余额度最高的 Key。
  - 同额度随机打散,防止频率限制。
  - 遇到 `401` / `429` / `432` / `433` 自动 failover 到下一把 Key。
- **可视化管理面板**(Vite + Vue 3 + Naive UI):
  - Key 管理、用量统计、请求日志、自动化任务(月度重置 + 日志清理)。

![仪表盘](docs/screenshots/dashboard.png)
- **搜索结果缓存**(SQLite):
  - 对 `POST /search` 响应按 `query + search_depth + topic + max_results + include/exclude_domains` 6 字段 SHA256 缓存。
  - 命中后直接读 DB,**0 credit,响应 ~20ms(对比真实 Tavily ~1-2s,加速 50-100x)**。
  - 适合多 agent 共享场景:同一 query 第二次起自动命中,大幅省 credits。

![搜索缓存设置](docs/screenshots/cache-settings.png)
- **Go 单文件二进制**,Docker 部署即开即用。

---

## 🌟 推荐使用方式:多 Agent 共享 Key 池

这是本项目**最核心的场景** —— 部署一个 proxy 实例,多个 AI Agent 共享同一池 Tavily Key。

```
                  ┌─────────────────────────┐
                  │  TavilyProxyManager     │
                  │  https://your-host/mcp  │
                  │                         │
   ┌──────┐       │  ┌─────────────────┐    │      ┌──────────────┐
   │Agent1│──┐    │  │   Key 池        │    │      │              │
   └──────┘  │    │  │  ┌──────┐       │    │      │              │
   ┌──────┐  ├────┼─►│  │Key A │─►─┐   │    │      │  api.tavily  │
   │Agent2│──┤    │  │  ├──────┤   ├►──┼────┼─────►│     .com      │
   └──────┘  │    │  │  │Key B │─►─┤   │    │      │              │
   ┌──────┐  │    │  │  ├──────┤   ├►──┘    │      │              │
   │Agent3│──┘    │  │  │Key C │─►─┘        │      │              │
   └──────┘       │  │  └──────┘            │      └──────────────┘
                  │  └─────────────────┘    │
   ┌──────┐       │                         │
   │Agent4│───────┤  单一 Master Key        │
   └──────┘       │  自动轮换 + failover    │
   ┌──────┐       │                         │
   │ Agent…│──────┤  (N 个 agent 并发)      │
   └──────┘       └─────────────────────────┘
```

**好处**:
- ✅ 每个 Agent **不需要自己的 Tavily Key**(避免每个 Agent 配 key 的麻烦)
- ✅ 1 把 Key 失败/限流时自动换下一把(其他 Agent 不受影响)
- ✅ N 把 Key 的配额**在所有 Agent 间公平共享**(按 quota 剩余动态调度)
- ✅ 单一鉴权点:改 Master Key 一次,所有 Agent 重新接入即可

**接入示例(Claude Code)**:
```bash
claude mcp add --transport http tavily-pool \
  https://your-host/mcp \
  --header "Authorization: Bearer <MASTER_KEY>"
```

**接入示例(OpenClaw)**:
```bash
openclaw mcp set tavily-pool '{"url":"https://your-host/mcp","transport":"streamable-http","headers":{"Authorization":"Bearer <MASTER_KEY>"},"timeout":300}'
```

---

## 🔧 特有功能(本 Fork 改进)

相比上游 [xuncv/TavilyProxyManager](https://github.com/xuncv/TavilyProxyManager),本 fork 新增/修复:

| 改进 | 说明 |
|---|---|
| **➕ `tavily-research` 工具** | 第 6 个 MCP 工具,深度研究报告(异步,内部 2s 轮询,5min 最多等待) |
| **🔒 容器绑定 `127.0.0.1:8080`** | 默认只监听回环,避免裸奔公网(必须配合反代) |
| **🔒 Master Key 不打明文** | 日志中不输出明文 key,改用提示用户去 `/api/settings/master-key` 拿 |
| **🔒 移除 `register/` 目录** | 上游含批量注册 Tavily Key 的脚本(违反 Tavily TOS),本 fork 完整删除 |
| **🛠 Schema 跟 Tavily 真实 API 对齐** | 6 个工具的 `inputSchema` enum 全部按 Tavily API 实测行为修正(如 `output_length: [short, standard, long]`、`citation_format: [numbered, mla, apa, chicago]`、`chunks_per_source: oneOf[1-5, "auto"]` 等) |
| **🐛 Candidates 随机种子修复** | 用 `math/rand/v2.Shuffle`(并发安全,自动种子)替代有缺陷的 `time-based` 种子,防止并发场景下 shuffle 模式重复 |
| **🐛 Update() 不再强制 `IsActive=false`** | 修复合法 partial update(如改 quota、alias)被错误地"软停用" |
| **🐛 Auto-sync 并发死代码** | 修复 `concurrency := 1` 硬编码,改为从 `SettingAutoSyncConcurrency` 读取,可在管理面板调整 |
| **🐛 Research 任务 key 绑定** | Tavily research task 是 per-key 隔离的(POST 用 KEY_A 创建的 task 只能用 KEY_A 查到)。本 fork 在 `addResearchTool` 中把 POST 用过的 Key ID 透传给后续 poll,避免永远 404 |

---

## 🚀 快速部署(Docker)

### 1. 使用 Docker Compose(推荐)

创建 `docker-compose.yml`:

```yaml
services:
  tavily-proxy:
    image: ghcr.io/one2agi/tavilyproxymanager:latest
    container_name: tavily-proxy
    # ⚠️ 安全:只绑回环,必须配合 nginx/Caddy 反代 + HTTPS
    ports:
      - "127.0.0.1:8080:8080"
    environment:
      - LISTEN_ADDR=:8080
      - DATABASE_PATH=/app/data/proxy.db
      - TAVILY_BASE_URL=https://api.tavily.com
      # ⚠️ 至少 100s(深度研究任务需要)
      - UPSTREAM_TIMEOUT=100s
    volumes:
      - ./data:/app/data
      - /var/log/tavily-proxy:/var/log/tavily-proxy
    restart: unless-stopped
```

启动:
```bash
docker compose up -d
```

### 2. 本地构建(自行修改后)

```bash
git clone https://github.com/one2agi/TavilyProxyManager.git
cd TavilyProxyManager
docker build -t tavily-proxy:custom .
docker compose up -d
```

---

## 🔑 首次运行:获取 Master Key

服务**首次启动**时自动生成随机 Master Key(用于登录管理面板和调用 API)。

**本 fork 改进**: Master Key **不再出现在启动日志明文**。

获取方式(任选其一):

**方式 1:数据库查询**
```bash
sqlite3 ./data/proxy.db "SELECT value FROM settings WHERE key='master_key'"
```

**方式 2:从管理面板获取**(首次登录后)
访问 `http://localhost:8080`,用初始临时凭证登录后,在 "设置" 页面查看 Master Key。

> ⚠️ **安全提示**:请将 Master Key 保存在 1Password/Bitwarden 等密码管理器,不要 commit 到 git。

---

## 🛠 本地开发

```bash
# 后端
go run ./server

# 前端(另一终端)
cd web && npm install && npm run dev
```

构建二进制:
- Windows: `.\scripts\build_all.ps1`
- Linux/macOS: `./scripts/build_all.sh`

---

## 📖 使用指南

### REST API 代理

调用方式与 Tavily 官方 API 完全一致,只需替换 base URL + 用 Master Key:

```bash
curl -X POST "http://localhost:8080/search" \
  -H "Authorization: Bearer <MASTER_KEY>" \
  -H "Content-Type: application/json" \
  -d '{"query": "最新 AI 技术趋势", "search_depth": "basic", "max_results": 5}'
```

### MCP 6 个工具

| 工具 | 能力 | 关键参数 |
|---|---|---|
| `tavily-search` | 实时 Web 搜索(**支持缓存**) | `query`, `search_depth`, `topic`, `max_results`, `time_range`, `chunks_per_source` |
| `tavily-extract` | 提取 URL 内容 | `urls[]`, `extract_depth`, `format` (markdown/text/html_tags), `query` (rerank) |
| `tavily-crawl` | 整站爬取 | `url`, `max_depth` (1-5), `max_breadth`, `limit` (≤1000) |
| `tavily-map` | 网站结构地图 | `url`, `max_depth`, `max_breadth`, `limit` (≤1000) |
| `tavily-research` | 深度研究(异步) | `input`, `model` (mini/pro/auto), `output_length`, `citation_format` |
| `tavily-usage` | 配额用量查询 | (无参数,本地聚合) |

### MCP 接入示例

**Claude Code:**
```bash
claude mcp add --transport http tavily-pool https://your-host/mcp \
  --header "Authorization: Bearer <MASTER_KEY>"
```

**Cursor** (`~/.cursor/mcp.json`):
```json
{
  "mcpServers": {
    "tavily-pool": {
      "url": "https://your-host/mcp",
      "headers": { "Authorization": "Bearer <MASTER_KEY>" }
    }
  }
}
```

**OpenClaw** (`openclaw mcp set`):
```bash
openclaw mcp set tavily-pool '{"url":"https://your-host/mcp","transport":"streamable-http","headers":{"Authorization":"Bearer <MASTER_KEY>"},"timeout":300}'
```

---

## ⚙️ 配置项(环境变量)

| 变量名 | 说明 | 默认值 |
|---|---|---|
| `LISTEN_ADDR` | 服务监听地址 | `:8080` |
| `DATABASE_PATH` | SQLite 数据库路径 | `/app/data/proxy.db` |
| `TAVILY_BASE_URL` | 上游 Tavily API 地址 | `https://api.tavily.com` |
| `UPSTREAM_TIMEOUT` | 上游请求超时(**研究任务建议 ≥ 100s**) | `100s` |
| `MCP_STATELESS` | MCP 无状态模式(避免 `session not found`) | `true` |
| `MCP_SESSION_TTL` | MCP 会话空闲超时 | `10m` |
| `LOG_DIR` | 文件日志目录(留空 = 只 stdout) | (空) |
| `cache_enabled` | 启用搜索响应缓存(管理面板"设置"页可改) | `true` |
| `cache_ttl_seconds` | 缓存条目过期时间(秒) | `43200` (12h) |

---

## 🔒 安全建议(生产部署)

本 fork 的默认配置已加固,但生产部署务必:

1. **容器只绑 `127.0.0.1`**,不暴露公网
2. **用 nginx / Caddy 反代 + HTTPS** + Let's Encrypt 证书
3. **反代必须透传 `Mcp-Session-Id`**(用有状态 MCP 时)
4. **Master Key 保存在密码管理器**,定期轮换
5. **设置 fail2ban** 防爆破
6. **Cloudflare 代理** 防 DDoS(可选)
7. **Master Key 严禁出现在 commit 历史 / 截图 / 文档**

---

## 🆚 与上游对比

| 特性 | 上游 xuncv | 本 fork |
|---|---|---|
| 5 个 MCP tool | ✅ | ✅ |
| `tavily-research` 工具 | ❌ | ✅ |
| Schema 跟 Tavily API 对齐 | ❌(部分字段错) | ✅(逐个实测) |
| Candidates 随机种子 | ❌(time-based) | ✅(math/rand/v2) |
| Update() 不强制停用 | ❌ | ✅ |
| Auto-sync 并发可配 | ❌(死代码 1) | ✅ |
| Research key 绑定 | ❌(per-key 隔离 404) | ✅ |
| 容器默认绑回环 | ❌ | ✅ |
| Master Key 不打明文 | ❌ | ✅ |
| `register/` 批量注册 | ⚠️(TOS 风险) | ✅ 已删除 |

---

## 📄 协议

MIT License. 基于 [xuncv/TavilyProxyManager](https://github.com/xuncv/TavilyProxyManager) fork。
