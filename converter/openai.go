package converter

import (
	"fmt"
	"strings"
	"time"

	"kiro2api/types"
	"kiro2api/utils"
)

// OpenAI格式转换器

// ConvertOpenAIToAnthropic 将OpenAI请求转换为Anthropic请求
func ConvertOpenAIToAnthropic(openaiReq types.OpenAIRequest) types.AnthropicRequest {
	var anthropicMessages []types.AnthropicRequestMessage
	var systemMessages []types.AnthropicSystemMessage

	// 转换消息，区分 system 消息和其他消息
	for _, msg := range openaiReq.Messages {
		// 转换消息内容格式
		convertedContent, err := convertOpenAIContentToAnthropic(msg.Content)
		if err != nil {
			// 如果转换失败，记录错误但使用原始内容继续处理
			convertedContent = msg.Content
		}

		// 如果是 system 角色，添加到 systemMessages
		if msg.Role == "system" {
			// 将 system 消息内容提取为字符串
			var systemContent string
			switch v := convertedContent.(type) {
			case string:
				systemContent = v
			default:
				// 如果不是字符串，使用 GetMessageContent 提取
				systemContent, err = utils.GetMessageContent(v)
				if err != nil {
					// 如果提取失败，尝试简单的字符串转换
					systemContent = fmt.Sprintf("%v", v)
				}
			}

			systemMessages = append(systemMessages, types.AnthropicSystemMessage{
				Type: "text",
				Text: systemContent,
			})
		} else {
			// 非 system 消息添加到普通消息列表
			anthropicMsg := types.AnthropicRequestMessage{
				Role:    msg.Role,
				Content: convertedContent,
			}
			anthropicMessages = append(anthropicMessages, anthropicMsg)
		}
	}

	// 设置默认值
	maxTokens := 16384
	if openaiReq.MaxTokens != nil {
		maxTokens = *openaiReq.MaxTokens
	}

	// 为了增强兼容性，当stream未设置时默认为false（非流式响应）
	// 这样可以避免客户端在处理函数调用时的解析问题
	stream := false
	if openaiReq.Stream != nil {
		stream = *openaiReq.Stream
	}

	anthropicReq := types.AnthropicRequest{
		Model:     openaiReq.Model,
		MaxTokens: maxTokens,
		Messages:  anthropicMessages,
		System:    systemMessages,
		Stream:    stream,
	}

	if openaiReq.Temperature != nil {
		anthropicReq.Temperature = openaiReq.Temperature
	}

	// 转换 reasoning_effort 为 thinking 配置
	if openaiReq.ReasoningEffort != "" {
		anthropicReq.Thinking = ConvertReasoningEffortToThinking(openaiReq.ReasoningEffort)
	}

	// 检测 -thinking 后缀模型，自动启用 thinking 模式
	if strings.HasSuffix(openaiReq.Model, "-thinking") && anthropicReq.Thinking == nil {
		anthropicReq.Thinking = map[string]any{
			"type":          "enabled",
			"budget_tokens": DefaultMaxThinkingLength,
		}
	}

	// 转换 tools
	if len(openaiReq.Tools) > 0 {
		anthropicTools, err := validateAndProcessTools(openaiReq.Tools)
		if err != nil {
			// 记录警告但不中断处理，允许部分工具失败
			// 可以考虑返回错误，取决于业务需求
			// 这里参考server.py的做法，记录错误但继续处理有效的工具
		}
		anthropicReq.Tools = anthropicTools
	}

	// 转换 tool_choice
	if openaiReq.ToolChoice != nil {
		anthropicReq.ToolChoice = convertOpenAIToolChoiceToAnthropic(openaiReq.ToolChoice)
	}

	return anthropicReq
}

// ConvertAnthropicToOpenAI 将Anthropic响应转换为OpenAI响应
func ConvertAnthropicToOpenAI(anthropicResp map[string]any, model string, messageId string) types.OpenAIResponse {
	content := ""
	var toolCalls []types.OpenAIToolCall
	finishReason := "stop"

	// 首先尝试[]any类型断言
	if contentArray, ok := anthropicResp["content"].([]any); ok && len(contentArray) > 0 {
		// 遍历所有content blocks
		var textParts []string
		for _, block := range contentArray {
			if textBlock, ok := block.(map[string]any); ok {
				if blockType, ok := textBlock["type"].(string); ok {
					switch blockType {
					case "text":
						if text, ok := textBlock["text"].(string); ok {
							textParts = append(textParts, text)
						}
					case "tool_use":
						finishReason = "tool_calls"
						if toolUseId, ok := textBlock["id"].(string); ok {
							if toolName, ok := textBlock["name"].(string); ok {
								if input, ok := textBlock["input"]; ok {
									inputJson, _ := utils.SafeMarshal(input)
									toolCall := types.OpenAIToolCall{
										ID:   toolUseId,
										Type: "function",
										Function: types.OpenAIToolFunction{
											Name:      toolName,
											Arguments: string(inputJson),
										},
									}
									toolCalls = append(toolCalls, toolCall)
								}
							}
						}
					}
				}
			}
		}
		content = strings.Join(textParts, "")
	} else if contentSlice, ok := anthropicResp["content"].([]map[string]any); ok && len(contentSlice) > 0 {
		// 尝试[]map[string]any类型断言
		var textParts []string
		for _, textBlock := range contentSlice {
			if blockType, ok := textBlock["type"].(string); ok {
				switch blockType {
				case "text":
					if text, ok := textBlock["text"].(string); ok {
						textParts = append(textParts, text)
					}
				case "tool_use":
					finishReason = "tool_calls"
					if toolUseId, ok := textBlock["id"].(string); ok {
						if toolName, ok := textBlock["name"].(string); ok {
							if input, ok := textBlock["input"]; ok {
								inputJson, _ := utils.SafeMarshal(input)
								toolCall := types.OpenAIToolCall{
									ID:   toolUseId,
									Type: "function",
									Function: types.OpenAIToolFunction{
										Name:      toolName,
										Arguments: string(inputJson),
									},
								}
								toolCalls = append(toolCalls, toolCall)
							}
						}
					}
				}
			}
		}
		content = strings.Join(textParts, "")
	}

	// 计算token使用量
	promptTokens := 0
	completionTokens := len(content) / 4 // 简单估算
	if usage, ok := anthropicResp["usage"].(map[string]any); ok {
		if inputTokens, ok := usage["input_tokens"].(int); ok {
			promptTokens = inputTokens
		}
		if outputTokens, ok := usage["output_tokens"].(int); ok {
			completionTokens = outputTokens
		}
	}

	message := types.OpenAIMessage{
		Role:    "assistant",
		Content: content,
	}

	// 只有当有tool_calls时才添加ToolCalls字段
	if len(toolCalls) > 0 {
		message.ToolCalls = toolCalls
	}

	return types.OpenAIResponse{
		ID:      messageId,
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []types.OpenAIChoice{
			{
				Index:        0,
				Message:      message,
				FinishReason: finishReason,
			},
		},
		Usage: types.Usage{
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
			TotalTokens:      promptTokens + completionTokens,
		},
	}
}
