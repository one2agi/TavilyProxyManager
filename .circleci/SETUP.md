# CircleCI 配置说明

本目录包含 TavilyProxyManager 的 CircleCI 配置文件，用于替代因账单问题暂停的 GitHub Actions。

## 📦 文件清单

| 文件 | 作用 |
|---|---|
| `config.yml` | CircleCI 主配置（前端构建 + Docker 镜像 + Linux 二进制 + Release） |
| `SETUP.md` | 本说明文档 |

## 🚀 首次配置（5 步）

### 1. 注册 CircleCI
```
打开 https://circleci.com/signup/
→ 点 "Sign up with GitHub"
→ 授权 CircleCI 访问你的 GitHub 账号
→ 在 Projects 列表找到 one2agi/TavilyProxyManager
→ 点 "Set Up Project"
```

### 2. 创建 GitHub Personal Access Token（用于推 GHCR）
```
GitHub → Settings → Developer settings → Personal access tokens → Tokens (classic)
→ Generate new token

勾选权限:
  ✅ write:packages    （推 Docker 镜像到 GHCR）
  ✅ read:packages     （读取 GHCR）
  ✅ repo              （发布 Release）

复制生成的 token（形如 ghp_xxxxxxxx）—— 只显示一次！
```

### 3. 在 CircleCI 创建 Context（存放密钥）
```
CircleCI → Organization Settings → Contexts
→ Create Context
→ 名字: ghcr-push
→ 添加环境变量:
   Name:  GHCR_TOKEN
   Value: ghp_xxxxxxxxxxxxxxxx   ← 粘贴刚才的 token
```

### 4. 推送配置到 GitHub
```bash
git add .circleci/
git commit -m "ci: add CircleCI configuration"
git push origin main
```

### 5. 验证
- 去 CircleCI Pipelines 页面，能看到 `build-web` 和 `build-docker` 任务跑起来
- 跑通后去 `https://github.com/one2agi/TavilyProxyManager/pkgs/container/tavilyproxymanager` 看镜像有没有推送成功

## 🎯 触发条件

| 触发方式 | 跑什么 |
|---|---|
| 任意 push / PR | `build-web` + `build-docker`（构建镜像但不发布） |
| 推 `v*` tag（如 `v1.2.3`） | `build-web` + `build-linux-binaries` + `build-docker` + `publish-release` |
| 手动触发 | CircleCI UI → 选 workflow → Start |

## ⚠️ 已知限制

| 功能 | 状态 | 原因 |
|---|---|---|
| Docker 镜像构建（linux/amd64 + arm64） | ✅ 支持 | CircleCI 免费版支持 Docker executor |
| Linux 二进制（amd64 + arm64） | ✅ 支持 | CircleCI 免费版支持 Linux executor |
| **Windows 二进制** | ❌ 不支持 | 需要付费 plan |
| **macOS 二进制** | ❌ 不支持 | 需要付费 plan |

如果需要 Windows/Mac 二进制，请保留原 GitHub Actions workflow 或使用 self-hosted runner。

## 🆘 常见问题

**Q: 报 `dial tcp: lookup ghcr.io` 超时？**
A: 检查 GHCR_TOKEN 是否正确，且有 `write:packages` 权限。

**Q: Docker 推送报 401 Unauthorized？**
A: Context 没有正确附加到 job 上。检查 `config.yml` 里 `context: ghcr-push` 是否写对。

**Q: 想换成 self-hosted runner，怎么改？**
A: 把 `docker:` executor 换成 `machine: image: ubuntu-2204:2024.01.2`，并去掉 `setup_remote_docker`。