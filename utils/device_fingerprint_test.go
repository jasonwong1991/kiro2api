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

	// 验证 DeviceHash 长度（应该是 64 个十六进制字符 - SHA256）
	assert.Len(t, fp.DeviceHash, 64, "DeviceHash 应该是 64 个字符")

	// 验证版本号格式
	assert.Regexp(t, `^\d+\.\d+\.\d+$`, fp.OSVersion, "OSVersion 格式应该是 x.y.z")
	assert.Regexp(t, `^\d+\.\d+\.\d+$`, fp.NodeVersion, "NodeVersion 格式应该是 x.y.z")
	assert.Regexp(t, `^1\.\d+\.\d+$`, fp.SDKVersion, "SDKVersion 格式应该是 1.x.y")

	// 验证 KiroAgentMode (对齐 kiro.rs 固定为 spec)
	assert.Equal(t, KiroAgentModeFixed, fp.KiroAgentMode, "KiroAgentMode 应该固定为 spec")
}

func TestGenerateFingerprint_VersionRanges(t *testing.T) {
	// 测试生成的版本号是否在合理范围内
	refreshToken := "test-token"
	fp := GenerateFingerprint(refreshToken)

	// 检查 OS 版本（应该是 darwin 25.x）
	assert.True(t, strings.HasPrefix(fp.OSVersion, "25."),
		"OS 版本应该是 25.x")

	// 检查 Node 版本（对齐 kiro.rs 默认值 22.21.1）
	assert.True(t, strings.HasPrefix(fp.NodeVersion, "22."),
		"Node 版本应该以 22. 开头")

	// 检查 SDK 版本（应该是 1.x.y）
	assert.True(t, strings.HasPrefix(fp.SDKVersion, "1."),
		"SDK 版本应该以 1. 开头")
}

func TestGenerateSocialRefreshFingerprint(t *testing.T) {
	// 测试 Social Token 刷新指纹生成
	refreshToken := "test-social-refresh-token"

	fp := GenerateSocialRefreshFingerprint(refreshToken)

	// Social 刷新使用简单 User-Agent 格式
	assert.Contains(t, fp.UserAgent, "KiroIDE-")
	assert.NotContains(t, fp.UserAgent, "aws-sdk-js/")
	assert.Empty(t, fp.XAmzUserAgent, "Social 刷新不应携带 x-amz-user-agent")
	assert.Len(t, fp.DeviceHash, 64)
	assert.NotEmpty(t, fp.DeviceHash)
}

func TestGenerateRefreshFingerprint(t *testing.T) {
	// 测试 IdC Token 刷新指纹生成
	refreshToken := "test-refresh-token"

	fp := GenerateRefreshFingerprint(refreshToken)

	// IdC 刷新使用完整的 AWS SDK User-Agent 格式
	assert.Contains(t, fp.UserAgent, "aws-sdk-js/3.738.0", "IdC 刷新应使用 SDK 版本 3.738.0")
	assert.Contains(t, fp.UserAgent, "ua/2.1", "应包含 ua/2.1")
	assert.Contains(t, fp.UserAgent, "os/darwin#", "应包含 os/darwin")
	assert.Contains(t, fp.UserAgent, "lang/js", "应包含 lang/js")
	assert.Contains(t, fp.UserAgent, "md/nodejs#", "应包含 nodejs")
	assert.Contains(t, fp.UserAgent, "api/sso-oidc#3.738.0", "应包含 api/sso-oidc")
	assert.Contains(t, fp.UserAgent, "m/E KiroIDE", "应包含 KiroIDE")

	// x-amz-user-agent 应该是简短格式（无 hash）
	assert.Equal(t, "aws-sdk-js/3.738.0 KiroIDE", fp.XAmzUserAgent, "x-amz-user-agent 应该是简短格式")
	assert.NotContains(t, fp.XAmzUserAgent, "ua/2.1", "x-amz-user-agent 不应包含详细信息")
	assert.NotEmpty(t, fp.DeviceHash)
}

func TestGenerateUsageCheckerFingerprint(t *testing.T) {
	// 测试使用限制检查指纹生成
	refreshToken := "test-usage-token"

	fp := GenerateUsageCheckerFingerprint(refreshToken)

	// Usage Checker 使用 SDK 版本 1.0.0 (对齐 kiro.rs)
	assert.Contains(t, fp.UserAgent, "aws-sdk-js/1.0.0")
	assert.Contains(t, fp.UserAgent, "api/codewhispererstreaming#1.0.0")
	assert.Contains(t, fp.XAmzUserAgent, "aws-sdk-js/1.0.0")
	assert.NotEmpty(t, fp.DeviceHash)
}

func TestMachineID_SHA256Algorithm(t *testing.T) {
	// 测试 Machine ID 使用 SHA256 算法 + KotlinNativeAPI 前缀
	refreshToken := "test-token-for-sha256"

	machineID := GenerateMachineID(refreshToken)

	// SHA256 输出应该是 64 个十六进制字符
	assert.Len(t, machineID, 64, "Machine ID 应该是 64 个字符 (SHA256)")

	// 验证是有效的十六进制字符串
	for _, c := range machineID {
		assert.True(t, (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f'),
			"Machine ID 应该只包含十六进制字符")
	}

	// 验证稳定性
	machineID2 := GenerateMachineID(refreshToken)
	assert.Equal(t, machineID, machineID2, "同一 token 的 Machine ID 应该相同")

	// 验证不同 token 产生不同的 Machine ID
	machineID3 := GenerateMachineID("different-token")
	assert.NotEqual(t, machineID, machineID3, "不同 token 的 Machine ID 应该不同")
}

func TestFingerprintStability_MultipleTypes(t *testing.T) {
	// 测试同一 token 的不同类型指纹是否使用相同的基础参数
	refreshToken := "stable-token-test"

	fp1 := GenerateFingerprint(refreshToken)
	fp2 := GenerateSocialRefreshFingerprint(refreshToken)
	fp3 := GenerateRefreshFingerprint(refreshToken)
	fp4 := GenerateUsageCheckerFingerprint(refreshToken)

	// DeviceHash 应该相同（基于同一 refreshToken）
	assert.Equal(t, fp1.DeviceHash, fp2.DeviceHash, "同一账号不同类型的请求应该有相同的 DeviceHash")
	assert.Equal(t, fp1.DeviceHash, fp3.DeviceHash, "同一账号不同类型的请求应该有相同的 DeviceHash")
	assert.Equal(t, fp1.DeviceHash, fp4.DeviceHash, "同一账号不同类型的请求应该有相同的 DeviceHash")

	// OS 版本应该相同
	assert.Equal(t, fp1.OSVersion, fp2.OSVersion, "同一账号不同类型的请求应该有相同的 OSVersion")
	assert.Equal(t, fp1.OSVersion, fp4.OSVersion, "同一账号不同类型的请求应该有相同的 OSVersion")
}

func BenchmarkGenerateFingerprint(b *testing.B) {
	refreshToken := "benchmark-token"
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		GenerateFingerprint(refreshToken)
	}
}

func BenchmarkGenerateMachineID(b *testing.B) {
	refreshToken := "benchmark-token"
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		GenerateMachineID(refreshToken)
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
