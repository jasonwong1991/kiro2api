package utils

import (
	"crypto/sha256"
	"fmt"
)

// 固定版本号常量
const (
	SDKVersionFixed     = "1.0.27"  // aws-sdk-js 固定版本 (所有请求统一使用)
	KiroIDEVersion      = "0.8.140" // KiroIDE 固定版本
	NodeVersionFixed    = "22.21.1" // Node.js 固定版本
	DarwinVersion       = "24.6.0"  // macOS Darwin 固定版本
	LinuxKernel         = "6.8.0"   // Linux 内核版本
	WindowsVersion      = "10.0"    // Windows 版本
	KiroAgentModeFixed  = "vibe"    // Kiro Agent Mode 固定值 (对齐 kiro.rs)
	IDCAmzUserAgent     = "aws-sdk-js/3.738.0 ua/2.1 os/other lang/js md/browser#unknown_unknown api/sso-oidc#3.738.0 m/E KiroIDE" // IdC 刷新固定 User-Agent
)

// DeviceFingerprint 设备指纹信息
type DeviceFingerprint struct {
	UserAgent     string // 完整的 User-Agent
	XAmzUserAgent string // AWS SDK User-Agent
	DeviceHash    string // 账号专属机器码 (64 字符，基于 refreshToken 生成)
	OSVersion     string // 操作系统版本
	NodeVersion   string // Node.js 版本
	SDKVersion    string // SDK 版本
	KiroAgentMode string // Kiro Agent Mode
	IDEVersion    string // IDE 版本号
}

// getOSInfo 获取操作系统信息 (统一使用 macOS)
// 返回: osType (darwin), osVersion
func getOSInfo() (string, string) {
	return "darwin", DarwinVersion
}

// generateDeviceHash 基于 refreshToken 生成账号专属机器码 (使用 SHA256)
// 格式: sha256("KiroAPI/{refreshToken}")
// 确保同一 refreshToken 每次生成相同的 64 字符十六进制机器码
func generateDeviceHash(refreshToken string) string {
	data := fmt.Sprintf("KiroAPI/%s", refreshToken)
	hash := sha256.Sum256([]byte(data))
	return fmt.Sprintf("%x", hash)
}

// GenerateFingerprint 生成设备指纹
// 基于 refreshToken 生成确定性指纹，确保同一账号每次生成的指纹相同
func GenerateFingerprint(refreshToken string) *DeviceFingerprint {
	// 获取操作系统信息
	osType, osVersion := getOSInfo()

	// 生成账号专属机器码（确定性）
	deviceHash := generateDeviceHash(refreshToken)

	// 使用固定的 agent mode (对齐 kiro.rs)
	agentMode := KiroAgentModeFixed

	// KiroIDE 完整标识（固定版本-账号专属机器码）
	kiroIDEFull := fmt.Sprintf("KiroIDE-%s-%s", KiroIDEVersion, deviceHash)

	// 构建完整的 User-Agent
	// 格式: aws-sdk-js/1.0.27 ua/2.1 os/darwin#24.6.0 lang/js md/nodejs#22.21.1 api/codewhispererstreaming#1.0.27 m/E KiroIDE-0.8.140-{机器码}
	userAgent := fmt.Sprintf(
		"aws-sdk-js/%s ua/2.1 os/%s#%s lang/js md/nodejs#%s api/codewhispererstreaming#%s m/E %s",
		SDKVersionFixed, osType, osVersion, NodeVersionFixed, SDKVersionFixed, kiroIDEFull,
	)

	// 构建 x-amz-user-agent
	// 格式: aws-sdk-js/1.0.27 KiroIDE-0.8.140-{机器码}
	xAmzUserAgent := fmt.Sprintf(
		"aws-sdk-js/%s %s",
		SDKVersionFixed, kiroIDEFull,
	)

	return &DeviceFingerprint{
		UserAgent:     userAgent,
		XAmzUserAgent: xAmzUserAgent,
		DeviceHash:    deviceHash,
		OSVersion:     osVersion,
		NodeVersion:   NodeVersionFixed,
		SDKVersion:    SDKVersionFixed,
		KiroAgentMode: agentMode,
		IDEVersion:    KiroIDEVersion,
	}
}

// GenerateSocialRefreshFingerprint 为 Social Token 刷新生成指纹
// Social 刷新使用简单的 User-Agent 格式（只包含 KiroIDE 标识）
func GenerateSocialRefreshFingerprint(refreshToken string) *DeviceFingerprint {
	// 获取操作系统信息
	_, osVersion := getOSInfo()

	// 生成账号专属机器码（确定性）
	deviceHash := generateDeviceHash(refreshToken)

	// KiroIDE 完整标识（固定版本-账号专属机器码）
	kiroIDEFull := fmt.Sprintf("KiroIDE-%s-%s", KiroIDEVersion, deviceHash)

	// Social 刷新使用简单的 User-Agent（只包含 KiroIDE 标识）
	userAgent := kiroIDEFull

	return &DeviceFingerprint{
		UserAgent:     userAgent,
		XAmzUserAgent: "", // Social 刷新不使用 x-amz-user-agent
		DeviceHash:    deviceHash,
		OSVersion:     osVersion,
		SDKVersion:    SDKVersionFixed,
		IDEVersion:    KiroIDEVersion,
		NodeVersion:   NodeVersionFixed,
	}
}

// GenerateRefreshFingerprint 为 IdC Token 刷新生成指纹
// IdC 刷新使用完整的 AWS SDK User-Agent 格式
func GenerateRefreshFingerprint(refreshToken string) *DeviceFingerprint {
	// 获取操作系统信息
	osType, osVersion := getOSInfo()

	// 生成账号专属机器码（确定性）
	deviceHash := generateDeviceHash(refreshToken)

	// IdC 刷新使用完整的 User-Agent (与实际请求一致)
	// 格式: aws-sdk-js/3.738.0 ua/2.1 os/darwin#24.6.0 lang/js md/nodejs#22.21.1 api/sso-oidc#3.738.0 m/E KiroIDE
	userAgent := fmt.Sprintf(
		"aws-sdk-js/3.738.0 ua/2.1 os/%s#%s lang/js md/nodejs#%s api/sso-oidc#3.738.0 m/E KiroIDE",
		osType, osVersion, NodeVersionFixed,
	)

	// IdC 刷新的 x-amz-user-agent 使用简短格式（无 hash）
	// 格式: aws-sdk-js/3.738.0 KiroIDE
	xAmzUserAgent := "aws-sdk-js/3.738.0 KiroIDE"

	return &DeviceFingerprint{
		UserAgent:     userAgent,
		XAmzUserAgent: xAmzUserAgent,
		DeviceHash:    deviceHash,
		OSVersion:     osVersion,
		SDKVersion:    "3.738.0", // IdC 使用 3.738.0
		IDEVersion:    KiroIDEVersion,
		NodeVersion:   NodeVersionFixed,
	}
}

// GenerateUsageCheckerFingerprint 为使用限制检查生成指纹
// 使用 SDK 版本 1.0.0 (对齐 kiro.rs)
func GenerateUsageCheckerFingerprint(refreshToken string) *DeviceFingerprint {
	// 获取操作系统信息
	osType, osVersion := getOSInfo()

	// 生成账号专属机器码（确定性）
	deviceHash := generateDeviceHash(refreshToken)

	// KiroIDE 完整标识（固定版本-账号专属机器码）
	kiroIDEFull := fmt.Sprintf("KiroIDE-%s-%s", KiroIDEVersion, deviceHash)

	// Usage Checker 使用 SDK 版本 1.0.0 (对齐 kiro.rs)
	usageSDKVersion := "1.0.0"

	userAgent := fmt.Sprintf(
		"aws-sdk-js/%s ua/2.1 os/%s#%s lang/js md/nodejs#%s api/codewhispererstreaming#%s m/E %s",
		usageSDKVersion, osType, osVersion, NodeVersionFixed, usageSDKVersion, kiroIDEFull,
	)

	xAmzUserAgent := fmt.Sprintf(
		"aws-sdk-js/%s %s",
		usageSDKVersion, kiroIDEFull,
	)

	return &DeviceFingerprint{
		UserAgent:     userAgent,
		XAmzUserAgent: xAmzUserAgent,
		DeviceHash:    deviceHash,
		OSVersion:     osVersion,
		NodeVersion:   NodeVersionFixed,
		SDKVersion:    usageSDKVersion,
		IDEVersion:    KiroIDEVersion,
	}
}

// GenerateMachineID 生成机器码 (使用 SHA256 算法)
// 格式: sha256_hex("KotlinNativeAPI/{refreshToken}")
// 与 kiro.rs 保持一致
func GenerateMachineID(refreshToken string) string {
	data := fmt.Sprintf("KotlinNativeAPI/%s", refreshToken)
	hash := sha256.Sum256([]byte(data))
	return fmt.Sprintf("%x", hash)
}
