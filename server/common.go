package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"kiro2api/auth"
	"kiro2api/config"
	"kiro2api/converter"
	"kiro2api/logger"
	"kiro2api/types"
	"kiro2api/utils"

	"github.com/gin-gonic/gin"
)

// upstreamRequestTimeout 上游请求超时时间（非流式）
const upstreamNonStreamTimeout = 120 * time.Second

// upstreamStreamTimeout 上游流式请求超时时间（较长，但仍需防止无限等待）
const upstreamStreamTimeout = 5 * time.Minute

// cancelOnCloseReadCloser 确保在响应体关闭时释放 context 相关资源（timer 等）。
// 适用于：在成功路径必须保留 ctx（用于读取 resp.Body），但又不能泄漏 cancel 的场景。
type cancelOnCloseReadCloser struct {
	io.ReadCloser
	cancel context.CancelFunc
	once   sync.Once
}

func (c *cancelOnCloseReadCloser) Close() error {
	err := c.ReadCloser.Close()
	c.once.Do(func() { c.cancel() })
	return err
}

// respondErrorWithCode 标准化的错误响应结构
// 统一返回: {"error": {"message": string, "code": string}}
func respondErrorWithCode(c *gin.Context, statusCode int, code string, format string, args ...any) {
	c.JSON(statusCode, gin.H{
		"error": gin.H{
			"message": fmt.Sprintf(format, args...),
			"code":    code,
		},
	})
}

// respondError 简化封装，依据statusCode映射默认code
func respondError(c *gin.Context, statusCode int, format string, args ...any) {
	var code string
	switch statusCode {
	case http.StatusBadRequest:
		code = "bad_request"
	case http.StatusUnauthorized:
		code = "unauthorized"
	case http.StatusForbidden:
		code = "forbidden"
	case http.StatusNotFound:
		code = "not_found"
	case http.StatusTooManyRequests:
		code = "rate_limited"
	default:
		code = "internal_error"
	}
	respondErrorWithCode(c, statusCode, code, format, args...)
}

// 通用请求处理错误函数
func handleRequestBuildError(c *gin.Context, err error) {
	logger.Error("构建请求失败", addReqFields(c, logger.Err(err))...)
	respondError(c, http.StatusInternalServerError, "构建请求失败: %v", err)
}

func handleRequestSendError(c *gin.Context, err error) {
	logger.Error("发送请求失败", addReqFields(c, logger.Err(err))...)
	respondError(c, http.StatusInternalServerError, "发送请求失败: %v", err)
}

func handleResponseReadError(c *gin.Context, err error) {
	logger.Error("读取响应体失败", addReqFields(c, logger.Err(err))...)
	respondError(c, http.StatusInternalServerError, "读取响应体失败: %v", err)
}

// 通用请求执行函数
// filterSupportedTools 过滤掉不支持的工具（与上游转换逻辑保持一致）
// 设计原则：
// - DRY: 统一过滤逻辑，确保计费与上游请求一致
// - KISS: 简单直接的过滤规则
func filterSupportedTools(tools []types.AnthropicTool) []types.AnthropicTool {
	if len(tools) == 0 {
		return tools
	}

	filtered := make([]types.AnthropicTool, 0, len(tools))
	for _, tool := range tools {
		// 过滤不支持的工具：web_search（与 converter/codewhisperer.go 保持一致）
		if tool.Name == "web_search" || tool.Name == "websearch" {
			logger.Debug("过滤不支持的工具（token计算）",
				logger.String("tool_name", tool.Name))
			continue
		}
		filtered = append(filtered, tool)
	}

	return filtered
}

func executeCodeWhispererRequest(c *gin.Context, anthropicReq types.AnthropicRequest, tokenInfo types.TokenInfo, isStream bool) (*http.Response, error) {
	// 为 OpenAI 兼容端点补齐上游请求超时，避免请求悬挂占满连接池。
	parentCtx := context.Background()
	if c != nil && c.Request != nil {
		parentCtx = c.Request.Context()
	}
	var reqCtx context.Context
	var cancel context.CancelFunc
	if isStream {
		reqCtx, cancel = context.WithTimeout(parentCtx, upstreamStreamTimeout)
	} else {
		reqCtx, cancel = context.WithTimeout(parentCtx, upstreamNonStreamTimeout)
	}

	req, err := buildCodeWhispererRequestWithContext(reqCtx, c, anthropicReq, tokenInfo, isStream)
	if err != nil {
		cancel()
		// 检查是否是模型未找到错误，如果是，则响应已经发送，不需要再次处理
		if _, ok := err.(*types.ModelNotFoundErrorType); ok {
			return nil, err
		}
		handleRequestBuildError(c, err)
		return nil, err
	}

	// 使用 token 关联的代理客户端（如果有）
	client := tokenInfo.HTTPClient
	if client == nil {
		client = utils.SharedHTTPClient
	}

	resp, err := client.Do(req)
	if err != nil {
		cancel()
		handleRequestSendError(c, err)
		return nil, err
	}

	if handleCodeWhispererError(c, resp) {
		cancel()
		resp.Body.Close()
		return nil, fmt.Errorf("CodeWhisperer API error")
	}

	// 上游响应成功，记录方向与会话
	logger.Debug("上游响应成功",
		addReqFields(c,
			logger.String("direction", "upstream_response"),
			logger.Int("status_code", resp.StatusCode),
		)...)

	// 成功返回：由调用方负责 Close(resp.Body)；Close 时触发 cancel，及时释放 WithTimeout 的 timer。
	resp.Body = &cancelOnCloseReadCloser{ReadCloser: resp.Body, cancel: cancel}

	return resp, nil
}

// execCWRequest 供测试覆盖的请求执行入口（可在测试中替换）
var execCWRequest = executeCodeWhispererRequest

// isRetryableStatusCode 检查状态码是否可重试
func isRetryableStatusCode(statusCode int) bool {
	for _, code := range config.RetryableStatusCodes {
		if statusCode == code {
			return true
		}
	}
	return false
}

// executeCodeWhispererRequestWithRetry 带重试的请求执行（换 token 重试）
// 返回: response, 使用的tokenInfo, error
func executeCodeWhispererRequestWithRetry(c *gin.Context, anthropicReq types.AnthropicRequest, authService *auth.AuthService, isStream bool) (*http.Response, types.TokenInfo, error) {
	var lastErr error
	var tokenInfo types.TokenInfo

	for attempt := 0; attempt <= config.RetryMaxAttempts; attempt++ {
		// 每次尝试都获取新 token（首次或重试）
		var err error
		tokenInfo, err = authService.GetToken()
		if err != nil {
			logger.Warn("获取token失败",
				addReqFields(c,
					logger.Int("attempt", attempt),
					logger.Err(err),
				)...)
			lastErr = err

			// 快速失败：如果所有token都不可用，立即返回503，不再重试
			if authService.GetTokenManager().IsAllTokensUnavailable() {
				logger.Warn("所有token都不可用，快速失败",
					addReqFields(c,
						logger.Int("attempt", attempt),
					)...)
				respondError(c, http.StatusServiceUnavailable, "所有token暂时不可用，请稍后重试")
				return nil, tokenInfo, fmt.Errorf("all tokens unavailable")
			}

			if attempt < config.RetryMaxAttempts {
				time.Sleep(config.UpstreamRetryDelay)
				continue
			}
			respondError(c, http.StatusInternalServerError, "获取token失败: %v", err)
			return nil, tokenInfo, err
		}

		if attempt > 0 {
			logger.Info("重试请求",
				addReqFields(c,
					logger.Int("attempt", attempt),
					logger.Int("max_attempts", config.RetryMaxAttempts),
				)...)
		}

		// 构建请求（带超时控制）
		// 流式请求设置较长超时（5分钟），非流式请求设置较短超时（2分钟）
		// 注意：不使用 defer cancel()，因为在循环内会导致资源泄漏
		var reqCtx context.Context
		var cancel context.CancelFunc
		if isStream {
			reqCtx, cancel = context.WithTimeout(c.Request.Context(), upstreamStreamTimeout)
		} else {
			reqCtx, cancel = context.WithTimeout(c.Request.Context(), upstreamNonStreamTimeout)
		}

		req, err := buildCodeWhispererRequestWithContext(reqCtx, c, anthropicReq, tokenInfo, isStream)
		if err != nil {
			cancel() // 显式释放 context
			if _, ok := err.(*types.ModelNotFoundErrorType); ok {
				return nil, tokenInfo, err
			}
			// 构建错误不重试
			handleRequestBuildError(c, err)
			return nil, tokenInfo, err
		}

		// 发送请求（使用 token 关联的代理客户端）
		client := tokenInfo.HTTPClient
		if client == nil {
			client = utils.SharedHTTPClient
		}

		resp, err := client.Do(req)
		if err != nil {
			cancel() // 显式释放 context
			// 检查是否是超时错误
			if reqCtx.Err() == context.DeadlineExceeded {
				logger.Warn("上游请求超时",
					addReqFields(c,
						logger.Int("attempt", attempt),
						logger.Duration("timeout", upstreamNonStreamTimeout),
					)...)
			} else {
				logger.Warn("请求发送失败",
					addReqFields(c,
						logger.Int("attempt", attempt),
						logger.Err(err),
					)...)
			}
			lastErr = err
			if attempt < config.RetryMaxAttempts {
				time.Sleep(config.UpstreamRetryDelay)
				continue
			}
			handleRequestSendError(c, err)
			return nil, tokenInfo, err
		}

		// 检查是否可重试的错误状态码
		if isRetryableStatusCode(resp.StatusCode) && attempt < config.RetryMaxAttempts {
			cancel() // 显式释放 context
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			logger.Warn("收到可重试错误，换token重试",
				addReqFields(c,
					logger.Int("attempt", attempt),
					logger.Int("status_code", resp.StatusCode),
					logger.String("response_body", string(body)),
				)...)
			time.Sleep(config.RetryDelay)
			continue
		}

		// 不可重试或已达最大重试次数，检查错误
		if handleCodeWhispererError(c, resp) {
			cancel() // 显式释放 context
			resp.Body.Close()
			return nil, tokenInfo, fmt.Errorf("CodeWhisperer API error")
		}

		// 成功 - 注意：成功时不调用 cancel()，因为 resp.Body 仍需使用 context
		// cancel 会在调用方处理完 response 后由 context 的父级取消或超时自动清理
		logger.Debug("上游响应成功",
			addReqFields(c,
				logger.String("direction", "upstream_response"),
				logger.Int("status_code", resp.StatusCode),
				logger.Int("attempts", attempt+1),
			)...)

		// 成功返回：由调用方负责 Close(resp.Body)；Close 时触发 cancel，及时释放 WithTimeout 的 timer。
		resp.Body = &cancelOnCloseReadCloser{ReadCloser: resp.Body, cancel: cancel}

		return resp, tokenInfo, nil
	}

	// 理论上不会到达这里
	respondError(c, http.StatusInternalServerError, "请求失败: %v", lastErr)
	return nil, tokenInfo, lastErr
}

// execCWRequestWithRetry 供测试覆盖的带重试请求执行入口
var execCWRequestWithRetry = executeCodeWhispererRequestWithRetry

// buildCodeWhispererRequest 构建通用的CodeWhisperer请求（向后兼容）
func buildCodeWhispererRequest(c *gin.Context, anthropicReq types.AnthropicRequest, tokenInfo types.TokenInfo, isStream bool) (*http.Request, error) {
	return buildCodeWhispererRequestWithContext(c.Request.Context(), c, anthropicReq, tokenInfo, isStream)
}

// buildCodeWhispererRequestWithContext 构建带context的CodeWhisperer请求
func buildCodeWhispererRequestWithContext(ctx context.Context, c *gin.Context, anthropicReq types.AnthropicRequest, tokenInfo types.TokenInfo, isStream bool) (*http.Request, error) {
	cwReq, err := converter.BuildCodeWhispererRequest(anthropicReq, c)
	if err != nil {
		// 检查是否是模型未找到错误
		if modelNotFoundErr, ok := err.(*types.ModelNotFoundErrorType); ok {
			// 直接返回用户期望的JSON格式
			c.JSON(http.StatusBadRequest, modelNotFoundErr.ErrorData)
			return nil, err
		}
		return nil, fmt.Errorf("构建CodeWhisperer请求失败: %v", err)
	}

	// 关键兼容性字段：Kiro App 会在顶层携带 profileArn；缺失时上游可能返回 400（Improperly formed request）
	if tokenInfo.ProfileArn != "" {
		cwReq.ProfileArn = tokenInfo.ProfileArn
	}
	// 注意：profileArn 为空是正常情况，不需要警告

	cwReqBody, err := utils.SafeMarshal(cwReq)
	if err != nil {
		return nil, fmt.Errorf("序列化请求失败: %v", err)
	}

	// 临时调试：记录发送给CodeWhisperer的请求内容
	// 补充：当工具直传启用时输出工具名称预览
	var toolNamesPreview string
	if len(cwReq.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.Tools) > 0 {
		names := make([]string, 0, len(cwReq.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.Tools))
		for _, t := range cwReq.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.Tools {
			if t.ToolSpecification.Name != "" {
				names = append(names, t.ToolSpecification.Name)
			}
		}
		toolNamesPreview = strings.Join(names, ",")
	}

	logger.Debug("发送给CodeWhisperer的请求",
		logger.String("direction", "upstream_request"),
		logger.Int("request_size", len(cwReqBody)),
		logger.String("request_body", string(cwReqBody)),
		logger.Int("tools_count", len(cwReq.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.Tools)),
		logger.String("tools_names", toolNamesPreview))

	// 存储请求体到 Context，用于错误调试
	if c != nil {
		c.Set("cw_request_body", cwReqBody)
	}

	// 使用带context的请求创建，支持超时控制
	req, err := http.NewRequestWithContext(ctx, "POST", config.CodeWhispererURL, bytes.NewReader(cwReqBody))
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %v", err)
	}

	req.Header.Set("Authorization", "Bearer "+tokenInfo.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	if isStream {
		req.Header.Set("Accept", "text/event-stream")
	}

	// 添加上游请求必需的header (使用账号专属的设备指纹)
	var userAgent, xAmzUserAgent, agentMode string
	if tokenInfo.Fingerprint != nil && tokenInfo.Fingerprint.UserAgent != "" {
		userAgent = tokenInfo.Fingerprint.UserAgent
		xAmzUserAgent = tokenInfo.Fingerprint.XAmzUserAgent
		agentMode = tokenInfo.Fingerprint.KiroAgentMode
	} else {
		// 如果没有指纹信息，临时生成（向后兼容）
		fp := utils.GenerateFingerprint(tokenInfo.RefreshToken)
		userAgent = fp.UserAgent
		xAmzUserAgent = fp.XAmzUserAgent
		agentMode = fp.KiroAgentMode
	}

	req.Header.Set("x-amzn-kiro-agent-mode", agentMode)
	req.Header.Set("x-amz-user-agent", xAmzUserAgent)
	req.Header.Set("user-agent", userAgent)

	// 添加真实 Kiro 客户端必需的 Header
	req.Header.Set("x-amzn-codewhisperer-optout", "true")
	req.Header.Set("amz-sdk-invocation-id", utils.GenerateUUID())
	req.Header.Set("amz-sdk-request", "attempt=1; max=3")

	return req, nil
}

// handleCodeWhispererError 处理CodeWhisperer API错误响应 (重构后符合SOLID原则)
func handleCodeWhispererError(c *gin.Context, resp *http.Response) bool {
	if resp.StatusCode == http.StatusOK {
		return false
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Error("读取错误响应失败",
			addReqFields(c,
				logger.String("direction", "upstream_response"),
				logger.Err(err),
			)...)
		respondError(c, http.StatusInternalServerError, "%s", "读取响应失败")
		return true
	}

	logger.Error("上游响应错误",
		addReqFields(c,
			logger.String("direction", "upstream_response"),
			logger.Int("status_code", resp.StatusCode),
			logger.Int("response_len", len(body)),
			logger.String("response_body", string(body)),
		)...)

	// 检测 "Improperly formed request" 错误并保存调试信息
	if strings.Contains(string(body), "Improperly formed request") {
		saveImproperlyFormedRequestDebug(c, body)
	}

	// 特殊处理：403错误表示token失效 (保持向后兼容)
	if resp.StatusCode == http.StatusForbidden {
		logger.Warn("收到403错误，token可能已失效")
		respondErrorWithCode(c, http.StatusUnauthorized, "unauthorized", "%s", "Token已失效，请重试")
		return true
	}

	// *** 新增：使用错误映射器处理错误，符合Claude API规范 ***
	errorMapper := NewErrorMapper()
	claudeError := errorMapper.MapCodeWhispererError(resp.StatusCode, body)

	// 根据映射结果发送符合Claude规范的响应
	if claudeError.ShouldTriggerCompaction {
		// CONTENT_LENGTH_EXCEEDS_THRESHOLD -> 返回高 input_tokens 的成功响应
		// 触发 Claude Code 自动压缩机制
		logger.Info("内容长度超限，返回高input_tokens触发Claude Code自动压缩",
			addReqFields(c,
				logger.String("upstream_reason", "CONTENT_LENGTH_EXCEEDS_THRESHOLD"),
				logger.String("strategy", "trigger_compaction"),
			)...)
		errorMapper.SendClaudeError(c, claudeError)
	} else if claudeError.ShouldReturn400 {
		// CONTENT_LENGTH_EXCEEDS_THRESHOLD -> invalid_request_error (400)
		// 注意：这不会自动触发压缩，用户需要手动处理
		logger.Warn("内容长度超限，返回invalid_request_error",
			addReqFields(c,
				logger.String("upstream_reason", "CONTENT_LENGTH_EXCEEDS_THRESHOLD"),
				logger.String("error_type", claudeError.ErrorType),
			)...)
		errorMapper.SendClaudeError(c, claudeError)
	} else if claudeError.StopReason == "max_tokens" {
		// 其他max_tokens情况（保留向后兼容）
		logger.Info("映射为max_tokens stop_reason",
			addReqFields(c,
				logger.String("claude_stop_reason", "max_tokens"),
			)...)
		errorMapper.SendClaudeError(c, claudeError)
	} else {
		// 其他错误使用传统方式处理 (向后兼容)
		respondErrorWithCode(c, http.StatusInternalServerError, "cw_error", "CodeWhisperer Error: %s", string(body))
	}

	return true
}

// StreamEventSender 统一的流事件发送接口
type StreamEventSender interface {
	SendEvent(c *gin.Context, data any) error
	SendError(c *gin.Context, message string, err error) error
}

// AnthropicStreamSender Anthropic格式的流事件发送器
type AnthropicStreamSender struct{}

func (s *AnthropicStreamSender) SendEvent(c *gin.Context, data any) error {
	var eventType string

	if dataMap, ok := data.(map[string]any); ok {
		if t, exists := dataMap["type"]; exists {
			eventType = t.(string)
		}

	}

	json, err := utils.SafeMarshal(data)
	if err != nil {
		return err
	}

	// 压缩日志：仅记录事件类型与负载长度
	logger.Debug("发送SSE事件",
		addReqFields(c,
			// logger.String("direction", "downstream_send"),
			logger.String("event", eventType),
			// logger.Int("payload_len", len(json)),
			logger.String("payload_preview", string(json)),
		)...)

	fmt.Fprintf(c.Writer, "event: %s\n", eventType)
	fmt.Fprintf(c.Writer, "data: %s\n\n", string(json))
	c.Writer.Flush()
	return nil
}

func (s *AnthropicStreamSender) SendError(c *gin.Context, message string, _ error) error {
	errorResp := map[string]any{
		"type": "error",
		"error": map[string]any{
			"type":    "overloaded_error",
			"message": message,
		},
	}
	return s.SendEvent(c, errorResp)
}

// OpenAIStreamSender OpenAI格式的流事件发送器
type OpenAIStreamSender struct{}

func (s *OpenAIStreamSender) SendEvent(c *gin.Context, data any) error {

	json, err := utils.SafeMarshal(data)
	if err != nil {
		return err
	}

	// 压缩日志：记录负载长度
	logger.Debug("发送OpenAI SSE事件",
		addReqFields(c,
			logger.String("direction", "downstream_send"),
			logger.Int("payload_len", len(json)),
		)...)

	fmt.Fprintf(c.Writer, "data: %s\n\n", string(json))
	c.Writer.Flush()
	return nil
}

func (s *OpenAIStreamSender) SendError(c *gin.Context, message string, _ error) error {
	errorResp := map[string]any{
		"error": map[string]any{
			"message": message,
			"type":    "server_error",
			"code":    "internal_error",
		},
	}

	json, err := utils.FastMarshal(errorResp)
	if err != nil {
		return err
	}

	fmt.Fprintf(c.Writer, "data: %s\n\n", string(json))
	c.Writer.Flush()
	return nil
}

// RequestContext 请求处理上下文，封装通用的请求处理逻辑
type RequestContext struct {
	GinContext  *gin.Context
	AuthService interface {
		GetToken() (types.TokenInfo, error)
		GetTokenWithUsage() (*types.TokenWithUsage, error)
	}
	RequestType string // "anthropic" 或 "openai"
}

// GetTokenAndBody 通用的token获取和请求体读取
// 返回: tokenInfo, requestBody, error
func (rc *RequestContext) GetTokenAndBody() (types.TokenInfo, []byte, error) {
	// 获取token
	tokenInfo, err := rc.AuthService.GetToken()
	if err != nil {
		logger.Error("获取token失败", logger.Err(err))
		respondError(rc.GinContext, http.StatusInternalServerError, "获取token失败: %v", err)
		return types.TokenInfo{}, nil, err
	}

	// 读取请求体
	body, err := rc.GinContext.GetRawData()
	if err != nil {
		// 区分客户端断开连接和其他错误
		if err == io.EOF || err.Error() == "unexpected EOF" || strings.Contains(err.Error(), "connection reset") {
			logger.Warn("客户端连接中断", logger.Err(err), logger.String("client_ip", rc.GinContext.ClientIP()))
		} else {
			logger.Error("读取请求体失败", logger.Err(err))
		}
		respondError(rc.GinContext, http.StatusBadRequest, "读取请求体失败: %v", err)
		return types.TokenInfo{}, nil, err
	}

	// 记录请求日志
	logger.Debug(fmt.Sprintf("收到%s请求", rc.RequestType),
		addReqFields(rc.GinContext,
			logger.String("direction", "client_request"),
			logger.String("body", string(body)),
			logger.Int("body_size", len(body)),
			logger.String("remote_addr", rc.GinContext.ClientIP()),
			logger.String("user_agent", rc.GinContext.GetHeader("User-Agent")),
		)...)

	return tokenInfo, body, nil
}

// GetTokenWithUsageAndBody 获取token（包含使用信息）和请求体
// 返回: tokenWithUsage, requestBody, error
func (rc *RequestContext) GetTokenWithUsageAndBody() (*types.TokenWithUsage, []byte, error) {
	// 获取token（包含使用信息）
	tokenWithUsage, err := rc.AuthService.GetTokenWithUsage()
	if err != nil {
		logger.Error("获取token失败", logger.Err(err))
		respondError(rc.GinContext, http.StatusInternalServerError, "获取token失败: %v", err)
		return nil, nil, err
	}

	// 读取请求体
	body, err := rc.GinContext.GetRawData()
	if err != nil {
		// 区分客户端断开连接和其他错误
		if err == io.EOF || err.Error() == "unexpected EOF" || strings.Contains(err.Error(), "connection reset") {
			logger.Warn("客户端连接中断", logger.Err(err), logger.String("client_ip", rc.GinContext.ClientIP()))
		} else {
			logger.Error("读取请求体失败", logger.Err(err))
		}
		respondError(rc.GinContext, http.StatusBadRequest, "读取请求体失败: %v", err)
		return nil, nil, err
	}

	// 记录请求日志
	logger.Debug(fmt.Sprintf("收到%s请求", rc.RequestType),
		addReqFields(rc.GinContext,
			logger.String("direction", "client_request"),
			logger.String("body", string(body)),
			logger.Int("body_size", len(body)),
			logger.String("remote_addr", rc.GinContext.ClientIP()),
			logger.String("user_agent", rc.GinContext.GetHeader("User-Agent")),
			logger.Float64("available_count", tokenWithUsage.AvailableCount),
		)...)

	return tokenWithUsage, body, nil
}

// saveImproperlyFormedRequestDebug 保存 "Improperly formed request" 错误的调试信息
// 需设置环境变量 DEBUG_LOG_DIR 指定保存目录，未设置则跳过保存
func saveImproperlyFormedRequestDebug(c *gin.Context, errorBody []byte) {
	debugDir := os.Getenv("DEBUG_LOG_DIR")
	if debugDir == "" {
		logger.Warn("DEBUG_LOG_DIR 未设置，跳过保存调试文件")
		return
	}

	// 确保目录存在
	if err := os.MkdirAll(debugDir, 0755); err != nil {
		logger.Error("创建调试目录失败", logger.Err(err), logger.String("dir", debugDir))
		return
	}

	timestamp := time.Now().Format("20060102_150405")
	filename := fmt.Sprintf("%s/debug_improperly_formed_%s.json", debugDir, timestamp)

	var requestBody []byte
	if val, exists := c.Get("cw_request_body"); exists {
		requestBody, _ = val.([]byte)
	}

	debugData := map[string]any{
		"timestamp":     time.Now().Format(time.RFC3339),
		"error":         string(errorBody),
		"request_body":  string(requestBody),
		"request_path":  c.Request.URL.Path,
		"request_model": c.GetString("model"),
	}

	data, err := json.MarshalIndent(debugData, "", "  ")
	if err != nil {
		logger.Error("序列化调试数据失败", logger.Err(err))
		return
	}

	if err := os.WriteFile(filename, data, 0644); err != nil {
		logger.Error("保存调试文件失败", logger.Err(err), logger.String("filename", filename))
		return
	}

	logger.Warn("已保存 Improperly formed request 调试信息", logger.String("filename", filename))
}
