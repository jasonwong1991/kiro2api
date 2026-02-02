#!/bin/sh
set -eu

# 目标：在不要求用户手动处理宿主机文件的前提下，自动处理 Docker bind mount
# "文件不存在会被创建为目录"的坑，确保 Token 导入后可以持久化。
#
# 典型问题：
#   -v ./tokens.json:/app/tokens.json
# 当宿主机 ./tokens.json 不存在时，Docker 会创建同名目录，导致应用写入时报：
#   open /app/tokens.json: is a directory
#
# 自愈策略：
# 1) 若 KIRO_AUTH_TOKEN 指向目录，则自动改写为 <dir>/tokens.json
# 2) 若文件不存在，则创建空数组文件 []，便于后续 WebUI 导入落盘
# 3) 权限修复由 docker-init.sh（root 权限）完成

CONFIG_PATH="${KIRO_AUTH_TOKEN:-/app/tokens.json}"

# JSON 字符串配置：不做路径自愈，直接交给程序解析
case "$CONFIG_PATH" in
	\[*|\{*)
		exec ./kiro2api "$@"
		;;
esac

# 目录场景（含 tokens.json 被挂载成目录）
if [ -d "$CONFIG_PATH" ]; then
	FIXED_PATH="$CONFIG_PATH/tokens.json"
	echo "[entrypoint] 检测到 KIRO_AUTH_TOKEN=$CONFIG_PATH 是目录，自动改为: $FIXED_PATH"
	CONFIG_PATH="$FIXED_PATH"
	export KIRO_AUTH_TOKEN="$CONFIG_PATH"
fi

# 确保父目录存在
PARENT_DIR="$(dirname "$CONFIG_PATH")"
if [ ! -d "$PARENT_DIR" ]; then
	mkdir -p "$PARENT_DIR" 2>/dev/null || true
fi

# 文件不存在则创建空配置
if [ ! -e "$CONFIG_PATH" ]; then
	echo "[entrypoint] 创建空 tokens 配置文件: $CONFIG_PATH"
	echo '[]' >"$CONFIG_PATH" 2>/dev/null || true
	chmod 600 "$CONFIG_PATH" 2>/dev/null || true
fi

exec ./kiro2api "$@"

