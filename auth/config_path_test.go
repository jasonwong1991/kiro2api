package auth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSaveConfigToFile_WhenPathIsDir_WritesIntoDir(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "tokens.json") // 模拟 Docker bind mount 缺失文件时创建的同名目录
	require.NoError(t, os.MkdirAll(configDir, 0o755))

	configs := []AuthConfig{
		{AuthType: AuthMethodSocial, RefreshToken: "test_refresh_token"},
	}

	require.NoError(t, SaveConfigToFile(configs, configDir))

	writtenPath := filepath.Join(configDir, DefaultConfigPath)
	data, err := os.ReadFile(writtenPath)
	require.NoError(t, err)

	var parsed []AuthConfig
	require.NoError(t, json.Unmarshal(data, &parsed))
	require.Len(t, parsed, 1)
	require.Equal(t, "test_refresh_token", parsed[0].RefreshToken)
}

func TestLoadConfigs_WhenEnvPathIsDir_ReadsDirTokensFile(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "tokens.json")
	require.NoError(t, os.MkdirAll(configDir, 0o755))

	configPath := filepath.Join(configDir, DefaultConfigPath)
	require.NoError(t, os.WriteFile(configPath, []byte(`[{"auth":"Social","refreshToken":"abc"}]`), 0o600))

	t.Setenv("KIRO_AUTH_TOKEN", configDir)

	configs, err := loadConfigs()
	require.NoError(t, err)
	require.Len(t, configs, 1)
	require.Equal(t, "abc", configs[0].RefreshToken)
}

func TestLoadConfigs_WhenDefaultPathIsDir_ReadsDirTokensFile(t *testing.T) {
	tmpDir := t.TempDir()
	originalWD, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(tmpDir))
	t.Cleanup(func() {
		_ = os.Chdir(originalWD)
	})

	// 让默认路径 tokens.json 成为目录，并在目录内放置 tokens.json 文件
	defaultDir := filepath.Join(tmpDir, DefaultConfigPath)
	require.NoError(t, os.MkdirAll(defaultDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(defaultDir, DefaultConfigPath), []byte(`[{"auth":"Social","refreshToken":"def"}]`), 0o600))

	t.Setenv("KIRO_AUTH_TOKEN", "")

	configs, err := loadConfigs()
	require.NoError(t, err)
	require.Len(t, configs, 1)
	require.Equal(t, "def", configs[0].RefreshToken)
}
