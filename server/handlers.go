package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"kiro2api/auth"
	"kiro2api/config"
	"kiro2api/logger"
	"kiro2api/parser"
	"kiro2api/types"
	"kiro2api/utils"

	"github.com/gin-gonic/gin"
)

const exhaustedThreshold = 1.0

// extractRelevantHeaders 提取相关的请求头信息
func extractRelevantHeaders(c *gin.Context) map[string]string {
	relevantHeaders := map[string]string{}

	// 提取关键的请求头
	headerKeys := []string{
		"Content-Type",
		"Authorization",
		"X-API-Key",
		"X-Request-ID",
		"X-Forwarded-For",
		"Accept",
		"Accept-Encoding",
	}

	for _, key := range headerKeys {
		if value := c.GetHeader(key); value != "" {
			// 对敏感信息进行脱敏处理
			if key == "Authorization" && len(value) > 20 {
				relevantHeaders[key] = value[:10] + "***" + value[len(value)-7:]
			} else if key == "X-API-Key" && len(value) > 10 {
				relevantHeaders[key] = value[:5] + "***" + value[len(value)-3:]
			} else {
				relevantHeaders[key] = value
			}
		}
	}

	return relevantHeaders
}

// handleStreamRequest 处理流式请求
// handleStreamRequest 处理流式请求
func handleStreamRequest(c *gin.Context, anthropicReq types.AnthropicRequest, authService *auth.AuthService) {
	sender := &AnthropicStreamSender{}
	handleGenericStreamRequest(c, anthropicReq, authService, sender, createAnthropicStreamEvents)
}

// handleGenericStreamRequest 通用流式请求处理
func handleGenericStreamRequest(c *gin.Context, anthropicReq types.AnthropicRequest, authService *auth.AuthService, sender StreamEventSender, eventCreator func(string, int, string) []map[string]any) {
	// 计算输入tokens（基于实际发送给上游的数据）
	estimator := utils.NewTokenEstimator()
	countReq := &types.CountTokensRequest{
		Model:    anthropicReq.Model,
		System:   anthropicReq.System,
		Messages: anthropicReq.Messages,
		Tools:    filterSupportedTools(anthropicReq.Tools), // 过滤不支持的工具后计算
	}
	inputTokens := estimator.EstimateTokens(countReq)

	// 生成消息ID并注入上下文
	messageID := fmt.Sprintf(config.MessageIDFormat, time.Now().Format(config.MessageIDTimeFormat))
	c.Set("message_id", messageID)

	// 执行CodeWhisperer请求（带重试，在 SSE 初始化之前）
	resp, tokenInfo, err := execCWRequestWithRetry(c, anthropicReq, authService, true)
	if err != nil {
		var modelNotFoundErrorType *types.ModelNotFoundErrorType
		if errors.As(err, &modelNotFoundErrorType) {
			return
		}
		// 错误已在 execCWRequestWithRetry 中处理
		return
	}
	defer resp.Body.Close()

	// 初始化SSE响应（在成功获取上游响应后）
	if err := initializeSSEResponse(c); err != nil {
		_ = sender.SendError(c, "连接不支持SSE刷新", err)
		return
	}

	// 获取 token 使用信息（用于流处理上下文）
	tokenWithUsage := &types.TokenWithUsage{
		TokenInfo:      tokenInfo,
		AvailableCount: 0, // 重试后无法准确获取，设为 0
	}

	// 创建流处理上下文
	ctx := NewStreamProcessorContext(c, anthropicReq, tokenWithUsage, sender, messageID, inputTokens)
	defer ctx.Cleanup()

	// 发送初始事件
	if err := ctx.sendInitialEvents(eventCreator); err != nil {
		return
	}

	// 处理事件流
	processor := NewEventStreamProcessor(ctx)
	if err := processor.ProcessEventStream(resp.Body); err != nil {
		if isClientDisconnectError(err) || c.Request.Context().Err() != nil {
			logger.Info("客户端已断开，结束事件流处理", addReqFields(c, logger.Err(err))...)
			return
		}
		logger.Error("事件流处理失败", logger.Err(err))
		return
	}

	// 发送结束事件
	if err := ctx.sendFinalEvents(); err != nil {
		logger.Error("发送结束事件失败", logger.Err(err))
		return
	}
}

// createAnthropicStreamEvents 创建Anthropic流式初始事件
func createAnthropicStreamEvents(messageId string, inputTokens int, model string) []map[string]any {
	// 创建基础初始事件序列，不包含content_block_start
	//
	// 关键修复：移除预先发送的空文本块
	// 问题：如果预先发送content_block_start(text)，但上游只返回tool_use没有文本，
	//      会导致空文本块（start -> stop 之间没有delta），违反Claude API规范
	//
	// 解决方案：依赖sse_state_manager.handleContentBlockDelta()中的自动启动机制
	//          只有在实际收到内容（文本或工具）时才动态生成content_block_start
	//          这确保每个content_block都有实际内容
	events := []map[string]any{
		{
			"type": "message_start",
			"message": map[string]any{
				"id":            messageId,
				"type":          "message",
				"role":          "assistant",
				"content":       []any{},
				"model":         model,
				"stop_reason":   nil,
				"stop_sequence": nil,
				"usage": map[string]any{
					"input_tokens":  inputTokens,
					"output_tokens": 0, // 初始输出tokens为0，最终在message_delta中更新
				},
			},
		},
		{
			"type": "ping",
		},
	}
	return events
}

// createAnthropicFinalEvents 创建Anthropic流式结束事件
func createAnthropicFinalEvents(outputTokens, inputTokens int, stopReason string) []map[string]any {
	// 构建符合Claude规范的完整usage信息
	usage := map[string]any{
		"output_tokens": outputTokens,
		"input_tokens":  inputTokens,
	}

	// 删除硬编码的content_block_stop，依赖sendFinalEvents的动态保护机制
	// sendFinalEvents在调用本函数前已经自动关闭所有未关闭的content_block（stream_processor.go:353-365）
	// 这样避免了重复发送content_block_stop导致的违规错误
	//
	// 三重保护机制确保不会缺失content_block_stop：
	// 1. ProcessEventStream正常转发上游的stop事件（99%场景）
	// 2. sendFinalEvents遍历所有activeBlocks并补发缺失的stop（容错机制，100%覆盖）
	// 3. handleMessageDelta在发送message_delta前的最后检查（最后保险）
	events := []map[string]any{
		{
			"type": "message_delta",
			"delta": map[string]any{
				"stop_reason":   stopReason,
				"stop_sequence": nil,
			},
			"usage": usage,
		},
		{
			"type": "message_stop",
		},
	}

	return events
}

// handleNonStreamRequest 处理非流式请求
func handleNonStreamRequest(c *gin.Context, anthropicReq types.AnthropicRequest, authService *auth.AuthService) {
	// 计算输入tokens（基于实际发送给上游的数据）
	estimator := utils.NewTokenEstimator()
	countReq := &types.CountTokensRequest{
		Model:    anthropicReq.Model,
		System:   anthropicReq.System,
		Messages: anthropicReq.Messages,
		Tools:    filterSupportedTools(anthropicReq.Tools), // 过滤不支持的工具后计算
	}
	inputTokens := estimator.EstimateTokens(countReq)

	// 执行CodeWhisperer请求（带重试）
	resp, _, err := execCWRequestWithRetry(c, anthropicReq, authService, false)
	if err != nil {
		return
	}
	defer func(Body io.ReadCloser) {
		_ = Body.Close()
	}(resp.Body)

	// 读取响应体
	body, err := utils.ReadHTTPResponse(resp.Body)
	if err != nil {
		handleResponseReadError(c, err)
		return
	}

	// 使用新的符合AWS规范的解析器，但在非流式模式下增加超时保护
	compliantParser := parser.NewCompliantEventStreamParser()
	compliantParser.SetMaxErrors(config.ParserMaxErrors) // 限制最大错误次数以防死循环

	// 为非流式解析添加超时保护（可取消，不创建额外 goroutine，避免超时后解析仍在后台运行）
	parseCtx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	result, err := compliantParser.ParseResponseWithContext(parseCtx, body)
	if err != nil {
		logger.Error("非流式解析失败",
			logger.Err(err),
			logger.String("model", anthropicReq.Model),
			logger.Int("response_size", len(body)))

		// 提供更详细的错误信息和建议
		errorResp := gin.H{
			"error":   "响应解析失败",
			"type":    "parsing_error",
			"message": "无法解析AWS CodeWhisperer响应格式",
		}

		// 根据错误类型提供不同的HTTP状态码
		statusCode := http.StatusInternalServerError
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			statusCode = http.StatusRequestTimeout
			errorResp["message"] = "请求处理超时，请稍后重试"
		}

		c.JSON(statusCode, errorResp)
		return
	}

	// 转换为Anthropic格式
	var contexts []map[string]any
	textAgg := result.GetCompletionText()

	// 先获取工具管理器的所有工具，确保sawToolUse的判断基于实际工具
	toolManager := compliantParser.GetToolManager()
	allTools := make([]*parser.ToolExecution, 0)

	// 获取活跃工具
	for _, tool := range toolManager.GetActiveTools() {
		allTools = append(allTools, tool)
	}

	// 获取已完成工具
	for _, tool := range toolManager.GetCompletedTools() {
		allTools = append(allTools, tool)
	}

	// 基于实际工具数量判断是否包含工具调用
	sawToolUse := len(allTools) > 0

	// logger.Debug("非流式响应处理完成",
	// 	addReqFields(c,
	// 		logger.String("text_content", textAgg[:utils.IntMin(config.LogPreviewMaxLength, len(textAgg))]),
	// 		logger.Int("tool_calls_count", len(allTools)),
	// 		logger.Bool("saw_tool_use", sawToolUse),
	// 	)...)

	// 添加文本内容
	if textAgg != "" {
		contexts = append(contexts, map[string]any{
			"type": "text",
			"text": textAgg,
		})
	}

	// 添加工具调用
	// 工具已经在前面从toolManager获取到allTools中
	// logger.Debug("从工具生命周期管理器获取工具调用",
	// 	logger.Int("total_tools", len(allTools)),
	// 	logger.Int("parse_result_tools", len(result.GetToolCalls())))

	for _, tool := range allTools {
		// logger.Debug("添加工具调用到响应",
		// 	logger.String("tool_id", tool.ID),
		// 	logger.String("tool_name", tool.Name),
		// 	logger.String("tool_status", tool.Status.String()),
		// 	logger.Any("tool_arguments", tool.Arguments))

		// 创建标准的tool_use块，确保包含完整的状态信息
		toolUseBlock := map[string]any{
			"type":  "tool_use",
			"id":    tool.ID,
			"name":  tool.Name,
			"input": tool.Arguments,
		}

		// 如果工具参数为空或nil，确保为空对象而不是nil
		if tool.Arguments == nil {
			toolUseBlock["input"] = map[string]any{}
		}

		// 添加详细的调试日志，验证tool_use块格式
		// if toolUseBlockJSON, err := utils.SafeMarshal(toolUseBlock); err == nil {
		// 	logger.Debug("发送给Claude CLI的tool_use块详细结构",
		// 		logger.String("tool_id", tool.ID),
		// 		logger.String("tool_name", tool.Name),
		// 		logger.String("tool_use_json", string(toolUseBlockJSON)),
		// 		logger.String("input_type", fmt.Sprintf("%T", tool.Arguments)),
		// 		logger.Any("arguments_value", tool.Arguments))
		// }

		contexts = append(contexts, toolUseBlock)

		// 记录工具调用完成状态，帮助客户端识别工具调用已完成
		// logger.Debug("工具调用已添加到响应",
		// 	logger.String("tool_id", tool.ID),
		// 	logger.String("tool_name", tool.Name))
	}

	// 使用新的stop_reason管理器，确保符合Claude官方规范
	stopReasonManager := NewStopReasonManager(anthropicReq)

	// *** 关键修复：基于实际发送给客户端的内容计算 token ***
	// 设计原则：token 计费应该基于实际下发的内容，而不是上游原始数据
	// 原因：
	// 1. 格式转换：CodeWhisperer → Claude 格式可能有差异
	// 2. 计费准确性：客户端消费的是 contexts，而不是 textAgg/allTools
	// 3. 一致性：确保 token 计算与实际响应内容完全一致
	outputTokens := 0
	for _, contentBlock := range contexts {
		blockType, _ := contentBlock["type"].(string)

		switch blockType {
		case "text":
			// 文本块：基于实际发送的文本内容
			if text, ok := contentBlock["text"].(string); ok {
				outputTokens += estimator.EstimateTextTokens(text)
			}

		case "tool_use":
			// 工具调用块：基于实际发送的工具名称和参数
			// 这里使用与 SSE 响应相同的 token 计算逻辑
			toolName, _ := contentBlock["name"].(string)
			toolInput, _ := contentBlock["input"].(map[string]any)
			outputTokens += estimator.EstimateToolUseTokens(toolName, toolInput)
		}
	}

	// 最小 token 保护：确保非空响应至少有 1 token
	if outputTokens < 1 && len(contexts) > 0 {
		outputTokens = 1
	}

	stopReasonManager.UpdateToolCallStatus(sawToolUse, sawToolUse)
	stopReason := stopReasonManager.DetermineStopReason()

	// logger.Debug("非流式响应stop_reason决策",
	// 	logger.String("stop_reason", stopReason),
	// 	logger.String("description", GetStopReasonDescription(stopReason)),
	// 	logger.Bool("saw_tool_use", sawToolUse),
	// 	logger.Int("output_tokens", outputTokens))

	anthropicResp := map[string]any{
		"content":       contexts,
		"model":         anthropicReq.Model,
		"role":          "assistant",
		"stop_reason":   stopReason,
		"stop_sequence": nil,
		"type":          "message",
		"usage": map[string]any{
			"input_tokens":  inputTokens,
			"output_tokens": outputTokens,
		},
	}

	// logger.Debug("非流式响应最终数据",
	// 	logger.String("stop_reason", stopReason),
	// 	logger.Int("content_blocks", len(contexts)))

	logger.Debug("下发非流式响应",
		addReqFields(c,
			logger.String("direction", "downstream_send"),
			logger.Any("contexts", contexts),
			logger.Bool("saw_tool_use", sawToolUse),
			logger.Int("content_count", len(contexts)),
		)...)
	c.JSON(http.StatusOK, anthropicResp)
}

// createTokenPreview 创建token预览显示格式 (***+后10位)
func createTokenPreview(token string) string {
	if len(token) <= 10 {
		// 如果token太短，全部用*代替
		return strings.Repeat("*", len(token))
	}

	// 3个*号 + 后10位
	suffix := token[len(token)-10:]
	return "***" + suffix
}

// maskEmail 对邮箱进行脱敏处理
// 规则：
// - 用户名部分：保留前2位和后2位，中间用星号替换
// - 域名部分：保留顶级域名和二级域名后缀，其他用星号替换
// 示例：
//   - caidaoli@gmail.com -> ca****li@*****.com
//   - caidaolihz888@sun.edu.pl -> ca*********88@***.**.pl
func maskEmail(email string) string {
	if email == "" {
		return ""
	}

	// 分割邮箱为用户名和域名
	parts := strings.Split(email, "@")
	if len(parts) != 2 {
		// 不是有效的邮箱格式，返回原值
		return email
	}

	username := parts[0]
	domain := parts[1]

	// 处理用户名部分：保留前2位和后2位
	var maskedUsername string
	if len(username) <= 4 {
		// 用户名太短，全部用星号替换
		maskedUsername = strings.Repeat("*", len(username))
	} else {
		prefix := username[:2]
		suffix := username[len(username)-2:]
		middleLen := len(username) - 4
		maskedUsername = prefix + strings.Repeat("*", middleLen) + suffix
	}

	// 处理域名部分：保留顶级域名和二级域名后缀
	domainParts := strings.Split(domain, ".")
	var maskedDomain string

	if len(domainParts) == 1 {
		// 只有一级域名（不常见），全部用星号替换
		maskedDomain = strings.Repeat("*", len(domain))
	} else if len(domainParts) == 2 {
		// 二级域名（如 gmail.com）
		// 主域名用星号替换，保留顶级域名
		maskedDomain = strings.Repeat("*", len(domainParts[0])) + "." + domainParts[1]
	} else {
		// 三级或更多级域名（如 sun.edu.pl）
		// 保留后两级域名，其他用星号替换
		maskedParts := make([]string, len(domainParts))
		for i := 0; i < len(domainParts)-2; i++ {
			maskedParts[i] = strings.Repeat("*", len(domainParts[i]))
		}
		// 保留最后两级
		maskedParts[len(domainParts)-2] = domainParts[len(domainParts)-2]
		maskedParts[len(domainParts)-1] = domainParts[len(domainParts)-1]
		maskedDomain = strings.Join(maskedParts, ".")
	}

	return maskedUsername + "@" + maskedDomain
}

// handleTokenPoolAPI 处理Token池API请求 - 从TokenManager缓存读取，不触发刷新
func handleTokenPoolAPI(c *gin.Context) {
	// 从context获取AuthService
	authServiceInterface, exists := c.Get("authService")
	if !exists {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "AuthService 未初��化",
		})
		return
	}

	authService, ok := authServiceInterface.(*auth.AuthService)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "AuthService 类型错误",
		})
		return
	}

	tm := authService.GetTokenManager()
	if tm == nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "TokenManager 未初始化",
		})
		return
	}

	// 从TokenManager获取所有token状态（只读，不触发刷新）
	statuses := tm.GetAllTokensStatus()

	if len(statuses) == 0 {
		c.JSON(http.StatusOK, gin.H{
			"timestamp":     time.Now().Format(time.RFC3339),
			"total_tokens":  0,
			"active_tokens": 0,
			"tokens":        []any{},
			"pool_stats": map[string]any{
				"total_tokens":  0,
				"active_tokens": 0,
			},
		})
		return
	}

	// 转换TokenStatus为前端需要的格式
	var tokenList []any
	var activeCount int

	for _, status := range statuses {
		// 构建token数据
		tokenData := map[string]any{
			"index":           status.Index,
			"token_preview":   status.RefreshToken, // 已经是脱敏后的
			"auth_type":       strings.ToLower(status.AuthType),
			"remaining_usage": status.Available,
			"is_invalid":      status.IsInvalid,
		}

		// 用户邮箱
		if status.UsageInfo != nil && status.UsageInfo.UserInfo.Email != "" {
			tokenData["user_email"] = maskEmail(status.UsageInfo.UserInfo.Email)
		} else {
			tokenData["user_email"] = "未知用户"
		}

		// 过期/重置时间逻辑：
		// 1. 如果有免费试用且状态为ACTIVE，显示试用到期时间
		// 2. 否则显示每月额度重置时间（固定每月1日）
		// quota_reset_at: 新字段，语义更准确
		// expires_at: 旧字段，保留用于向后兼容
		if status.NextResetDate != nil {
			tokenData["quota_reset_at"] = status.NextResetDate.Format(time.RFC3339)
			tokenData["expires_at"] = status.NextResetDate.Format(time.RFC3339) // 向后兼容
		} else {
			defaultResetTime := time.Now().Add(30 * 24 * time.Hour).Format(time.RFC3339)
			tokenData["quota_reset_at"] = defaultResetTime
			tokenData["expires_at"] = defaultResetTime // 向后兼容
		}

		// 最后使用时间
		if status.LastUsed != nil {
			tokenData["last_used"] = status.LastUsed.Format(time.RFC3339)
		} else {
			tokenData["last_used"] = "未使用"
		}

		// 状态判断
		if status.Disabled {
			tokenData["status"] = "disabled"
			tokenData["error"] = "配置已禁用"
		} else if status.IsInvalid {
			tokenData["status"] = "error"
			if status.InvalidatedAt != nil {
				tokenData["error"] = "Token失效于 " + status.InvalidatedAt.Format("2006-01-02 15:04:05")
			} else {
				tokenData["error"] = "Token已失效"
			}
		} else if status.Available < exhaustedThreshold {
			tokenData["status"] = "exhausted"
		} else {
			tokenData["status"] = "active"
			activeCount++
		}

		// 添加使用限制详细信息
		if status.UsageInfo != nil {
			for _, breakdown := range status.UsageInfo.UsageBreakdownList {
				if breakdown.ResourceType == "CREDIT" {
					var totalLimit float64
					var totalUsed float64

					// 基础额度
					totalLimit += breakdown.UsageLimitWithPrecision
					totalUsed += breakdown.CurrentUsageWithPrecision

					// 免费试用额度
					if breakdown.FreeTrialInfo != nil && breakdown.FreeTrialInfo.FreeTrialStatus == "ACTIVE" {
						totalLimit += breakdown.FreeTrialInfo.UsageLimitWithPrecision
						totalUsed += breakdown.FreeTrialInfo.CurrentUsageWithPrecision
					}

					tokenData["usage_limits"] = map[string]any{
						"total_limit":   totalLimit,
						"current_usage": totalUsed,
						"is_exceeded":   status.Available < exhaustedThreshold,
					}
					break
				}
			}
		}

		// 添加重置日期信息（新增）
		if status.NextResetDate != nil {
			tokenData["next_reset_date"] = status.NextResetDate.Format(time.RFC3339)
			tokenData["days_until_reset"] = status.DaysUntilReset
		}

		tokenList = append(tokenList, tokenData)
	}

	// 返回多token数据
	c.JSON(http.StatusOK, gin.H{
		"timestamp":     time.Now().Format(time.RFC3339),
		"total_tokens":  len(tokenList),
		"active_tokens": activeCount,
		"tokens":        tokenList,
		"pool_stats": map[string]any{
			"total_tokens":  len(statuses),
			"active_tokens": activeCount,
		},
	})
}

// refreshSingleTokenByConfig 根据配置刷新单个token
func refreshSingleTokenByConfig(config auth.AuthConfig) (types.TokenInfo, error) {
	switch config.AuthType {
	case auth.AuthMethodSocial:
		return auth.RefreshSocialToken(config.RefreshToken)
	case auth.AuthMethodIdC:
		return auth.RefreshIdCToken(config)
	default:
		return types.TokenInfo{}, fmt.Errorf("不支持的认证类型: %s", config.AuthType)
	}
}

// 已移除复杂的token数据收集函数，现在使用简单的内存数据读取
