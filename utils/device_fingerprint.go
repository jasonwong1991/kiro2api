package utils

import (
	"crypto/md5"
	"fmt"
	"math/rand"
	"time"
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
	sdkVersion := gen.randomSDKVersion()
	ideVersion := gen.randomIDEVersion()
	deviceHash := gen.generateDeviceHash(refreshToken)
	agentMode := gen.randomAgentMode()

	// 构建完整的 User-Agent
	userAgent := fmt.Sprintf(
		"aws-sdk-js/%s ua/2.1 os/darwin#%s lang/js md/nodejs#%s api/codewhispererstreaming#%s m/E %s",
		sdkVersion, osVersion, nodeVersion, sdkVersion, deviceHash,
	)

	// 构建 x-amz-user-agent
	xAmzUserAgent := fmt.Sprintf(
		"aws-sdk-js/%s %s",
		sdkVersion, deviceHash,
	)

	return &DeviceFingerprint{
		UserAgent:     userAgent,
		XAmzUserAgent: xAmzUserAgent,
		DeviceHash:    deviceHash,
		OSVersion:     osVersion,
		NodeVersion:   nodeVersion,
		SDKVersion:    sdkVersion,
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

	xAmzUserAgent := fmt.Sprintf(
		"aws-sdk-js/%s ua/2.1 os/other lang/js md/browser#unknown_unknown api/sso-oidc#%s m/E %s",
		sdkVersion, sdkVersion, ideVersion,
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

	// 使用限制检查的 SDK 版本
	usageSdkVersion := "1.0.0"

	userAgent := fmt.Sprintf(
		"aws-sdk-js/%s ua/2.1 os/darwin#%s lang/js md/nodejs#%s api/codewhispererruntime#%s m/E %s",
		usageSdkVersion, osVersion, nodeVersion, usageSdkVersion, deviceHash,
	)

	xAmzUserAgent := fmt.Sprintf(
		"aws-sdk-js/%s %s",
		usageSdkVersion, deviceHash,
	)

	return &DeviceFingerprint{
		UserAgent:     userAgent,
		XAmzUserAgent: xAmzUserAgent,
		DeviceHash:    deviceHash,
		OSVersion:     osVersion,
		NodeVersion:   nodeVersion,
		SDKVersion:    usageSdkVersion,
	}
}

// randomOSVersion 生成随机 macOS 版本号 (Darwin kernel version)
// 合理范围：23.0.0 - 24.6.0 (对应 macOS 14.x)
func (g *DeviceFingerprintGenerator) randomOSVersion() string {
	major := 23 + g.rng.Intn(2)      // 23 或 24
	minor := g.rng.Intn(7)            // 0-6
	patch := g.rng.Intn(10)           // 0-9
	return fmt.Sprintf("%d.%d.%d", major, minor, patch)
}

// randomNodeVersion 生成随机 Node.js 版本号
// 合理范围：18.x - 20.x
func (g *DeviceFingerprintGenerator) randomNodeVersion() string {
	major := 18 + g.rng.Intn(3)      // 18, 19, 或 20
	minor := g.rng.Intn(20)           // 0-19
	patch := g.rng.Intn(10)           // 0-9
	return fmt.Sprintf("%d.%d.%d", major, minor, patch)
}

// randomSDKVersion 生成随机 AWS SDK 版本号
func (g *DeviceFingerprintGenerator) randomSDKVersion() string {
	minor := 0 + g.rng.Intn(5)        // 1.0.x - 1.4.x
	patch := 10 + g.rng.Intn(20)      // 10-29
	return fmt.Sprintf("1.%d.%d", minor, patch)
}

// randomIDEVersion 生成随机 IDE 版本号
func (g *DeviceFingerprintGenerator) randomIDEVersion() string {
	minor := 2 + g.rng.Intn(2)        // 0.2.x - 0.3.x
	patch := 10 + g.rng.Intn(20)      // 10-29
	return fmt.Sprintf("KiroIDE-0.%d.%d", minor, patch)
}

// randomAgentMode 生成随机的 agent mode
func (g *DeviceFingerprintGenerator) randomAgentMode() string {
	modes := []string{"spec", "auto", "manual"}
	return modes[g.rng.Intn(len(modes))]
}

// generateDeviceHash 基于 refreshToken 生成稳定的设备 hash
func (g *DeviceFingerprintGenerator) generateDeviceHash(refreshToken string) string {
	// 使用 MD5 生成 64 字符的十六进制 hash（与原格式一致）
	data := fmt.Sprintf("%s-%d-%d", refreshToken, g.rng.Int63(), time.Now().Unix())
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
