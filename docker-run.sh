#!/usr/bin/env bash
# 表格编辑器 Docker 一键部署脚本
#
# 用法:
#   ./docker-run.sh              # 构建镜像并启动容器（默认 8080 端口）
#   ./docker-run.sh 9000         # 指定宿主机端口
#   ./docker-run.sh stop         # 停止并移除容器（数据保留在 ./data）
#   ./docker-run.sh logs         # 实时跟踪日志
#   ./docker-run.sh status       # 查看容器状态
#   ./docker-run.sh reset-pin    # 重置锁屏密码
#   ./docker-run.sh rebuild      # 强制重新构建镜像后重启

set -euo pipefail
cd "$(dirname "$0")"

IMAGE="table-editor:latest"
NAME="table-editor"
CONTAINER_PORT=8080
DATA_DIR="$(pwd)/data"
STAMP=".docker-build.stamp"

CMD="${1:-start}"
PORT="${2:-8080}"
# 直接传数字时视为端口号：./docker-run.sh 9000
if [[ "$CMD" =~ ^[0-9]+$ ]]; then
  PORT="$CMD"
  CMD="start"
fi

# ---------- 工具函数 ----------

# 找 docker 命令：优先 PATH，其次 Docker Desktop 的固定路径
find_docker() {
  if command -v docker >/dev/null 2>&1; then
    DOCKER="docker"
  elif [ -x "/Applications/Docker.app/Contents/Resources/bin/docker" ]; then
    DOCKER="/Applications/Docker.app/Contents/Resources/bin/docker"
  else
    echo "✗ 未找到 docker 命令。"
    echo "  macOS 请安装并启动 Docker Desktop: https://www.docker.com/products/docker-desktop"
    echo "  Linux 请安装 docker engine: https://docs.docker.com/engine/install/"
    exit 1
  fi

  if ! $DOCKER info >/dev/null 2>&1; then
    echo "✗ Docker 守护进程没在运行，请先启动 Docker Desktop 后重试。"
    exit 1
  fi
}

sha256() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 | awk '{print $1}'
  else
    sha256sum | awk '{print $1}'
  fi
}

source_hash() {
  cat main.go go.mod Dockerfile | sha256
}

image_exists() {
  $DOCKER image inspect "$IMAGE" >/dev/null 2>&1
}

# 镜像不存在，或源码/Dockerfile 改过 → 需要构建
need_build() {
  image_exists || return 0
  [ "$(source_hash)" != "$(cat "$STAMP" 2>/dev/null || echo none)" ]
}

build_image() {
  if ! need_build; then
    echo "✓ 镜像已是最新版本，跳过构建"
    return 0
  fi
  echo "⏳ 正在构建镜像（多阶段构建，首次约 30 秒）..."
  if ! $DOCKER build -t "$IMAGE" . ; then
    echo "⚠ 常规构建失败，改用传统构建器重试（本机 buildx 插件异常时的降级方案）..."
    DOCKER_BUILDKIT=0 $DOCKER build -t "$IMAGE" .
  fi
  source_hash > "$STAMP"
  echo "✓ 镜像构建完成: $IMAGE"
}

# 等容器里的服务能响应
wait_ready() {
  for _ in $(seq 1 40); do
    if curl -fsS -o /dev/null "http://localhost:$PORT/" 2>/dev/null; then
      return 0
    fi
    sleep 0.25
  done
  return 1
}

open_browser() {
  if [ "$(uname)" = "Darwin" ]; then
    open "$1" >/dev/null 2>&1 || true
  fi
}

# ---------- 子命令 ----------

case "$CMD" in
  stop|down)
    find_docker
    $DOCKER rm -f "$NAME" >/dev/null 2>&1 && echo "✓ 容器已停止并移除" || echo "✓ 没有正在运行的容器"
    echo "  数据保留在: $DATA_DIR"
    exit 0
    ;;

  logs)
    find_docker
    exec $DOCKER logs -f "$NAME"
    ;;

  status)
    find_docker
    echo "镜像:"
    image_exists && $DOCKER images "$IMAGE" --format '  {{.Repository}}:{{.Tag}}  {{.Size}}  构建于 {{.CreatedSince}}' || echo "  未构建"
    echo "容器:"
    $DOCKER ps -a --filter "name=^/${NAME}$" --format '  {{.Names}}  {{.Status}}  {{.Ports}}' | grep . || echo "  不存在"
    echo "数据目录: $DATA_DIR"
    [ -f "$DATA_DIR/data.json" ] && echo "  data.json 大小: $(wc -c < "$DATA_DIR/data.json") 字节" || echo "  data.json 尚未生成"
    exit 0
    ;;

  reset-pin)
    find_docker
    if ! $DOCKER ps --filter "name=^/${NAME}$" --format '{{.Names}}' | grep -q .; then
      echo "✗ 容器没在运行。请先 ./docker-run.sh 启动，或删除 $DATA_DIR/data.json 里锁屏密码"
      exit 1
    fi
    $DOCKER exec "$NAME" table-editor --reset-pin "$CONTAINER_PORT"
    echo "✓ 回到页面点「解锁」即可进入"
    exit 0
    ;;

  rebuild)
    FORCE_BUILD=1
    ;;
esac

# ---------- 启动流程 ----------

find_docker

if [ "${FORCE_BUILD:-0}" = "1" ]; then
  rm -f "$STAMP"
fi
build_image

# 数据目录：首次自动把项目根下的 data.json 迁进去，避免容器看不到旧数据
mkdir -p "$DATA_DIR"
if [ -f data.json ] && [ ! -f "$DATA_DIR/data.json" ]; then
  cp data.json "$DATA_DIR/data.json"
  echo "✓ 已把现有 data.json 复制到 $DATA_DIR/"
fi

# 清掉同名旧容器，按新端口重启
$DOCKER rm -f "$NAME" >/dev/null 2>&1 || true

echo "🚀 启动容器: 宿主机 $PORT → 容器 $CONTAINER_PORT"
$DOCKER run -d \
  --name "$NAME" \
  -p "$PORT:$CONTAINER_PORT" \
  -v "$DATA_DIR:/app/data" \
  -e TZ=Asia/Shanghai \
  --restart unless-stopped \
  "$IMAGE" >/dev/null

if wait_ready; then
  URL="http://localhost:$PORT"
  open_browser "$URL"
  echo "─────────────────────────────────────────"
  echo " 地址: $URL"
  echo " 数据目录: $DATA_DIR"
  echo " 看日志: ./docker-run.sh logs"
  echo " 停止:   ./docker-run.sh stop"
  echo "─────────────────────────────────────────"
else
  echo "✗ 容器已创建但服务没起来，日志如下:"
  $DOCKER logs --tail 30 "$NAME" || true
  exit 1
fi
