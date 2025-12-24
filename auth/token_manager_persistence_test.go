package auth

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTokenManager_AddToken_ReturnsPersistenceError_WhenSaveFails(t *testing.T) {
	tmpDir := t.TempDir()

	// 让配置路径的父目录变成“文件”，触发 SaveConfigToFile 失败
	notADir := filepath.Join(tmpDir, "notadir")
	require.NoError(t, os.WriteFile(notADir, []byte("x"), 0o600))

	t.Setenv("KIRO_AUTH_TOKEN", filepath.Join(notADir, DefaultConfigPath))

	tm := NewTokenManager([]AuthConfig{})
	err := tm.AddToken(AuthConfig{AuthType: AuthMethodSocial, RefreshToken: "rt"})
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrConfigPersistence))
}

func TestTokenManager_UpdateToken_ReturnsPersistenceError_WhenSaveFails(t *testing.T) {
	tmpDir := t.TempDir()
	notADir := filepath.Join(tmpDir, "notadir")
	require.NoError(t, os.WriteFile(notADir, []byte("x"), 0o600))

	t.Setenv("KIRO_AUTH_TOKEN", filepath.Join(notADir, DefaultConfigPath))

	tm := NewTokenManager([]AuthConfig{{AuthType: AuthMethodSocial, RefreshToken: "rt"}})
	err := tm.UpdateToken(0, "", "rt2", "", "", nil)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrConfigPersistence))
}

func TestTokenManager_RemoveToken_ReturnsPersistenceError_WhenSaveFails(t *testing.T) {
	tmpDir := t.TempDir()
	notADir := filepath.Join(tmpDir, "notadir")
	require.NoError(t, os.WriteFile(notADir, []byte("x"), 0o600))

	t.Setenv("KIRO_AUTH_TOKEN", filepath.Join(notADir, DefaultConfigPath))

	tm := NewTokenManager([]AuthConfig{{AuthType: AuthMethodSocial, RefreshToken: "rt"}})
	tm.invalidated[0] = time.Now()

	err := tm.RemoveToken(0)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrConfigPersistence))
}
