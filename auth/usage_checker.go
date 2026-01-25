package auth

import (
	"fmt"
	"io"
	"kiro2api/logger"
	"kiro2api/types"
	"kiro2api/utils"
	"net/http"
	"net/url"
)

// UsageLimitsChecker 使用限制检查器 (遵循SRP原则)
type UsageLimitsChecker struct {
	httpClient *http.Client
	tokenIndex int // 用于日志追踪
}

// NewUsageLimitsChecker 创建使用限制检查器
// client 参数可选：如果为 nil，使用 utils.SharedHTTPClient
func NewUsageLimitsChecker(tokenIndex int, client ...*http.Client) *UsageLimitsChecker {
	var httpClient *http.Client
	if len(client) > 0 && client[0] != nil {
		httpClient = client[0]
	} else {
		httpClient = utils.SharedHTTPClient
	}

	return &UsageLimitsChecker{
		httpClient: httpClient,
		tokenIndex: tokenIndex,
	}
}

// CheckUsageLimits 检���token的使用限制 (基于token.md API规范)
func (c *UsageLimitsChecker) CheckUsageLimits(token types.TokenInfo) (*types.UsageLimits, error) {
	// 构建请求URL (完全遵循token.md中的示例)
	baseURL := "https://codewhisperer.us-east-1.amazonaws.com/getUsageLimits"
	params := url.Values{}
	params.Add("isEmailRequired", "true")
	params.Add("origin", "AI_EDITOR")
	params.Add("resourceType", "AGENTIC_REQUEST")

	requestURL := fmt.Sprintf("%s?%s", baseURL, params.Encode())

	// 创建HTTP请求
	req, err := http.NewRequest("GET", requestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("创建使用限制检查请求失败: %v", err)
	}

	// 设置请求头 (使用账号专属的设备指纹)
	var userAgent, xAmzUserAgent string
	if token.Fingerprint != nil && token.Fingerprint.UsageUserAgent != "" {
		userAgent = token.Fingerprint.UsageUserAgent
		xAmzUserAgent = token.Fingerprint.UsageXAmzAgent
	} else {
		// 如果没有指纹信息，临时生成（向后兼容）
		fp := utils.GenerateUsageCheckerFingerprint(token.RefreshToken)
		userAgent = fp.UserAgent
		xAmzUserAgent = fp.XAmzUserAgent
	}

	req.Header.Set("x-amz-user-agent", xAmzUserAgent)
	req.Header.Set("user-agent", userAgent)
	// net/http 发送 Host header 使用 req.Host
	req.Host = req.URL.Host
	req.Header.Set("host", req.URL.Host)
	req.Header.Set("amz-sdk-invocation-id", utils.GenerateUUID()) // 使用标准 UUID 格式
	req.Header.Set("amz-sdk-request", "attempt=1; max=1")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token.AccessToken))
	req.Header.Set("Connection", "close")

	// 发送请求
	logger.Debug("发送使用限制检查请求",
		logger.String("url", requestURL),
		logger.String("token_preview", token.AccessToken[:20]+"..."))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("使用限制检查请求失败: %v", err)
	}
	defer resp.Body.Close()

	// 读取响应
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取使用限制响应失败: %v", err)
	}

	logger.Debug("使用限制API响应",
		logger.Int("status_code", resp.StatusCode),
		logger.String("response_body", string(body)))

	if resp.StatusCode != http.StatusOK {
		// 检查是否是 token 失效错误
		if isUsageLimitsTokenInvalidError(resp.StatusCode, body) {
			return nil, &types.TokenInvalidError{
				StatusCode: resp.StatusCode,
				Message:    string(body),
			}
		}
		return nil, fmt.Errorf("使用限制检查失败: 状态码 %d, 响应: %s", resp.StatusCode, string(body))
	}

	// 解析响应
	var usageLimits types.UsageLimits
	if err := utils.SafeUnmarshal(body, &usageLimits); err != nil {
		return nil, fmt.Errorf("解析使用限制响应失败: %v", err)
	}

	// 记录关键信息
	c.logUsageLimits(&usageLimits)

	return &usageLimits, nil
}

// logUsageLimits 记录使用限制的关键信息
func (c *UsageLimitsChecker) logUsageLimits(limits *types.UsageLimits) {
	for _, breakdown := range limits.UsageBreakdownList {
		if breakdown.ResourceType == "CREDIT" {
			// 计算可用次数 (使用浮点精度数据)
			var totalLimit float64
			var totalUsed float64

			// 基础额度
			baseLimit := breakdown.UsageLimitWithPrecision
			baseUsed := breakdown.CurrentUsageWithPrecision
			totalLimit += baseLimit
			totalUsed += baseUsed

			// 免费试用额度
			var freeTrialLimit float64
			var freeTrialUsed float64
			if breakdown.FreeTrialInfo != nil && breakdown.FreeTrialInfo.FreeTrialStatus == "ACTIVE" {
				freeTrialLimit = breakdown.FreeTrialInfo.UsageLimitWithPrecision
				freeTrialUsed = breakdown.FreeTrialInfo.CurrentUsageWithPrecision
				totalLimit += freeTrialLimit
				totalUsed += freeTrialUsed
			}

			available := totalLimit - totalUsed

			logger.Info("CREDIT使用状态",
				logger.Int("token_index", c.tokenIndex),
				logger.String("resource_type", breakdown.ResourceType),
				logger.Float64("total_limit", totalLimit),
				logger.Float64("total_used", totalUsed),
				logger.Float64("available", available),
				logger.Float64("base_limit", baseLimit),
				logger.Float64("base_used", baseUsed),
				logger.Float64("free_trial_limit", freeTrialLimit),
				logger.Float64("free_trial_used", freeTrialUsed),
				logger.String("free_trial_status", func() string {
					if breakdown.FreeTrialInfo != nil {
						return breakdown.FreeTrialInfo.FreeTrialStatus
					}
					return "NONE"
				}()))

			if available <= 1 {
				logger.Warn("CREDIT使用量即将耗尽",
					logger.Float64("remaining", available),
					logger.String("recommendation", "考虑切换到其他token"))
			}

			break
		}
	}

	// 记录订阅信息
	logger.Debug("订阅信息",
		logger.String("subscription_type", limits.SubscriptionInfo.Type),
		logger.String("subscription_title", limits.SubscriptionInfo.SubscriptionTitle),
		logger.String("user_email", limits.UserInfo.Email))
}

// isUsageLimitsTokenInvalidError 判断使用限制检查错误是否是 token 失效
func isUsageLimitsTokenInvalidError(statusCode int, body []byte) bool {
	// 403 Forbidden 通常表示账号被暂停或 token 失效
	if statusCode != http.StatusForbidden && statusCode != http.StatusUnauthorized {
		return false
	}

	bodyStr := string(body)

	// 检查账号暂停或失效的标识
	suspendedPatterns := []string{
		"TEMPORARILY_SUSPENDED",  // 账号暂停
		"PERMANENTLY_SUSPENDED",  // 账号永久暂停
		"SUSPENDED",              // 通用暂停
		"InvalidToken",           // Token 无效
		"ExpiredToken",           // Token 过期
		"Unauthorized",           // 未授权
	}

	for _, pattern := range suspendedPatterns {
		if contains(bodyStr, pattern) {
			return true
		}
	}

	return false
}
