#!/usr/bin/env bash
# 表格编辑器一键运行脚本
# 用法:
#   ./run.sh              # 默认 8080 端口
#   ./run.sh 9000         # 指定端口
#   ./run.sh --reset-pin  # 忘记锁屏密码时重置（需服务在运行，或直接改 data.json）
# 停止: 前台运行时按 Ctrl+C

set -e
cd "$(dirname "$0")"

APP="table-editor"
SRC="main.go"
PORT="${1:-8080}"

# 0. 忘记密码：重置锁屏密码
if [ "$1" = "--reset-pin" ]; then
  if [ ! -x "$APP" ] || [ "$SRC" -nt "$APP" ]; then
    go build -o "$APP" . || { echo "✗ 编译失败"; exit 1; }
  fi
  ./"$APP" --reset-pin "${2:-8080}"
  exit $?
fi

# 1. 检查 Go 环境
if ! command -v go >/dev/null 2>&1; then
  echo "✗ 未检测到 Go 环境，请先安装 Go: https://go.dev/dl/"
  exit 1
fi

# 2. 需要时编译（二进制不存在或源码更新过）
if [ ! -x "$APP" ] || [ "$SRC" -nt "$APP" ]; then
  echo "⏳ 正在编译..."
  go build -o "$APP" . || { echo "✗ 编译失败"; exit 1; }
  echo "✓ 编译完成"
else
  echo "✓ 已是最新版本，跳过编译"
fi

# 3. 端口被占用时自动顺延，找第一个空闲端口
is_busy() {
  if command -v lsof >/dev/null 2>&1; then
    lsof -nP -iTCP:"$1" -sTCP:LISTEN >/dev/null 2>&1
  else
    (exec 3<>/dev/tcp/127.0.0.1/"$1") 2>/dev/null && exec 3>&- && return 0 || return 1
  fi
}

if is_busy "$PORT"; then
  ORIGIN=$PORT
  for p in $(seq "$PORT" $((PORT + 20))); do
    if ! is_busy "$p"; then PORT=$p; break; fi
  done
  if [ "$PORT" = "$ORIGIN" ]; then
    echo "✗ 端口 $ORIGIN 及后续 20 个端口都被占用，请手动指定: ./run.sh 9000"
    exit 1
  fi
  echo "⚠ 端口 $ORIGIN 已被占用，改用 $PORT"
fi

URL="http://localhost:$PORT"
echo "🚀 启动中: $URL"

# 4. 后台等待服务就绪后打开浏览器（仅 macOS 自动打开）
(
  for _ in $(seq 1 30); do
    if curl -s -o /dev/null "$URL" 2>/dev/null; then
      if [ "$(uname)" = "Darwin" ]; then open "$URL"; fi
      break
    fi
    sleep 0.2
  done
) &

echo "─────────────────────────────────────────"
echo " 地址: $URL"
echo " 数据文件: $(pwd)/data.json"
echo " 停止: Ctrl+C"
echo "─────────────────────────────────────────"

# 5. 前台运行，Ctrl+C 即退出
exec ./"$APP" "$PORT"
