#!/bin/sh
# Docker entrypoint script
# 在启动主程序前，确保 tokens.json 是文件而非目录

CONFIG_PATH="${KIRO_AUTH_TOKEN:-./tokens.json}"

# 只处理文件路径（不以 [ 或 { 开头的）
case "$CONFIG_PATH" in
    \[*|\{*)
        # JSON 字符串，跳过
        ;;
    *)
        # 文件路径
        if [ -d "$CONFIG_PATH" ]; then
            echo "[entrypoint] 检测到 $CONFIG_PATH 是目录（Docker 挂载导致），尝试修复..."
            # 尝试删除目录（可能失败，因为是挂载点）
            rmdir "$CONFIG_PATH" 2>/dev/null || true
            # 如果仍然是目录，说明是挂载点，无法修复
            if [ -d "$CONFIG_PATH" ]; then
                echo "[entrypoint] 警告: 无法修复挂载的目录，请在宿主机执行:"
                echo "[entrypoint]   rm -rf $CONFIG_PATH && echo '[]' > $CONFIG_PATH"
            fi
        fi
        # 如果文件不存在，创建空配置
        if [ ! -e "$CONFIG_PATH" ]; then
            echo "[entrypoint] 创建空配置文件: $CONFIG_PATH"
            echo '[]' > "$CONFIG_PATH" 2>/dev/null || true
        fi
        ;;
esac

# 启动主程序
exec ./kiro2api "$@"
