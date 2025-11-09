package utils

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGenerateFingerprint_Stability(t *testing.T) {
	// 测试同一 refreshToken 生成的指纹是否稳定
	refreshToken := "test-refresh-token-123"

	fp1 := GenerateFingerprint(refreshToken)
	fp2 := GenerateFingerprint(refreshToken)

	assert.Equal(t, fp1.UserAgent, fp2.UserAgent, "同一账号的 UserAgent 应该相同")
	assert.Equal(t, fp1.XAmzUserAgent, fp2.XAmzUserAgent, "同一账号的 XAmzUserAgent 应该相同")
	assert.Equal(t, fp1.DeviceHash, fp2.DeviceHash, "同一账号的 DeviceHash 应该相同")
	assert.Equal(t, fp1.OSVersion, fp2.OSVersion, "同一账号的 OSVersion 应该相同")
	assert.Equal(t, fp1.NodeVersion, fp2.NodeVersion, "同一账号的 NodeVersion 应该相同")
	assert.Equal(t, fp1.SDKVersion, fp2.SDKVersion, "同一账号的 SDKVersion 应该相同")
	assert.Equal(t, fp1.KiroAgentMode, fp2.KiroAgentMode, "同一账号的 KiroAgentMode 应该相同")
}

func TestGenerateFingerprint_Uniqueness(t *testing.T) {
	// 测试不同 refreshToken 生成的指纹是否不同
	token1 := "refresh-token-1"
	token2 := "refresh-token-2"

	fp1 := GenerateFingerprint(token1)
	fp2 := GenerateFingerprint(token2)

	assert.NotEqual(t, fp1.UserAgent, fp2.UserAgent, "不同账号的 UserAgent 应该不同")
	assert.NotEqual(t, fp1.DeviceHash, fp2.DeviceHash, "不同账号的 DeviceHash 应该不同")
	// OS 版本和 Node 版本有可能相同（随机范围有限），所以不做强制要求
}

func TestGenerateFingerprint_Format(t *testing.T) {
	// 测试生成的指纹格式是否正确
	refreshToken := "test-token"
	fp := GenerateFingerprint(refreshToken)

	// 验证 UserAgent 格式
	assert.Contains(t, fp.UserAgent, "aws-sdk-js/")
	assert.Contains(t, fp.UserAgent, "os/darwin#")
	assert.Contains(t, fp.UserAgent, "md/nodejs#")
	assert.Contains(t, fp.UserAgent, fp.DeviceHash)

	// 验证 DeviceHash 长度（应该是 64 个十六进制字符）
	assert.Len(t, fp.DeviceHash, 64, "DeviceHash 应该是 64 个字符")

	// 验证版本号格式
	assert.Regexp(t, `^\d+\.\d+\.\d+$`, fp.OSVersion, "OSVersion 格式应该是 x.y.z")
	assert.Regexp(t, `^\d+\.\d+\.\d+$`, fp.NodeVersion, "NodeVersion 格式应该是 x.y.z")
	assert.Regexp(t, `^1\.\d+\.\d+$`, fp.SDKVersion, "SDKVersion 格式应该是 1.x.y")

	// 验证 KiroAgentMode
	validModes := []string{"spec", "auto", "manual"}
	assert.Contains(t, validModes, fp.KiroAgentMode, "KiroAgentMode 应该是有效值")
}

func TestGenerateFingerprint_VersionRanges(t *testing.T) {
	// 测试生成的版本号是否在合理范围内
	refreshToken := "test-token"
	fp := GenerateFingerprint(refreshToken)

	// 检查 OS 版本（应该是 darwin 23.x 或 24.x）
	assert.True(t, strings.HasPrefix(fp.OSVersion, "23.") || strings.HasPrefix(fp.OSVersion, "24."),
		"OS 版本应该在 23.x - 24.x 范围内")

	// 检查 Node 版本（应该是 18.x - 20.x）
	nodePrefix := fp.NodeVersion[:2]
	assert.Contains(t, []string{"18", "19", "20"}, nodePrefix,
		"Node 版本应该在 18.x - 20.x 范围内")

	// 检查 SDK 版本（应该是 1.x.y）
	assert.True(t, strings.HasPrefix(fp.SDKVersion, "1."),
		"SDK 版本应该以 1. 开头")
}

func TestGenerateRefreshFingerprint(t *testing.T) {
	// 测试刷新指纹生成
	refreshToken := "test-refresh-token"

	fp := GenerateRefreshFingerprint(refreshToken)

	assert.Equal(t, "node", fp.UserAgent, "刷新请求的 UserAgent 应该是 'node'")
	assert.Contains(t, fp.XAmzUserAgent, "aws-sdk-js/3.738.0")
	assert.Contains(t, fp.XAmzUserAgent, "api/sso-oidc#3.738.0")
	assert.NotEmpty(t, fp.DeviceHash)
}

func TestGenerateUsageCheckerFingerprint(t *testing.T) {
	// 测试使用限制检查指纹生成
	refreshToken := "test-usage-token"

	fp := GenerateUsageCheckerFingerprint(refreshToken)

	assert.Contains(t, fp.UserAgent, "aws-sdk-js/1.0.0")
	assert.Contains(t, fp.UserAgent, "api/codewhispererruntime#1.0.0")
	assert.Contains(t, fp.XAmzUserAgent, "aws-sdk-js/1.0.0")
	assert.NotEmpty(t, fp.DeviceHash)
}

func TestFingerprintStability_MultipleTypes(t *testing.T) {
	// 测试同一 token 的不同类型指纹是否使用相同的基础参数
	refreshToken := "stable-token-test"

	fp1 := GenerateFingerprint(refreshToken)
	fp2 := GenerateRefreshFingerprint(refreshToken)
	fp3 := GenerateUsageCheckerFingerprint(refreshToken)

	// DeviceHash 应该相同（基于同一 refreshToken）
	assert.Equal(t, fp1.DeviceHash, fp2.DeviceHash, "同一账号不同类型的请求应该有相同的 DeviceHash")
	assert.Equal(t, fp1.DeviceHash, fp3.DeviceHash, "同一账号不同类型的请求应该有相同的 DeviceHash")

	// OS 版本应该相同
	assert.Equal(t, fp1.OSVersion, fp2.OSVersion, "同一账号不同类型的请求应该有相同的 OSVersion")
	assert.Equal(t, fp1.OSVersion, fp3.OSVersion, "同一账号不同类型的请求应该有相同的 OSVersion")
}

func BenchmarkGenerateFingerprint(b *testing.B) {
	refreshToken := "benchmark-token"
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		GenerateFingerprint(refreshToken)
	}
}

func BenchmarkGenerateFingerprint_Parallel(b *testing.B) {
	refreshToken := "benchmark-token-parallel"

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			GenerateFingerprint(refreshToken)
		}
	})
}
