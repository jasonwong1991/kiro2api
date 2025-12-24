package auth

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func isLikelyJSONConfigValue(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return false
	}
	switch trimmed[0] {
	case '[', '{':
		return true
	default:
		return false
	}
}

// resolveConfigFilePath 兼容两类配置路径：
// 1) 文件路径：/app/tokens.json
// 2) 目录路径：/app/tokens （或 Docker bind mount 缺失文件导致的 /app/tokens.json 目录）
//
// 当 path 指向目录时，实际配置文件落在 <dir>/tokens.json。
func resolveConfigFilePath(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", fmt.Errorf("配置路径为空")
	}

	info, err := os.Stat(trimmed)
	if err == nil {
		if info.IsDir() {
			return filepath.Join(trimmed, DefaultConfigPath), nil
		}
		return trimmed, nil
	}

	if !os.IsNotExist(err) {
		return "", fmt.Errorf("检查配置路径失败: %w", err)
	}

	// 不存在时：如果看起来更像目录（无扩展名或以分隔符结尾），则按目录处理。
	if strings.HasSuffix(trimmed, string(os.PathSeparator)) || filepath.Ext(trimmed) == "" {
		dir := strings.TrimRight(trimmed, string(os.PathSeparator))
		if dir == "" {
			dir = string(os.PathSeparator)
		}
		return filepath.Join(dir, DefaultConfigPath), nil
	}

	// 默认按文件路径处理
	return trimmed, nil
}
