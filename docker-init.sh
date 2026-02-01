#!/bin/sh
# Docker 初始化脚本（以 root 身份运行）
# 目标：修复挂载文件的权限问题，然后切换到非 root 用户执行主程序

set -eu

CONFIG_PATH="${KIRO_AUTH_TOKEN:-/app/tokens.json}"

# JSON 字符串配置：跳过权限修复
case "$CONFIG_PATH" in
	\[*|\{*)
		exec su-exec appuser /docker-entrypoint.sh "$@"
		;;
esac

# 目录场景（含 tokens.json 被挂载成目录）
if [ -d "$CONFIG_PATH" ]; then
	FIXED_PATH="$CONFIG_PATH/tokens.json"
	echo "[init] 检测到 KIRO_AUTH_TOKEN=$CONFIG_PATH 是目录，自动改为: $FIXED_PATH"
	CONFIG_PATH="$FIXED_PATH"
	export KIRO_AUTH_TOKEN="$CONFIG_PATH"
fi

# 确保父目录存在并设置权限
PARENT_DIR="$(dirname "$CONFIG_PATH")"
if [ ! -d "$PARENT_DIR" ]; then
	mkdir -p "$PARENT_DIR" 2>/dev/null || true
fi
chown -R appuser:appgroup "$PARENT_DIR" 2>/dev/null || true

# 文件不存在则创建空配置
if [ ! -e "$CONFIG_PATH" ]; then
	echo "[init] 创建空 tokens 配置文件: $CONFIG_PATH"
	echo '[]' >"$CONFIG_PATH"
	chmod 600 "$CONFIG_PATH"
	chown appuser:appgroup "$CONFIG_PATH"
else
	# 文件存在，修复权限
	echo "[init] 修复配置文件权限: $CONFIG_PATH"
	chmod 600 "$CONFIG_PATH" 2>/dev/null || true
	chown appuser:appgroup "$CONFIG_PATH" 2>/dev/null || true

	# 测试文件是否可写（以 appuser 身份）
	if ! su-exec appuser test -w "$CONFIG_PATH"; then
		echo "[init] 警告: 配置文件不可写，尝试复制到可写位置"

		# 创建临时可写副本
		WRITABLE_PATH="/tmp/tokens.json"
		cp "$CONFIG_PATH" "$WRITABLE_PATH"
		chmod 600 "$WRITABLE_PATH"
		chown appuser:appgroup "$WRITABLE_PATH"

		# 更新环境变量指向可写位置
		export KIRO_AUTH_TOKEN="$WRITABLE_PATH"

		echo "[init] 配置文件已复制到: $WRITABLE_PATH"
		echo "[init] 注意: 修改将保存到临时位置，容器重启后丢失"
		echo "[init] 建议: 确保宿主机文件权限正确，或使用 Docker volume"
	fi
fi

# 切换到非 root 用户并执行主程序
exec su-exec appuser /docker-entrypoint.sh "$@"
