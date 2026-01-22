package utils

import (
	"crypto/md5"
	"fmt"
	"math/rand"
)

// 固定版本号常量
const (
	SDKVersionFixed = "1.0.27" // aws-sdk-js 固定版本
)

// DeviceFingerprint 设备指纹信息
type DeviceFingerprint struct {
	UserAgent           string // 完整的 User-Agent
	XAmzUserAgent       string // AWS SDK User-Agent
	DeviceHash          string // 设备指纹 hash
	OSVersion           string // 操作系统版本
	NodeVersion         string // Node.js 版本
	SDKVersion          string // SDK 版本
	KiroAgentMode       string // Kiro Agent Mode
	IDEVersion          string // IDE 版本号
}

// DeviceFingerprintGenerator 设备指纹生成器
type DeviceFingerprintGenerator struct {
	rng *rand.Rand
}

// NewDeviceFingerprintGenerator 创建设备指纹生成器
func NewDeviceFingerprintGenerator(seed int64) *DeviceFingerprintGenerator {
	return &DeviceFingerprintGenerator{
		rng: rand.New(rand.NewSource(seed)),
	}
}

// GenerateFingerprint 生成设备指纹
// 基于 refreshToken 作为种子，确保同一账号每次生成的指纹相同
func GenerateFingerprint(refreshToken string) *DeviceFingerprint {
	// 使用 refreshToken 作为种子，确保稳定性
	seed := hashToSeed(refreshToken)
	gen := NewDeviceFingerprintGenerator(seed)

	// 生成各种版本号
	osVersion := gen.randomOSVersion()
	nodeVersion := gen.randomNodeVersion()
	ideVersion := gen.randomIDEVersion()
	deviceHash := gen.generateDeviceHash(refreshToken)
	agentMode := gen.randomAgentMode()

	// KiroIDE 完整标识（版本-hash）
	kiroIDEFull := fmt.Sprintf("KiroIDE-%s-%s", ideVersion, deviceHash)

	// 构建完整的 User-Agent
	// 格式: aws-sdk-js/1.0.27 ua/2.1 os/darwin#24.6.0 lang/js md/nodejs#22.21.1 api/codewhispererstreaming#1.0.27 m/E KiroIDE-0.8.140-{hash}
	userAgent := fmt.Sprintf(
		"aws-sdk-js/%s ua/2.1 os/darwin#%s lang/js md/nodejs#%s api/codewhispererstreaming#%s m/E %s",
		SDKVersionFixed, osVersion, nodeVersion, SDKVersionFixed, kiroIDEFull,
	)

	// 构建 x-amz-user-agent
	// 格式: aws-sdk-js/1.0.27 KiroIDE-0.8.140-{hash}
	xAmzUserAgent := fmt.Sprintf(
		"aws-sdk-js/%s %s",
		SDKVersionFixed, kiroIDEFull,
	)

	return &DeviceFingerprint{
		UserAgent:     userAgent,
		XAmzUserAgent: xAmzUserAgent,
		DeviceHash:    deviceHash,
		OSVersion:     osVersion,
		NodeVersion:   nodeVersion,
		SDKVersion:    SDKVersionFixed,
		KiroAgentMode: agentMode,
		IDEVersion:    ideVersion,
	}
}

// GenerateRefreshFingerprint 为 token 刷新生成指纹
func GenerateRefreshFingerprint(refreshToken string) *DeviceFingerprint {
	seed := hashToSeed(refreshToken)
	gen := NewDeviceFingerprintGenerator(seed)

	osVersion := gen.randomOSVersion()
	ideVersion := gen.randomIDEVersion()
	deviceHash := gen.generateDeviceHash(refreshToken)

	// IdC 刷新使用的固定 SDK 版本
	sdkVersion := "3.738.0"

	// KiroIDE 完整标识
	kiroIDEFull := fmt.Sprintf("KiroIDE-%s-%s", ideVersion, deviceHash)

	xAmzUserAgent := fmt.Sprintf(
		"aws-sdk-js/%s ua/2.1 os/other lang/js md/browser#unknown_unknown api/sso-oidc#%s m/E %s",
		sdkVersion, sdkVersion, kiroIDEFull,
	)

	return &DeviceFingerprint{
		UserAgent:     "node",
		XAmzUserAgent: xAmzUserAgent,
		DeviceHash:    deviceHash,
		OSVersion:     osVersion,
		SDKVersion:    sdkVersion,
		IDEVersion:    ideVersion,
	}
}

// GenerateUsageCheckerFingerprint 为使用限制检查生成指纹
func GenerateUsageCheckerFingerprint(refreshToken string) *DeviceFingerprint {
	seed := hashToSeed(refreshToken)
	gen := NewDeviceFingerprintGenerator(seed)

	osVersion := gen.randomOSVersion()
	nodeVersion := gen.randomNodeVersion()
	deviceHash := gen.generateDeviceHash(refreshToken)

	// KiroIDE 完整标识
	kiroIDEFull := fmt.Sprintf("KiroIDE-%s-%s", gen.randomIDEVersion(), deviceHash)

	userAgent := fmt.Sprintf(
		"aws-sdk-js/%s ua/2.1 os/darwin#%s lang/js md/nodejs#%s api/codewhispererruntime#%s m/E %s",
		SDKVersionFixed, osVersion, nodeVersion, SDKVersionFixed, kiroIDEFull,
	)

	xAmzUserAgent := fmt.Sprintf(
		"aws-sdk-js/%s %s",
		SDKVersionFixed, kiroIDEFull,
	)

	return &DeviceFingerprint{
		UserAgent:     userAgent,
		XAmzUserAgent: xAmzUserAgent,
		DeviceHash:    deviceHash,
		OSVersion:     osVersion,
		NodeVersion:   nodeVersion,
		SDKVersion:    SDKVersionFixed,
	}
}

// randomOSVersion 生成随机 macOS 版本号 (Darwin kernel version)
// 合理范围：24.0.0 - 24.6.0 (对应 macOS 15.x)
func (g *DeviceFingerprintGenerator) randomOSVersion() string {
	major := 24                       // 固定 24
	minor := g.rng.Intn(7)            // 0-6
	patch := g.rng.Intn(10)           // 0-9
	return fmt.Sprintf("%d.%d.%d", major, minor, patch)
}

// randomNodeVersion 生成随机 Node.js 版本号
// 合理范围：22.x (22.0.0 - 22.21.x)
func (g *DeviceFingerprintGenerator) randomNodeVersion() string {
	major := 22                       // 固定 22
	minor := g.rng.Intn(22)           // 0-21
	patch := g.rng.Intn(10)           // 0-9
	return fmt.Sprintf("%d.%d.%d", major, minor, patch)
}

// randomIDEVersion 生成随机 IDE 版本号
// 范围：0.8.100 - 0.8.140
func (g *DeviceFingerprintGenerator) randomIDEVersion() string {
	patch := 100 + g.rng.Intn(41)     // 100-140
	return fmt.Sprintf("0.8.%d", patch)
}

// randomAgentMode 生成随机的 agent mode
func (g *DeviceFingerprintGenerator) randomAgentMode() string {
	modes := []string{"spec", "auto", "manual"}
	return modes[g.rng.Intn(len(modes))]
}

// generateDeviceHash 基于 refreshToken 生成稳定的设备 hash
func (g *DeviceFingerprintGenerator) generateDeviceHash(refreshToken string) string {
	// 使用 MD5 生成 64 字符的十六进制 hash（与原格式一致）
	// 使用 rng 确保同一 token 每次生成相同的 hash
	data := fmt.Sprintf("%s-%d-%d", refreshToken, g.rng.Int63(), g.rng.Int63())
	hash := md5.Sum([]byte(data))

	// 生成 64 字符的 hash（扩展到原始长度）
	return fmt.Sprintf("%032x%032x", hash, md5.Sum([]byte(data+"-ext")))
}

// hashToSeed 将字符串转换为种子
func hashToSeed(s string) int64 {
	hash := md5.Sum([]byte(s))
	seed := int64(0)
	for i := 0; i < 8; i++ {
		seed = (seed << 8) | int64(hash[i])
	}
	return seed
}
