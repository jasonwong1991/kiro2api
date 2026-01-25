package server

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"

	"kiro2api/auth"
	"kiro2api/config"
	"kiro2api/logger"
	"kiro2api/types"
	"kiro2api/utils"

	"github.com/gin-gonic/gin"
)

// IsWebSearchRequest 检查请求是否为纯 WebSearch 请求
// 条件：tools 有且只有一个，且为 web_search 工具
func IsWebSearchRequest(req types.AnthropicRequest) bool {
	if len(req.Tools) == 1 {
		return req.Tools[0].IsWebSearch()
	}
	return false
}

// HandleWebSearchRequest 处理 WebSearch 请求
func HandleWebSearchRequest(c *gin.Context, req types.AnthropicRequest, authService *auth.AuthService) {
	// 1. 提取搜索查询
	query := extractSearchQuery(req)
	if query == "" {
		respondError(c, http.StatusBadRequest, "无法从消息中提取搜索查询")
		return
	}

	logger.Info("处理 WebSearch 请求", logger.String("query", query))

	// 2. 创建 MCP 请求
	toolUseID, mcpReq := createMcpRequest(query)

	// 3. 调用 MCP API (带重试)
	searchResults, err := executeMCPRequestWithRetry(c, mcpReq, authService)
	if err != nil {
		logger.Warn("MCP API 调用失败", logger.Err(err))
		// 如果失败，返回空结果，让模型感知
		searchResults = nil
	}

	// 4. 计算输入 tokens (简化估算)
	estimator := utils.NewTokenEstimator()
	countReq := &types.CountTokensRequest{
		Model:    req.Model,
		System:   req.System,
		Messages: req.Messages,
		Tools:    req.Tools,
	}
	inputTokens := estimator.EstimateTokens(countReq)

	// 5. 生成并发送 SSE 事件
	if req.Stream {
		handleWebSearchStream(c, req.Model, query, toolUseID, searchResults, inputTokens)
	} else {
		// 非流式响应
		handleWebSearchNonStream(c, req.Model, query, toolUseID, searchResults, inputTokens)
	}
}

// extractSearchQuery 从消息中提取搜索查询
func extractSearchQuery(req types.AnthropicRequest) string {
	if len(req.Messages) == 0 {
		return ""
	}
	firstMsg := req.Messages[0]

	var text string
	switch v := firstMsg.Content.(type) {
	case string:
		text = v
	case []any:
		if len(v) > 0 {
			if block, ok := v[0].(map[string]any); ok {
				if t, ok := block["type"].(string); ok && t == "text" {
					if txt, ok := block["text"].(string); ok {
						text = txt
					}
				}
			}
		}
	}

	// 去除前缀 "Perform a web search for the query: "
	const prefix = "Perform a web search for the query: "
	if strings.HasPrefix(text, prefix) {
		return text[len(prefix):]
	}
	return text
}

// createMcpRequest 创建 MCP 请求
// ID 格式: web_search_tooluse_{22位随机}_{毫秒时间戳}_{8位随机}
func createMcpRequest(query string) (string, types.McpRequest) {
	random22 := generateRandomString(22, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789")
	timestamp := time.Now().UnixMilli()
	random8 := generateRandomString(8, "abcdefghijklmnopqrstuvwxyz0123456789")

	requestID := fmt.Sprintf("web_search_tooluse_%s_%d_%s", random22, timestamp, random8)

	// tool_use_id 格式: srvtoolu_{32位UUID无连字符}
	uuidStr := strings.ReplaceAll(utils.GenerateUUID(), "-", "")
	toolUseID := fmt.Sprintf("srvtoolu_%s", uuidStr[:32])

	mcpReq := types.McpRequest{
		ID:      requestID,
		JsonRpc: "2.0",
		Method:  "tools/call",
		Params: types.McpParams{
			Name: "web_search",
			Arguments: types.McpArguments{
				Query: query,
			},
		},
	}

	return toolUseID, mcpReq
}

// generateRandomString 生成指定长度的随机字符串 (使用 crypto/rand)
func generateRandomString(n int, charset string) string {
	b := make([]byte, n)
	for i := range b {
		num, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			// 回退到简单方案
			b[i] = charset[0]
			continue
		}
		b[i] = charset[num.Int64()]
	}
	return string(b)
}

// executeMCPRequestWithRetry 带重试的 MCP API 调用
func executeMCPRequestWithRetry(c *gin.Context, mcpReq types.McpRequest, authService *auth.AuthService) (*types.WebSearchResults, error) {
	var lastErr error

	for attempt := 0; attempt <= config.RetryMaxAttempts; attempt++ {
		tokenInfo, err := authService.GetToken()
		if err != nil {
			lastErr = err
			time.Sleep(config.UpstreamRetryDelay)
			continue
		}

		// 构建 URL (https://q.{region}.amazonaws.com/mcp)
		// 使用默认区域
		url := fmt.Sprintf(config.McpURLTemplate, config.DefaultMcpRegion)

		reqBody, _ := utils.SafeMarshal(mcpReq)
		req, err := http.NewRequest("POST", url, bytes.NewReader(reqBody))
		if err != nil {
			return nil, err
		}

		// 设置 Header (对齐 kiro.rs 实现)
		var userAgent, xAmzUserAgent string
		if tokenInfo.Fingerprint != nil {
			userAgent = tokenInfo.Fingerprint.UserAgent
			xAmzUserAgent = tokenInfo.Fingerprint.XAmzUserAgent
		}
		// 向后兼容：缺失时回退到即时生成的指纹
		if userAgent == "" || xAmzUserAgent == "" {
			fp := utils.GenerateFingerprint(tokenInfo.RefreshToken)
			if userAgent == "" {
				userAgent = fp.UserAgent
			}
			if xAmzUserAgent == "" {
				xAmzUserAgent = fp.XAmzUserAgent
			}
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-amz-user-agent", xAmzUserAgent)
		req.Header.Set("User-Agent", userAgent)
		req.Host = req.URL.Host // net/http 发送 Host header 使用 req.Host
		req.Header.Set("amz-sdk-invocation-id", utils.GenerateUUID())
		req.Header.Set("amz-sdk-request", "attempt=1; max=3")
		req.Header.Set("Authorization", "Bearer "+tokenInfo.AccessToken)
		req.Header.Set("Connection", "close")

		logger.Debug("发送 MCP 请求",
			logger.String("url", url),
			logger.String("request_id", mcpReq.ID))

		// 使用 token 关联的代理客户端（如果有）
		client := tokenInfo.HTTPClient
		if client == nil {
			client = utils.SharedHTTPClient
		}

		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			time.Sleep(config.UpstreamRetryDelay)
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			logger.Debug("MCP 响应", logger.String("body", string(body)))

			var mcpResp types.McpResponse
			if err := utils.SafeUnmarshal(body, &mcpResp); err != nil {
				return nil, err
			}

			if mcpResp.Error != nil {
				return nil, fmt.Errorf("MCP error: %d %s", mcpResp.Error.Code, mcpResp.Error.Message)
			}

			if mcpResp.Result == nil || len(mcpResp.Result.Content) == 0 {
				return nil, nil
			}

			content := mcpResp.Result.Content[0]
			if content.Type != "text" {
				return nil, nil
			}

			var results types.WebSearchResults
			if err := utils.SafeUnmarshal([]byte(content.Text), &results); err != nil {
				return nil, err
			}
			return &results, nil
		}

		// 错误处理
		lastErr = fmt.Errorf("MCP API status: %d", resp.StatusCode)
		if isRetryableStatusCode(resp.StatusCode) {
			time.Sleep(config.RetryDelay)
			continue
		}
		break
	}

	return nil, lastErr
}

// handleWebSearchStream 生成并发送 SSE 流
func handleWebSearchStream(c *gin.Context, model, query, toolUseID string, results *types.WebSearchResults, inputTokens int) {
	// 设置响应头
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Flush()

	sender := &AnthropicStreamSender{}
	messageID := fmt.Sprintf("msg_%s", strings.ReplaceAll(utils.GenerateUUID(), "-", "")[:24])

	// 1. message_start
	_ = sender.SendEvent(c, map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            messageID,
			"type":          "message",
			"role":          "assistant",
			"model":         model,
			"content":       []any{},
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage": map[string]any{
				"input_tokens":               inputTokens,
				"output_tokens":              0,
				"cache_creation_input_tokens": 0,
				"cache_read_input_tokens":     0,
			},
		},
	})

	// 2. content_block_start (server_tool_use) - index 0
	_ = sender.SendEvent(c, map[string]any{
		"type":  "content_block_start",
		"index": 0,
		"content_block": map[string]any{
			"id":    toolUseID,
			"type":  "server_tool_use",
			"name":  "web_search",
			"input": map[string]any{},
		},
	})

	// 3. content_block_delta (input_json_delta) - index 0
	inputJSON, _ := utils.SafeMarshal(map[string]string{"query": query})
	_ = sender.SendEvent(c, map[string]any{
		"type":  "content_block_delta",
		"index": 0,
		"delta": map[string]any{
			"type":         "input_json_delta",
			"partial_json": string(inputJSON),
		},
	})

	// 4. content_block_stop - index 0
	_ = sender.SendEvent(c, map[string]any{"type": "content_block_stop", "index": 0})

	// 5. content_block_start (web_search_tool_result) - index 1
	searchContent := buildSearchResultContent(results)
	_ = sender.SendEvent(c, map[string]any{
		"type":  "content_block_start",
		"index": 1,
		"content_block": map[string]any{
			"type":        "web_search_tool_result",
			"tool_use_id": toolUseID,
			"content":     searchContent,
		},
	})

	// 6. content_block_stop - index 1
	_ = sender.SendEvent(c, map[string]any{"type": "content_block_stop", "index": 1})

	// 7. content_block_start (text) - index 2
	_ = sender.SendEvent(c, map[string]any{
		"type":  "content_block_start",
		"index": 2,
		"content_block": map[string]any{
			"type": "text",
			"text": "",
		},
	})

	// 8. content_block_delta (text_delta) - 分块发送摘要
	summary := generateSearchSummary(query, results)
	chunkSize := 100
	runes := []rune(summary)
	for i := 0; i < len(runes); i += chunkSize {
		end := i + chunkSize
		if end > len(runes) {
			end = len(runes)
		}
		_ = sender.SendEvent(c, map[string]any{
			"type":  "content_block_delta",
			"index": 2,
			"delta": map[string]any{
				"type": "text_delta",
				"text": string(runes[i:end]),
			},
		})
	}

	// 9. content_block_stop - index 2
	_ = sender.SendEvent(c, map[string]any{"type": "content_block_stop", "index": 2})

	// 10. message_delta
	outputTokens := (len(runes) + 3) / 4 // 粗略估算
	_ = sender.SendEvent(c, map[string]any{
		"type": "message_delta",
		"delta": map[string]any{
			"stop_reason":   "end_turn",
			"stop_sequence": nil,
		},
		"usage": map[string]any{
			"output_tokens": outputTokens,
		},
	})

	// 11. message_stop
	_ = sender.SendEvent(c, map[string]any{"type": "message_stop"})
}

// handleWebSearchNonStream 处理非流式 WebSearch 响应
func handleWebSearchNonStream(c *gin.Context, model, query, toolUseID string, results *types.WebSearchResults, inputTokens int) {
	messageID := fmt.Sprintf("msg_%s", strings.ReplaceAll(utils.GenerateUUID(), "-", "")[:24])
	summary := generateSearchSummary(query, results)
	outputTokens := (len([]rune(summary)) + 3) / 4

	searchContent := buildSearchResultContent(results)

	response := map[string]any{
		"id":   messageID,
		"type": "message",
		"role": "assistant",
		"content": []any{
			map[string]any{
				"id":    toolUseID,
				"type":  "server_tool_use",
				"name":  "web_search",
				"input": map[string]any{"query": query},
			},
			map[string]any{
				"type":        "web_search_tool_result",
				"tool_use_id": toolUseID,
				"content":     searchContent,
			},
			map[string]any{
				"type": "text",
				"text": summary,
			},
		},
		"model":         model,
		"stop_reason":   "end_turn",
		"stop_sequence": nil,
		"usage": map[string]any{
			"input_tokens":  inputTokens,
			"output_tokens": outputTokens,
		},
	}

	c.JSON(http.StatusOK, response)
}

// buildSearchResultContent 构建搜索结果内容块
func buildSearchResultContent(results *types.WebSearchResults) []any {
	if results == nil {
		return []any{}
	}

	content := make([]any, 0, len(results.Results))
	for _, r := range results.Results {
		content = append(content, map[string]any{
			"type":              "web_search_result",
			"title":             r.Title,
			"url":               r.URL,
			"encrypted_content": r.Snippet,
			"page_age":          nil,
		})
	}
	return content
}

// generateSearchSummary 生成搜索结果摘要
func generateSearchSummary(query string, results *types.WebSearchResults) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Here are the search results for \"%s\":\n\n", query)

	if results != nil && len(results.Results) > 0 {
		for i, r := range results.Results {
			fmt.Fprintf(&b, "%d. **%s**\n", i+1, r.Title)
			if r.Snippet != "" {
				snippet := r.Snippet
				// 使用 runes 正确处理多字节字符
				runes := []rune(snippet)
				if len(runes) > 200 {
					snippet = string(runes[:200]) + "..."
				}
				fmt.Fprintf(&b, "   %s\n", snippet)
			}
			fmt.Fprintf(&b, "   Source: %s\n\n", r.URL)
		}
	} else {
		b.WriteString("No results found.\n")
	}
	b.WriteString("\nPlease note that these are web search results and may not be fully accurate or up-to-date.")
	return b.String()
}
