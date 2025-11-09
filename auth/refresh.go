package auth

import (
	"bytes"
	"fmt"
	"io"
	"kiro2api/config"
	"kiro2api/types"
	"kiro2api/utils"
	"net/http"
	"time"
)

// refreshSingleToken 刷新单个token
func (tm *TokenManager) refreshSingleToken(authConfig AuthConfig) (types.TokenInfo, error) {
	switch authConfig.AuthType {
	case AuthMethodSocial:
		return refreshSocialToken(authConfig.RefreshToken)
	case AuthMethodIdC:
		return refreshIdCToken(authConfig)
	default:
		return types.TokenInfo{}, fmt.Errorf("不支持的认证类型: %s", authConfig.AuthType)
	}
}

// refreshSocialToken 刷新Social认证token
func refreshSocialToken(refreshToken string) (types.TokenInfo, error) {
	// 为该账号生成固定的设备指纹
	fp := utils.GenerateRefreshFingerprint(refreshToken)

	refreshReq := types.RefreshRequest{
		RefreshToken: refreshToken,
	}

	reqBody, err := utils.FastMarshal(refreshReq)
	if err != nil {
		return types.TokenInfo{}, fmt.Errorf("序列化请求失败: %v", err)
	}

	req, err := http.NewRequest("POST", config.RefreshTokenURL, bytes.NewBuffer(reqBody))
	if err != nil {
		return types.TokenInfo{}, fmt.Errorf("创建请求失败: %v", err)
	}

	// 使用该账号专属的设备指纹
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-amz-user-agent", fp.XAmzUserAgent)
	req.Header.Set("User-Agent", fp.UserAgent)

	client := utils.SharedHTTPClient
	resp, err := client.Do(req)
	if err != nil {
		return types.TokenInfo{}, fmt.Errorf("请求失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		// 检查是否是 token 失效错误（非额度耗尽）
		if isTokenInvalidError(resp.StatusCode, body) {
			return types.TokenInfo{}, &types.TokenInvalidError{
				StatusCode: resp.StatusCode,
				Message:    string(body),
			}
		}
		return types.TokenInfo{}, fmt.Errorf("刷新失败: 状态码 %d, 响应: %s", resp.StatusCode, string(body))
	}

	var refreshResp types.RefreshResponse
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return types.TokenInfo{}, fmt.Errorf("读取响应失败: %v", err)
	}

	if err := utils.SafeUnmarshal(body, &refreshResp); err != nil {
		return types.TokenInfo{}, fmt.Errorf("解析响应失败: %v", err)
	}

	var token types.Token
	token.FromRefreshResponse(refreshResp, refreshToken)

	// 附加完整的设备指纹信息
	fullFp := utils.GenerateFingerprint(refreshToken)
	refreshFp := utils.GenerateRefreshFingerprint(refreshToken)
	usageFp := utils.GenerateUsageCheckerFingerprint(refreshToken)

	token.Fingerprint = &types.DeviceFingerprint{
		UserAgent:        fullFp.UserAgent,
		XAmzUserAgent:    fullFp.XAmzUserAgent,
		DeviceHash:       fullFp.DeviceHash,
		OSVersion:        fullFp.OSVersion,
		NodeVersion:      fullFp.NodeVersion,
		SDKVersion:       fullFp.SDKVersion,
		KiroAgentMode:    fullFp.KiroAgentMode,
		IDEVersion:       fullFp.IDEVersion,
		RefreshUserAgent: refreshFp.UserAgent,
		RefreshXAmzAgent: refreshFp.XAmzUserAgent,
		UsageUserAgent:   usageFp.UserAgent,
		UsageXAmzAgent:   usageFp.XAmzUserAgent,
	}

	return token, nil
}

// refreshIdCToken 刷新IdC认证token
func refreshIdCToken(authConfig AuthConfig) (types.TokenInfo, error) {
	// 为该账号生成固定的设备指纹
	fp := utils.GenerateRefreshFingerprint(authConfig.RefreshToken)

	refreshReq := types.IdcRefreshRequest{
		ClientId:     authConfig.ClientID,
		ClientSecret: authConfig.ClientSecret,
		GrantType:    "refresh_token",
		RefreshToken: authConfig.RefreshToken,
	}

	reqBody, err := utils.FastMarshal(refreshReq)
	if err != nil {
		return types.TokenInfo{}, fmt.Errorf("序列化IdC请求失败: %v", err)
	}

	req, err := http.NewRequest("POST", config.IdcRefreshTokenURL, bytes.NewBuffer(reqBody))
	if err != nil {
		return types.TokenInfo{}, fmt.Errorf("创建IdC请求失败: %v", err)
	}

	// 使用该账号专属的设备指纹设置IdC特殊headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Host", "oidc.us-east-1.amazonaws.com")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("x-amz-user-agent", fp.XAmzUserAgent)
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "*")
	req.Header.Set("sec-fetch-mode", "cors")
	req.Header.Set("User-Agent", fp.UserAgent)
	req.Header.Set("Accept-Encoding", "br, gzip, deflate")

	client := utils.SharedHTTPClient
	resp, err := client.Do(req)
	if err != nil {
		return types.TokenInfo{}, fmt.Errorf("IdC请求失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		// 检查是否是 token 失效错误（非额度耗尽）
		if isTokenInvalidError(resp.StatusCode, body) {
			return types.TokenInfo{}, &types.TokenInvalidError{
				StatusCode: resp.StatusCode,
				Message:    string(body),
			}
		}
		return types.TokenInfo{}, fmt.Errorf("IdC刷新失败: 状态码 %d, 响应: %s", resp.StatusCode, string(body))
	}

	var refreshResp types.RefreshResponse
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return types.TokenInfo{}, fmt.Errorf("读取IdC响应失败: %v", err)
	}

	if err := utils.SafeUnmarshal(body, &refreshResp); err != nil {
		return types.TokenInfo{}, fmt.Errorf("解析IdC响应失败: %v", err)
	}

	var token types.Token
	token.AccessToken = refreshResp.AccessToken
	token.RefreshToken = authConfig.RefreshToken
	token.ExpiresIn = refreshResp.ExpiresIn
	token.ExpiresAt = time.Now().Add(time.Duration(refreshResp.ExpiresIn) * time.Second)

	// 附加完整的设备指纹信息
	fullFp := utils.GenerateFingerprint(authConfig.RefreshToken)
	refreshFp := utils.GenerateRefreshFingerprint(authConfig.RefreshToken)
	usageFp := utils.GenerateUsageCheckerFingerprint(authConfig.RefreshToken)

	token.Fingerprint = &types.DeviceFingerprint{
		UserAgent:        fullFp.UserAgent,
		XAmzUserAgent:    fullFp.XAmzUserAgent,
		DeviceHash:       fullFp.DeviceHash,
		OSVersion:        fullFp.OSVersion,
		NodeVersion:      fullFp.NodeVersion,
		SDKVersion:       fullFp.SDKVersion,
		KiroAgentMode:    fullFp.KiroAgentMode,
		IDEVersion:       fullFp.IDEVersion,
		RefreshUserAgent: refreshFp.UserAgent,
		RefreshXAmzAgent: refreshFp.XAmzUserAgent,
		UsageUserAgent:   usageFp.UserAgent,
		UsageXAmzAgent:   usageFp.XAmzUserAgent,
	}

	return token, nil
}

// RefreshSocialToken 公开的Social token刷新函数
func RefreshSocialToken(refreshToken string) (types.TokenInfo, error) {
	return refreshSocialToken(refreshToken)
}

// RefreshIdCToken 公开的IdC token刷新函数
func RefreshIdCToken(authConfig AuthConfig) (types.TokenInfo, error) {
	return refreshIdCToken(authConfig)
}

// isTokenInvalidError 判断是否是 token 失效错误（非额度耗尽）
func isTokenInvalidError(statusCode int, body []byte) bool {
	// 401/403 通常表示认证失败
	if statusCode != http.StatusUnauthorized && statusCode != http.StatusForbidden {
		return false
	}

	bodyStr := string(body)

	// 检查常见的 token 失效错误标识
	invalidPatterns := []string{
		"invalid_grant",
		"invalid_token",
		"token_expired",
		"unauthorized_client",
		"InvalidToken",
		"ExpiredToken",
		"UnauthorizedClient",
	}

	for _, pattern := range invalidPatterns {
		if contains(bodyStr, pattern) {
			return true
		}
	}

	return false
}

// contains 简单的字符串包含检查（不区分大小写）
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && containsIgnoreCase(s, substr)))
}

func containsIgnoreCase(s, substr string) bool {
	s = toLower(s)
	substr = toLower(substr)
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func toLower(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}
