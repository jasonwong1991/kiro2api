package types

import (
	"net/http"
	"time"
)

// DeviceFingerprint 设备指纹信息（存储在 Token 中）
type DeviceFingerprint struct {
	UserAgent           string `json:"userAgent,omitempty"`           // 完整的 User-Agent
	XAmzUserAgent       string `json:"xAmzUserAgent,omitempty"`       // AWS SDK User-Agent
	DeviceHash          string `json:"deviceHash,omitempty"`          // 设备指纹 hash
	OSVersion           string `json:"osVersion,omitempty"`           // 操作系统版本
	NodeVersion         string `json:"nodeVersion,omitempty"`         // Node.js 版本
	SDKVersion          string `json:"sdkVersion,omitempty"`          // SDK 版本
	KiroAgentMode       string `json:"kiroAgentMode,omitempty"`       // Kiro Agent Mode
	IDEVersion          string `json:"ideVersion,omitempty"`          // IDE 版本号
	RefreshUserAgent    string `json:"refreshUserAgent,omitempty"`    // Token 刷新专用 User-Agent
	RefreshXAmzAgent    string `json:"refreshXAmzAgent,omitempty"`    // Token 刷新专用 X-Amz-User-Agent
	UsageUserAgent      string `json:"usageUserAgent,omitempty"`      // 使用限制检查专用 User-Agent
	UsageXAmzAgent      string `json:"usageXAmzAgent,omitempty"`      // 使用限制检查专用 X-Amz-User-Agent
}

// Token 统一的token管理结构，合并了TokenInfo、RefreshResponse、RefreshRequest的功能
type Token struct {
	// 核心token信息
	AccessToken  string    `json:"accessToken,omitempty"`
	RefreshToken string    `json:"refreshToken"`
	ExpiresAt    time.Time `json:"expiresAt,omitempty"`

	// API响应字段
	ExpiresIn  int    `json:"expiresIn,omitempty"`  // 多少秒后失效，来自RefreshResponse
	ProfileArn string `json:"profileArn,omitempty"` // 来自RefreshResponse

	// 区域信息
	Region string `json:"region,omitempty"` // AWS 区域，如 us-east-1, us-east-2

	// 设备指纹信息（每个账号的固定设备标识）
	Fingerprint *DeviceFingerprint `json:"fingerprint,omitempty"`

	// 运行时字段（不序列化）
	ConfigIndex int          `json:"-"` // 配置索引
	HTTPClient  *http.Client `json:"-"` // 代理客户端
}

// FromRefreshResponse 从RefreshResponse创建Token
func (t *Token) FromRefreshResponse(resp RefreshResponse, originalRefreshToken string) {
	t.AccessToken = resp.AccessToken
	t.RefreshToken = originalRefreshToken // 保持原始refresh token
	t.ExpiresIn = resp.ExpiresIn
	t.ProfileArn = resp.ProfileArn

	// 确保合理的过期时间（至少1小时）
	expiresIn := resp.ExpiresIn
	if expiresIn <= 0 || expiresIn < 3600 {
		// 如果过期时间太短或无效，默认设置为1小时
		expiresIn = 3600
	}
	t.ExpiresAt = time.Now().Add(time.Duration(expiresIn) * time.Second)
}

// IsExpired 检查token是否已过期
func (t *Token) IsExpired() bool {
	return time.Now().After(t.ExpiresAt)
}

// 兼容性别名 - 逐步迁移时使用
type TokenInfo = Token // TokenInfo现在是Token的别名
// RefreshResponse 统一的token刷新响应结构，支持Social和IdC两种认证方式
type RefreshResponse struct {
	AccessToken  string `json:"accessToken"`
	ExpiresIn    int    `json:"expiresIn"` // 多少秒后失效
	RefreshToken string `json:"refreshToken,omitempty"`

	// Social认证方式专用字段
	ProfileArn string `json:"profileArn,omitempty"`

	// IdC认证方式专用字段
	TokenType string `json:"tokenType,omitempty"`

	// 可能的其他响应字段
	OriginSessionId    *string `json:"originSessionId,omitempty"`
	IssuedTokenType    *string `json:"issuedTokenType,omitempty"`
	AwsSsoAppSessionId *string `json:"aws_sso_app_session_id,omitempty"`
	IdToken            *string `json:"idToken,omitempty"`
}

// RefreshRequest Social认证方式的刷新请求结构
type RefreshRequest struct {
	RefreshToken string `json:"refreshToken"`
}

// IdcRefreshRequest IdC认证方式的刷新请求结构
type IdcRefreshRequest struct {
	ClientId     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
	GrantType    string `json:"grantType"`
	RefreshToken string `json:"refreshToken"`
}

// TokenInvalidError token 失效错误（非额度耗尽）
type TokenInvalidError struct {
	StatusCode int
	Message    string
}

func (e *TokenInvalidError) Error() string {
	return e.Message
}

// IsTokenInvalidError 检查错误是否是 token 失效错误
func IsTokenInvalidError(err error) bool {
	_, ok := err.(*TokenInvalidError)
	return ok
}
