#!/bin/bash
# ----------------------------------------------------------------------------
# 本地构建并推送 Docker 镜像到 GitHub Container Registry
#
# 用法：
#   ./scripts/build-and-push.sh           # 构建并推 :latest
#   ./scripts/build-and-push.sh v1.2.3    # 构建并推 :latest + :v1.2.3
#
# 前置条件：
#   1. Docker 已安装，且支持 buildx
#   2. 已用 GitHub PAT 登录：docker login ghcr.io -u one2agi
# ----------------------------------------------------------------------------
set -euo pipefail

REGISTRY="ghcr.io"
IMAGE="${REGISTRY}/one2agi/tavilyproxymanager"
TAG="${1:-latest}"

echo "🏗️  准备构建: ${IMAGE}"
echo "📌 Tag: ${TAG}"
echo ""

# 创建 buildx 构建器（如果还没有）
if ! docker buildx ls | grep -q multiarch; then
  echo "🔧 创建 buildx 构建器..."
  docker buildx create --name multiarch --driver docker-container --use
fi

# 构建参数
PLATFORMS="linux/amd64,linux/arm64"
TAGS=("-t ${IMAGE}:latest" "-t ${IMAGE}:${TAG}" "-t ${IMAGE}:$(git rev-parse --short HEAD)")

# 拼接 tag 参数
TAG_ARGS=""
for t in "${TAGS[@]}"; do
  TAG_ARGS="${TAG_ARGS} ${t}"
done

echo "🚀 开始构建多架构镜像..."
docker buildx build \
  --platform ${PLATFORMS} \
  ${TAG_ARGS} \
  --push \
  .

echo ""
echo "✅ 完成！镜像已推送到 GHCR"
echo ""
echo "📦 拉取方式："
echo "   docker pull ${IMAGE}:${TAG}"
echo "   docker run --rm -p 8080:8080 ${IMAGE}:${TAG}"
echo ""
echo "🔗 查看镜像："
echo "   https://github.com/one2agi/TavilyProxyManager/pkgs/container/tavilyproxymanager"