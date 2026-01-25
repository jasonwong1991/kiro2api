package utils

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"runtime"
)

// 固定版本号常量
const (
	SDKVersionMain   = "1.0.27"  // aws-sdk-js: CodeWhisperer/MCP 主请求
	SDKVersionUsage  = "1.0.0"   // aws-sdk-js: getUsageLimits (对齐 kiro.rs)
	KiroIDEVersion   = "0.8.140" // KiroIDE 固定版本
	NodeVersionFixed = "22.21.1" // Node.js 固定版本
	DarwinVersion    = "24.6.0"  // macOS Darwin 固定版本
	LinuxKernel      = "6.8.0"   // Linux 内核版本
	WindowsVersion   = "10.0"    // Windows 版本
)

// IdC Token 刷新所需的 x-amz-user-agent (对齐 kiro.rs)
const IDCAmzUserAgent = "aws-sdk-js/3.738.0 ua/2.1 os/other lang/js md/browser#unknown_unknown api/sso-oidc#3.738.0 m/E KiroIDE"

// Kiro 主请求所需的 x-amzn-kiro-agent-mode (对齐 kiro.rs)
const KiroAgentModeFixed = "vibe"

// 向后兼容的别名
const SDKVersionFixed = SDKVersionMain

// DeviceFingerprint 设备指纹信息
type DeviceFingerprint struct {
	UserAgent           string // 完整的 User-Agent
	XAmzUserAgent       string // AWS SDK User-Agent
	DeviceHash          string // 设备指纹 hash (machineId)
	OSVersion           string // 操作系统版本
	NodeVersion         string // Node.js 版本
	SDKVersion          string // SDK 版本
	KiroAgentMode       string // Kiro Agent Mode
	IDEVersion          string // IDE 版本号
}

// getOSInfo 获取当前操作系统信息 (用于 User-Agent)
// 返回: osType (darwin/linux/windows), osVersion
func getOSInfo() (string, string) {
	switch runtime.GOOS {
	case "darwin":
		return "darwin", DarwinVersion
	case "linux":
		return "linux", LinuxKernel
	case "windows":
		return "windows", WindowsVersion
	default:
		return "darwin", DarwinVersion // 默认伪装为 macOS
	}
}

// GenerateFingerprint 生成设备指纹
// 基于 refreshToken 生成确定性的指纹，确保同一账号每次生成的指纹相同
func GenerateFingerprint(refreshToken string) *DeviceFingerprint {
	osType, osVersion := getOSInfo()
	machineID := generateMachineID(refreshToken)

	// KiroIDE 完整标识（固定版本-machineId）
	kiroIDEFull := fmt.Sprintf("KiroIDE-%s-%s", KiroIDEVersion, machineID)

	// 构建完整的 User-Agent
	// 格式: aws-sdk-js/1.0.27 ua/2.1 os/darwin#24.6.0 lang/js md/nodejs#22.21.1 api/codewhispererstreaming#1.0.27 m/E KiroIDE-0.8.140-{machineId}
	userAgent := fmt.Sprintf(
		"aws-sdk-js/%s ua/2.1 os/%s#%s lang/js md/nodejs#%s api/codewhispererstreaming#%s m/E %s",
		SDKVersionMain, osType, osVersion, NodeVersionFixed, SDKVersionMain, kiroIDEFull,
	)

	// 构建 x-amz-user-agent
	// 格式: aws-sdk-js/1.0.27 KiroIDE-0.8.140-{machineId}
	xAmzUserAgent := fmt.Sprintf("aws-sdk-js/%s %s", SDKVersionMain, kiroIDEFull)

	return &DeviceFingerprint{
		UserAgent:     userAgent,
		XAmzUserAgent: xAmzUserAgent,
		DeviceHash:    machineID,
		OSVersion:     osVersion,
		NodeVersion:   NodeVersionFixed,
		SDKVersion:    SDKVersionMain,
		KiroAgentMode: KiroAgentModeFixed,
		IDEVersion:    KiroIDEVersion,
	}
}

// GenerateSocialRefreshFingerprint 为 Social Token 刷新生成指纹
// 参考 kiro.rs: Social 刷新仅使用简单 User-Agent，不携带 x-amz-user-agent
func GenerateSocialRefreshFingerprint(refreshToken string) *DeviceFingerprint {
	_, osVersion := getOSInfo()
	machineID := generateMachineID(refreshToken)

	// 格式: KiroIDE-{version}-{machineId}
	userAgent := fmt.Sprintf("KiroIDE-%s-%s", KiroIDEVersion, machineID)

	return &DeviceFingerprint{
		UserAgent:  userAgent,
		DeviceHash: machineID,
		OSVersion:  osVersion,
		IDEVersion: KiroIDEVersion,
	}
}

// GenerateRefreshFingerprint 为 IdC Token 刷新生成指纹
// 参考 kiro.rs: IdC 刷新使用固定的 x-amz-user-agent 和 User-Agent: node
func GenerateRefreshFingerprint(refreshToken string) *DeviceFingerprint {
	_, osVersion := getOSInfo()
	machineID := generateMachineID(refreshToken)

	return &DeviceFingerprint{
		UserAgent:     "node",
		XAmzUserAgent: IDCAmzUserAgent,
		DeviceHash:    machineID,
		OSVersion:     osVersion,
		IDEVersion:    KiroIDEVersion,
		NodeVersion:   NodeVersionFixed,
	}
}

// GenerateUsageCheckerFingerprint 为使用限制检查生成指纹
// 参考 kiro.rs: 使用 SDK 版本 1.0.0
func GenerateUsageCheckerFingerprint(refreshToken string) *DeviceFingerprint {
	osType, osVersion := getOSInfo()
	machineID := generateMachineID(refreshToken)

	// KiroIDE 完整标识 (固定版本)
	kiroIDEFull := fmt.Sprintf("KiroIDE-%s-%s", KiroIDEVersion, machineID)

	userAgent := fmt.Sprintf(
		"aws-sdk-js/%s ua/2.1 os/%s#%s lang/js md/nodejs#%s api/codewhispererruntime#%s m/E %s",
		SDKVersionUsage, osType, osVersion, NodeVersionFixed, SDKVersionUsage, kiroIDEFull,
	)

	xAmzUserAgent := fmt.Sprintf("aws-sdk-js/%s %s", SDKVersionUsage, kiroIDEFull)

	return &DeviceFingerprint{
		UserAgent:     userAgent,
		XAmzUserAgent: xAmzUserAgent,
		DeviceHash:    machineID,
		OSVersion:     osVersion,
		NodeVersion:   NodeVersionFixed,
		SDKVersion:    SDKVersionUsage,
		IDEVersion:    KiroIDEVersion,
	}
}

// generateMachineID 生成与 kiro.rs 对齐的 machineId
// 算法: SHA256("KotlinNativeAPI/{refreshToken}") -> 64 字符十六进制字符串
func generateMachineID(refreshToken string) string {
	sum := sha256.Sum256([]byte("KotlinNativeAPI/" + refreshToken))
	return hex.EncodeToString(sum[:])
}

// GenerateMachineID 导出的 machineId 生成函数
func GenerateMachineID(refreshToken string) string {
	return generateMachineID(refreshToken)
}
