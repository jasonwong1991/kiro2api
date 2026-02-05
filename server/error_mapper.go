package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"kiro2api/logger"
)

// isClaudeCodeRequest 检测请求是否来自 Claude Code
// 检测条件：URL 参数包含 beta=true 或 User-Agent 包含 claude-cli
func isClaudeCodeRequest(c *gin.Context) bool {
	// 检查 URL 参数 beta=true
	if c.Query("beta") == "true" {
		return true
	}

	// 检查 User-Agent 是否包含 claude-cli
	userAgent := c.GetHeader("User-Agent")
	if strings.Contains(strings.ToLower(userAgent), "claude-cli") {
		return true
	}

	return false
}

// ErrorMappingStrategy 错误映射策略接口 (DIP原则)
type ErrorMappingStrategy interface {
	MapError(c *gin.Context, statusCode int, responseBody []byte) (*ClaudeErrorResponse, bool)
	GetErrorType() string
}

// ClaudeErrorResponse Claude API规范的错误响应结构
type ClaudeErrorResponse struct {
	Type                   string `json:"type"`
	Message                string `json:"message"`
	ErrorType              string `json:"error_type,omitempty"`    // Anthropic API错误类型（如invalid_request_error）
	StopReason             string `json:"stop_reason,omitempty"`   // 用于内容长度超限等情况
	ShouldReturn400        bool   `json:"-"`                       // 标记是否应返回400状态码（内部字段）
	ShouldTriggerCompaction bool  `json:"-"`                       // 标记是否应返回触发压缩的成功响应（内部字段）
}

// CodeWhispererErrorBody AWS CodeWhisperer错误响应体
type CodeWhispererErrorBody struct {
	Message string `json:"message"`
	Reason  string `json:"reason"`
}

// ContentLengthExceedsStrategy 内容长度超限错误映射策略 (SRP原则)
type ContentLengthExceedsStrategy struct{}

func (s *ContentLengthExceedsStrategy) MapError(c *gin.Context, statusCode int, responseBody []byte) (*ClaudeErrorResponse, bool) {
	if statusCode != http.StatusBadRequest {
		return nil, false
	}

	var errorBody CodeWhispererErrorBody
	if err := json.Unmarshal(responseBody, &errorBody); err != nil {
		return nil, false
	}

	// 检查是否为内容长度超限错误
	if errorBody.Reason == "CONTENT_LENGTH_EXCEEDS_THRESHOLD" {
		// 检测是否为 Claude Code 请求
		if isClaudeCodeRequest(c) {
			// Claude Code 请求：返回高 input_tokens 触发自动压缩
			logger.Info("检测到 Claude Code 请求，启用自动压缩",
				addReqFields(c,
					logger.String("user_agent", c.GetHeader("User-Agent")),
					logger.String("beta_param", c.Query("beta")))...)

			return &ClaudeErrorResponse{
				Type:                    "message",
				Message:                 "Context limit reached. Auto-compacting conversation history...",
				StopReason:              "end_turn",
				ShouldTriggerCompaction: true,
			}, true
		}

		// 非 Claude Code 请求：返回标准错误
		logger.Info("检测到非 Claude Code 请求，返回标准错误",
			addReqFields(c,
				logger.String("user_agent", c.GetHeader("User-Agent")))...)

		return &ClaudeErrorResponse{
			Type:      "error",
			ErrorType: "invalid_request_error",
			Message:   "Input is too long. Please reduce the length of your messages or conversation history.",
			ShouldReturn400: true,
		}, true
	}

	return nil, false
}

func (s *ContentLengthExceedsStrategy) GetErrorType() string {
	return "content_length_exceeds"
}

// DefaultErrorStrategy 默认错误映射策略 (YAGNI原则)
type DefaultErrorStrategy struct{}

func (s *DefaultErrorStrategy) MapError(c *gin.Context, statusCode int, responseBody []byte) (*ClaudeErrorResponse, bool) {
	return &ClaudeErrorResponse{
		Type:    "error",
		Message: fmt.Sprintf("Upstream error: %s", string(responseBody)),
	}, true
}

func (s *DefaultErrorStrategy) GetErrorType() string {
	return "default"
}

// ErrorMapper 错误映射器 (Strategy Pattern + Factory Pattern)
type ErrorMapper struct {
	strategies []ErrorMappingStrategy
}

// NewErrorMapper 创建错误映射器 (Factory Pattern)
func NewErrorMapper() *ErrorMapper {
	return &ErrorMapper{
		strategies: []ErrorMappingStrategy{
			&ContentLengthExceedsStrategy{}, // 优先处理特定错误
			&DefaultErrorStrategy{},         // 默认处理器
		},
	}
}

// MapCodeWhispererError 映射CodeWhisperer错误到Claude格式 (Template Method Pattern)
func (em *ErrorMapper) MapCodeWhispererError(c *gin.Context, statusCode int, responseBody []byte) *ClaudeErrorResponse {
	// 依次尝试各种映射策略
	for _, strategy := range em.strategies {
		if response, handled := strategy.MapError(c, statusCode, responseBody); handled {
			logger.Debug("错误映射成功",
				logger.String("strategy", strategy.GetErrorType()),
				logger.Int("status_code", statusCode),
				logger.String("mapped_type", response.Type),
				logger.String("stop_reason", response.StopReason))
			return response
		}
	}

	// 理论上不会到达这里，因为DefaultErrorStrategy总是返回true
	return &ClaudeErrorResponse{
		Type:    "error",
		Message: "Unknown error",
	}
}

// SendClaudeError 发送Claude规范的错误响应 (KISS原则)
func (em *ErrorMapper) SendClaudeError(c *gin.Context, claudeError *ClaudeErrorResponse) {
	// 优先检查是否应返回触发压缩的成功响应
	if claudeError.ShouldTriggerCompaction {
		em.sendCompactionTriggerResponse(c, claudeError)
		return
	}

	// 如果标记为需要返回400状态码（如内容过长错误），返回JSON错误
	if claudeError.ShouldReturn400 {
		em.sendInvalidRequestError(c, claudeError)
		return
	}

	// 根据错误类型决定发送格式
	if claudeError.StopReason == "max_tokens" {
		// 发送message_delta事件，符合Claude规范
		em.sendMaxTokensResponse(c, claudeError)
	} else {
		// 发送标准错误事件
		em.sendStandardError(c, claudeError)
	}
}

// sendInvalidRequestError 发送invalid_request_error类型的400错误响应 (SRP原则)
// 注意：这不会触发Claude Code自动压缩，用户需要手动运行/compact或开始新任务
// Claude Code的自动压缩是基于客户端上下文监控（约95%阈值），而非API错误响应
func (em *ErrorMapper) sendInvalidRequestError(c *gin.Context, claudeError *ClaudeErrorResponse) {
	errorResp := map[string]any{
		"type": "error",
		"error": map[string]any{
			"type":    claudeError.ErrorType,
			"message": claudeError.Message,
		},
	}

	c.JSON(http.StatusBadRequest, errorResp)

	logger.Warn("已发送invalid_request_error响应，用户需要手动压缩上下文或开始新任务",
		addReqFields(c,
			logger.String("error_type", claudeError.ErrorType),
			logger.String("message", claudeError.Message))...)
}

// sendCompactionTriggerResponse 发送触发压缩的成功响应 (SRP原则)
// 通过返回高 input_tokens 值触发 Claude Code 自动执行上下文压缩
// 原理：Claude Code 基于累计 input_tokens 监控上下文使用率（约95%阈值）
func (em *ErrorMapper) sendCompactionTriggerResponse(c *gin.Context, claudeError *ClaudeErrorResponse) {
	// Claude 3.5 Sonnet 的上下文限制是 200,000 tokens
	// 返回 195,000 input_tokens (97.5%) 应该足以触发自动压缩
	const triggerInputTokens = 195000

	// 构造成功响应，包含提示消息
	response := map[string]any{
		"id":      generateMessageID(),
		"type":    "message",
		"role":    "assistant",
		"model":   "claude-sonnet-4-20250514", // 使用默认模型
		"content": []map[string]any{
			{
				"type": "text",
				"text": claudeError.Message, // "Context limit reached. Auto-compacting conversation history..."
			},
		},
		"stop_reason":   claudeError.StopReason, // "end_turn"
		"stop_sequence": nil,
		"usage": map[string]any{
			"input_tokens":  triggerInputTokens, // 关键：高 input_tokens 触发压缩
			"output_tokens": len(claudeError.Message) / 4, // 简单估算输出 tokens
		},
	}

	c.JSON(http.StatusOK, response)

	logger.Info("已发送触发压缩的成功响应，Claude Code应自动执行/compact",
		addReqFields(c,
			logger.Int("input_tokens", triggerInputTokens),
			logger.String("message", claudeError.Message))...)
}

// generateMessageID 生成消息ID
func generateMessageID() string {
	// 生成类似 msg_xxx 的ID
	return fmt.Sprintf("msg_%s", randomString(24))
}

// randomString 生成指定长度的随机字符串
func randomString(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	result := make([]byte, length)
	for i := range result {
		result[i] = charset[i%len(charset)]
	}
	return string(result)
}



// sendMaxTokensResponse 发送max_tokens类型的响应 (SRP原则)
func (em *ErrorMapper) sendMaxTokensResponse(c *gin.Context, claudeError *ClaudeErrorResponse) {
	// 按照Anthropic规范，当内容长度超限时，应该发送一个带有stop_reason: max_tokens的message_delta事件
	response := map[string]any{
		"type": "message_delta",
		"delta": map[string]any{
			"stop_reason":   "max_tokens",
			"stop_sequence": nil,
		},
		"usage": map[string]any{
			"input_tokens":  0, // 实际项目中应该从请求中获取
			"output_tokens": 0,
		},
	}

	// 发送SSE事件
	sender := &AnthropicStreamSender{}
	if err := sender.SendEvent(c, response); err != nil {
		logger.Error("发送max_tokens响应失败",
			logger.Err(err),
			logger.String("original_message", claudeError.Message))
	}

	logger.Info("已发送max_tokens stop_reason响应",
		addReqFields(c,
			logger.String("stop_reason", "max_tokens"),
			logger.String("original_message", claudeError.Message))...)
}

// sendStandardError 发送标准错误响应 (SRP原则)
func (em *ErrorMapper) sendStandardError(c *gin.Context, claudeError *ClaudeErrorResponse) {
	errorResp := map[string]any{
		"type": "error",
		"error": map[string]any{
			"type":    "overloaded_error",
			"message": claudeError.Message,
		},
	}

	sender := &AnthropicStreamSender{}
	if err := sender.SendEvent(c, errorResp); err != nil {
		logger.Error("发送标准错误响应失败", logger.Err(err))
	}
}
