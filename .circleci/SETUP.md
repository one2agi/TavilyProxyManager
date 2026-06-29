# CircleCI 配置说明（极简版）

本目录包含 TavilyProxyManager 的 CircleCI 配置，**只做一件事：构建并推送 Docker 镜像到 GHCR**。

## 🎯 流水线做了什么

| Job | 做什么 | 触发 |
|---|---|---|
| `build-web` | `npm ci` + `npm run build`（含缓存） | 任何 push / PR |
| `build-docker` | 多架构 Docker 构建 → 推送到 GHCR | 需要前置 `build-web` 成功 |

## 📦 触发后的产出

- **push 到 main** → `ghcr.io/one2agi/tavilyproxymanager:latest` 被更新
- **打 `v*` tag**（如 `v1.2.3`）→ 额外生成 `:v1.2.3` tag
- **PR** → 只构建不推送（不会污染 `:latest`）

## 🚀 首次配置（5 步）

### 1. 注册 CircleCI
```
https://circleci.com/signup/ → "Sign up with GitHub"
→ 授权访问你的 GitHub 账号
→ Projects 里找到 one2agi/TavilyProxyManager → "Set Up Project"
→ 选 "Use Existing Config"（因为我们已经写好 .circleci/config.yml）
```

### 2. 创建 GitHub PAT
```
GitHub → Settings → Developer settings → Personal access tokens → Tokens (classic)
→ Generate new token

勾选权限:
  ✅ write:packages   （推镜像到 GHCR）
  ✅ read:packages    （读 GHCR）

复制 token（只显示一次！形如 ghp_xxxxxxxx）
```

### 3. 在 CircleCI 创建 Context
```
CircleCI → Organization Settings → Contexts
→ Create Context
→ 名字: ghcr-push
→ 添加环境变量:
   Name:  GHCR_TOKEN
   Value: ghp_xxxxxxxxxxxxxxxx
```

### 4. 触发流水线
push 到 main 即可（或在 CircleCI UI 手动触发）：
```bash
git commit --allow-empty -m "ci: trigger CircleCI"
git push origin main
```

### 5. 验证
去 https://github.com/one2agi/TavilyProxyManager/pkgs/container/tavilyproxymanager
看 `:latest` tag 的 `Last updated` 时间是不是刚才。

## 🆘 常见问题

**Q: 报 `dial tcp: lookup ghcr.io` 超时？**
A: 检查 GHCR_TOKEN 是否正确，且有 `write:packages` 权限。

**Q: 报 `Cannot find ... in the orb registry`？**
A: 本配置**不使用任何 orb**，纯原生命令。如果看到 orb 错误，说明你看的是旧版本，重新拉一下代码。

**Q: 报 `Permission denied` 在 apt 步骤？**
A: 本配置**不使用 apt**，所有工具都直接下载二进制。如果看到 apt 错误，也是旧版本。

**Q: 推送 401 Unauthorized？**
A: Context 没正确附加。检查 `config.yml` 里 `context: ghcr-push` 是否存在。

**Q: 推送成功但本地 docker pull 失败？**
A: 镜像可能是私有。访问 https://github.com/one2agi?tab=packages 把它改成 Public。

## 📝 与 GitHub Actions 的对比

| 维度 | GitHub Actions | CircleCI（当前） |
|---|---|---|
| Docker 镜像构建 | ✅ | ✅ |
| 多架构 (amd64+arm64) | ✅ | ✅ |
| Linux 二进制 | ✅（6 平台）| ❌ 暂不支持 |
| Release 发布 | ✅ | ❌ 暂不支持 |
| Windows/macOS 二进制 | ✅ | ❌ 需付费 plan |
| 费用 | 你的账户被锁 | 免费 30,000 分钟/月 |