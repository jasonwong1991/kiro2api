package converter

import (
	"fmt"
	"strings"

	"kiro2api/config"
	"kiro2api/logger"
	"kiro2api/types"
	"kiro2api/utils"

	"github.com/gin-gonic/gin"
)

// ValidateAssistantResponseEvent 验证助手响应事件
// ConvertToAssistantResponseEvent 转换任意数据为标准的AssistantResponseEvent
// NormalizeAssistantResponseEvent 标准化助手响应事件（填充默认值等）
// normalizeWebLinks 标准化网页链接
// normalizeReferences 标准化引用
// CodeWhisperer格式转换器

// determineChatTriggerType 智能确定聊天触发类型 (SOLID-SRP: 单一责任)
func determineChatTriggerType(anthropicReq types.AnthropicRequest) string {
	// CodeWhisperer API 只支持 MANUAL 类型
	// 不存在 AUTO 类型，所有请求都应该使用 MANUAL
	return "MANUAL"
}

// validateCodeWhispererRequest 验证CodeWhisperer请求的完整性 (SOLID-SRP: 单一责任验证)
func validateCodeWhispererRequest(cwReq *types.CodeWhispererRequest) error {
	// 验证必需字段
	if cwReq.ConversationState.CurrentMessage.UserInputMessage.ModelId == "" {
		return fmt.Errorf("ModelId不能为空")
	}

	if cwReq.ConversationState.ConversationId == "" {
		return fmt.Errorf("ConversationId不能为空")
	}

	// 验证内容完整性 (KISS: 简化内容验证)
	trimmedContent := strings.TrimSpace(cwReq.ConversationState.CurrentMessage.UserInputMessage.Content)
	hasImages := len(cwReq.ConversationState.CurrentMessage.UserInputMessage.Images) > 0

	// 安全检查 UserInputMessageContext 指针
	hasTools := false
	hasToolResults := false
	if ctx := cwReq.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext; ctx != nil {
		hasTools = len(ctx.Tools) > 0
		hasToolResults = len(ctx.ToolResults) > 0
	}

	// 如果有工具结果，允许内容为空（这是工具执行后的反馈请求）
	if hasToolResults {
		logger.Debug("检测到工具结果，允许内容为空",
			logger.String("conversation_id", cwReq.ConversationState.ConversationId),
			logger.Int("tool_results_count", len(cwReq.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.ToolResults)))
		return nil
	}

	// 如果没有内容但有工具，注入占位内容 (YAGNI: 只在需要时处理)
	if trimmedContent == "" && !hasImages && hasTools {
		placeholder := "执行工具任务"
		cwReq.ConversationState.CurrentMessage.UserInputMessage.Content = placeholder
		logger.Warn("注入占位内容以触发工具调用",
			logger.String("conversation_id", cwReq.ConversationState.ConversationId),
			logger.Int("tools_count", len(cwReq.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.Tools)))
		trimmedContent = placeholder
	}

	// 验证至少有内容或图片
	if trimmedContent == "" && !hasImages {
		return fmt.Errorf("用户消息内容和图片都为空")
	}

	return nil
}

// extractToolResultsFromMessage 从消息内容中提取工具结果
func extractToolResultsFromMessage(content any) []types.ToolResult {
	var toolResults []types.ToolResult

	switch v := content.(type) {
	case []any:
		for _, item := range v {
			if block, ok := item.(map[string]any); ok {
				if blockType, exists := block["type"]; exists {
					if typeStr, ok := blockType.(string); ok && typeStr == "tool_result" {
						toolResult := types.ToolResult{}

						// 提取 tool_use_id
						if toolUseId, ok := block["tool_use_id"].(string); ok {
							toolResult.ToolUseId = toolUseId
						}

						// 提取 content - 转换为数组格式
						if content, exists := block["content"]; exists {
							// 将 content 转换为 []map[string]any 格式
							var contentArray []map[string]any

							// 处理不同的 content 格式
							switch c := content.(type) {
							case string:
								// 如果是字符串，包装成标准格式
								contentArray = []map[string]any{
									{"text": c},
								}
							case []any:
								// 如果已经是数组，保持原样
								for _, item := range c {
									if m, ok := item.(map[string]any); ok {
										contentArray = append(contentArray, m)
									}
								}
							case map[string]any:
								// 如果是单个对象，包装成数组
								contentArray = []map[string]any{c}
							default:
								// 其他格式，尝试转换为字符串
								contentArray = []map[string]any{
									{"text": fmt.Sprintf("%v", c)},
								}
							}

							toolResult.Content = contentArray
						}

						// 提取 status (默认为 success)
						toolResult.Status = "success"
						if isError, ok := block["is_error"].(bool); ok && isError {
							toolResult.Status = "error"
							toolResult.IsError = true
						}

						// 处理无效的工具结果（例如用户取消的工具调用）
						if toolResult.ToolUseId == "" {
							continue // 没有 ID 的工具结果无法匹配，跳过
						}
						// 如果内容为空，生成占位内容表示工具调用出错
						if len(toolResult.Content) == 0 {
							toolResult.Content = []map[string]any{
								{"text": "[Tool call failed: no response received]"},
							}
							toolResult.Status = "error"
							toolResult.IsError = true
							logger.Debug("为空响应的工具调用生成占位内容",
								logger.String("tool_use_id", toolResult.ToolUseId))
						}

						toolResults = append(toolResults, toolResult)

						// logger.Debug("提取到工具结果",
						// 	logger.String("tool_use_id", toolResult.ToolUseId),
						// 	logger.String("status", toolResult.Status),
						// 	logger.Int("content_items", len(toolResult.Content)))
					}
				}
			}
		}
	case []types.ContentBlock:
		for _, block := range v {
			if block.Type == "tool_result" {
				toolResult := types.ToolResult{}

				if block.ToolUseId != nil {
					toolResult.ToolUseId = *block.ToolUseId
				}

				// 处理 content
				if block.Content != nil {
					var contentArray []map[string]any

					switch c := block.Content.(type) {
					case string:
						contentArray = []map[string]any{
							{"text": c},
						}
					case []any:
						for _, item := range c {
							if m, ok := item.(map[string]any); ok {
								contentArray = append(contentArray, m)
							}
						}
					case map[string]any:
						contentArray = []map[string]any{c}
					default:
						contentArray = []map[string]any{
							{"text": fmt.Sprintf("%v", c)},
						}
					}

					toolResult.Content = contentArray
				}

				// 设置 status
				toolResult.Status = "success"
				if block.IsError != nil && *block.IsError {
					toolResult.Status = "error"
					toolResult.IsError = true
				}

				// 处理无效的工具结果（例如用户取消的工具调用）
				if toolResult.ToolUseId == "" {
					continue // 没有 ID 的工具结果无法匹配，跳过
				}
				// 如果内容为空，生成占位内容表示工具调用出错
				if len(toolResult.Content) == 0 {
					toolResult.Content = []map[string]any{
						{"text": "[Tool call failed: no response received]"},
					}
					toolResult.Status = "error"
					toolResult.IsError = true
					logger.Debug("为空响应的工具调用生成占位内容",
						logger.String("tool_use_id", toolResult.ToolUseId))
				}

				toolResults = append(toolResults, toolResult)
			}
		}
	}

	return toolResults
}

// BuildCodeWhispererRequest 构建 CodeWhisperer 请求
func BuildCodeWhispererRequest(anthropicReq types.AnthropicRequest, ctx *gin.Context) (types.CodeWhispererRequest, error) {
	// logger.Debug("构建CodeWhisperer请求", logger.String("profile_arn", profileArn))

	cwReq := types.CodeWhispererRequest{}

	// 解析 thinking 配置
	thinkingConfig := ParseThinkingConfig(anthropicReq.Thinking)
	thinkingHint := GetThinkingHint(thinkingConfig)

	// 设置代理相关字段 (基于参考文档的标准配置)
	// 使用稳定的代理延续ID生成器，保持会话连续性 (KISS + DRY原则)
	cwReq.ConversationState.AgentContinuationId = utils.GenerateStableAgentContinuationID(ctx)
	cwReq.ConversationState.AgentTaskType = "vibe" // 固定设置为"vibe"，符合参考文档

	// 智能设置ChatTriggerType (KISS: 简化逻辑但保持准确性)
	cwReq.ConversationState.ChatTriggerType = determineChatTriggerType(anthropicReq)

	// 使用稳定的会话ID生成器，基于客户端信息生成持久化的conversationId
	if ctx != nil {
		cwReq.ConversationState.ConversationId = utils.GenerateStableConversationID(ctx)

		// 调试日志：记录会话ID生成信息
		// clientInfo := utils.ExtractClientInfo(ctx)
		// logger.Debug("生成稳定会话ID",
		// 	logger.String("conversation_id", cwReq.ConversationState.ConversationId),
		// 	logger.String("agent_continuation_id", cwReq.ConversationState.AgentContinuationId),
		// 	logger.String("agent_task_type", cwReq.ConversationState.AgentTaskType),
		// 	logger.String("client_ip", clientInfo["client_ip"]),
		// 	logger.String("user_agent", clientInfo["user_agent"]),
		// 	logger.String("custom_conv_id", clientInfo["custom_conv_id"]),
		// logger.String("custom_agent_cont_id", clientInfo["custom_agent_cont_id"]))
	} else {
		// 向后兼容：如果没有提供context，仍使用UUID
		cwReq.ConversationState.ConversationId = utils.GenerateUUID()
		logger.Debug("使用随机UUID作为会话ID（向后兼容）",
			logger.String("conversation_id", cwReq.ConversationState.ConversationId),
			logger.String("agent_continuation_id", cwReq.ConversationState.AgentContinuationId),
			logger.String("agent_task_type", cwReq.ConversationState.AgentTaskType))
	}

	// 处理最后一条消息，包括图片
	if len(anthropicReq.Messages) == 0 {
		return cwReq, fmt.Errorf("消息列表为空")
	}

	lastMessage := anthropicReq.Messages[len(anthropicReq.Messages)-1]

	// 调试：记录原始消息内容
	// logger.Debug("处理用户消息",
	// 	logger.String("role", lastMessage.Role),
	// 	logger.String("content_type", fmt.Sprintf("%T", lastMessage.Content)))

	textContent, images, err := processMessageContent(lastMessage.Content)
	if err != nil {
		return cwReq, fmt.Errorf("处理消息内容失败: %v", err)
	}

	// 如果启用了 thinking 模式，在用户消息末尾附加 hint
	if thinkingHint != "" && textContent != "" {
		textContent = AppendThinkingHint(textContent, thinkingHint)
	}

	cwReq.ConversationState.CurrentMessage.UserInputMessage.Content = textContent
	// 确保Images字段始终是数组，即使为空
	if len(images) > 0 {
		cwReq.ConversationState.CurrentMessage.UserInputMessage.Images = images
	} else {
		cwReq.ConversationState.CurrentMessage.UserInputMessage.Images = []types.CodeWhispererImage{}
	}

	// 新增：检查并处理 ToolResults
	if lastMessage.Role == "user" {
		toolResults := extractToolResultsFromMessage(lastMessage.Content)
		if len(toolResults) > 0 {
			// 初始化 UserInputMessageContext 指针
			if cwReq.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext == nil {
				cwReq.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext = &types.MessageContext{}
			}
			cwReq.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.ToolResults = toolResults

			logger.Debug("已添加工具结果到请求",
				logger.Int("tool_results_count", len(toolResults)),
				logger.String("conversation_id", cwReq.ConversationState.ConversationId))

			// 如果原始 content 为空，使用占位符（API 不接受空 content）
			if strings.TrimSpace(cwReq.ConversationState.CurrentMessage.UserInputMessage.Content) == "" {
				cwReq.ConversationState.CurrentMessage.UserInputMessage.Content = "Tool execution result"
				logger.Debug("工具结果请求的 content 为空，使用占位符")
			}

			// API 不接受同时包含 tools 和 toolResults，清除 tools
			if len(cwReq.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.Tools) > 0 {
				cwReq.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.Tools = nil
				logger.Debug("清除 tools 以避免与 toolResults 冲突")
			}
		}
	}

	// 检查模型映射是否存在，如果不存在则返回错误
	modelId := config.ModelMap[anthropicReq.Model]
	if modelId == "" {
		logger.Warn("模型映射不存在",
			logger.String("requested_model", anthropicReq.Model),
			logger.String("request_id", cwReq.ConversationState.AgentContinuationId))

		// 返回模型未找到错误，使用已生成的AgentContinuationId
		return cwReq, types.NewModelNotFoundErrorType(anthropicReq.Model, cwReq.ConversationState.AgentContinuationId)
	}
	cwReq.ConversationState.CurrentMessage.UserInputMessage.ModelId = modelId
	cwReq.ConversationState.CurrentMessage.UserInputMessage.Origin = "AI_EDITOR" // v0.4兼容性：固定使用AI_EDITOR

	// 处理 tools 信息 - 根据req.json实际结构优化工具转换
	if len(anthropicReq.Tools) > 0 {
		// logger.Debug("开始处理工具配置",
		// 	logger.Int("tools_count", len(anthropicReq.Tools)),
		// 	logger.String("conversation_id", cwReq.ConversationState.ConversationId))

		var tools []types.CodeWhispererTool
		for i, tool := range anthropicReq.Tools {
			// 验证工具定义的完整性 (SOLID-SRP: 单一责任验证)
			if tool.Name == "" {
				logger.Warn("跳过无名称的工具", logger.Int("tool_index", i))
				continue
			}

			// 过滤不支持的工具：web_search (静默过滤，不发送到上游)
			if tool.Name == "web_search" || tool.Name == "websearch" {
				continue
			}

			// logger.Debug("转换工具定义",
			// 	logger.Int("tool_index", i),
			// 	logger.String("tool_name", tool.Name),
			// logger.String("tool_description", tool.Description)
			// )

			// 根据req.json的实际结构，确保JSON Schema完整性
			cwTool := types.CodeWhispererTool{}
			// 标准化工具名称：如果超过64字符则进行hash处理
			cwTool.ToolSpecification.Name = NormalizeToolName(tool.Name)

			// 验证并处理工具描述
			description := strings.TrimSpace(tool.Description)

			// 限制 description 长度为 10000 字符
			if len(description) > config.MaxToolDescriptionLength {
				cwTool.ToolSpecification.Description = description[:config.MaxToolDescriptionLength]
				logger.Debug("工具描述超长已截断",
					logger.String("tool_name", tool.Name),
					logger.Int("original_length", len(description)),
					logger.Int("max_length", config.MaxToolDescriptionLength))
			} else {
				cwTool.ToolSpecification.Description = description
			}

			// 直接使用原始的InputSchema，避免过度处理 (恢复v0.4兼容性)
			cwTool.ToolSpecification.InputSchema = types.InputSchema{
				Json: tool.InputSchema,
			}
			tools = append(tools, cwTool)
		}

		// 工具配置放在 UserInputMessageContext.Tools 中 (符合req.json结构)
		// 注意：如果过滤后没有任何有效工具，不要发送空的 userInputMessageContext（上游可能判定为格式错误）
		if len(tools) > 0 {
			// 初始化 UserInputMessageContext 指针
			if cwReq.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext == nil {
				cwReq.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext = &types.MessageContext{}
			}
			cwReq.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.Tools = tools
		}
	}

	// 构建历史消息
	if len(anthropicReq.System) > 0 || len(anthropicReq.Messages) > 1 || len(anthropicReq.Tools) > 0 {
		var history []any

		// 构建综合系统提示
		var systemContentBuilder strings.Builder

		// 添加原有的 system 消息
		if len(anthropicReq.System) > 0 {
			for _, sysMsg := range anthropicReq.System {
				content, err := utils.GetMessageContent(sysMsg)
				if err == nil {
					systemContentBuilder.WriteString(content)
					systemContentBuilder.WriteString("\n")
				}
			}
		}

		// 如果有系统内容，添加到历史记录 (恢复v0.4结构化类型)
		if systemContentBuilder.Len() > 0 {
			userMsg := types.HistoryUserMessage{}
			userMsg.UserInputMessage.Content = strings.TrimSpace(systemContentBuilder.String())
			userMsg.UserInputMessage.ModelId = modelId
			userMsg.UserInputMessage.Origin = "AI_EDITOR" // v0.4兼容性：固定使用AI_EDITOR
			history = append(history, userMsg)

			assistantMsg := types.HistoryAssistantMessage{}
			assistantMsg.AssistantResponseMessage.Content = "OK"
			assistantMsg.AssistantResponseMessage.ToolUses = []types.ToolUseEntry{}
			history = append(history, assistantMsg)
		}

		// 然后处理常规消息历史 (修复配对逻辑：合并连续user消息，然后与assistant配对)
		// 关键修复：收集连续的user消息并合并，遇到assistant时配对添加
		var userMessagesBuffer []types.AnthropicRequestMessage // 累积连续的user消息

		// 决定历史消息的循环边界
		// 关键修复：如果最后一条消息是assistant，应该将它加入历史（与前面的user配对）
		// 如果最后一条是user，它作为currentMessage，不加入历史
		historyEndIndex := len(anthropicReq.Messages) - 1
		if lastMessage.Role == "assistant" {
			historyEndIndex = len(anthropicReq.Messages) // 包含最后一条assistant
		}

		for i := 0; i < historyEndIndex; i++ {
			msg := anthropicReq.Messages[i]

			if msg.Role == "user" {
				// 收集user消息到缓冲区
				userMessagesBuffer = append(userMessagesBuffer, msg)
				continue
			}
			if msg.Role == "assistant" {
				// 遇到assistant，只有当有对应的user消息时才处理（忽略孤立assistant）
				if len(userMessagesBuffer) > 0 {
					// 合并所有累积的user消息
					mergedUserMsg := types.HistoryUserMessage{}
					var contentParts []string
					var allImages []types.CodeWhispererImage
					var allToolResults []types.ToolResult

					for _, userMsg := range userMessagesBuffer {
						// 处理每个user消息的内容和图片
						messageContent, messageImages, err := processMessageContent(userMsg.Content)
						if err == nil && messageContent != "" {
							contentParts = append(contentParts, messageContent)
							if len(messageImages) > 0 {
								allImages = append(allImages, messageImages...)
							}
						}

						// 收集工具结果
						toolResults := extractToolResultsFromMessage(userMsg.Content)
						if len(toolResults) > 0 {
							allToolResults = append(allToolResults, toolResults...)
						}
					}

					// 设置合并后的内容
					mergedContent := strings.Join(contentParts, "\n")
					// 如果启用了 thinking 模式，在历史用户消息中也附加 hint
					if thinkingHint != "" && mergedContent != "" {
						mergedContent = AppendThinkingHint(mergedContent, thinkingHint)
					}
					mergedUserMsg.UserInputMessage.Content = mergedContent
					if len(allImages) > 0 {
						mergedUserMsg.UserInputMessage.Images = allImages
					}
					if len(allToolResults) > 0 {
						// 初始化 UserInputMessageContext 指针
						if mergedUserMsg.UserInputMessage.UserInputMessageContext == nil {
							mergedUserMsg.UserInputMessage.UserInputMessageContext = &types.MessageContext{}
						}
						mergedUserMsg.UserInputMessage.UserInputMessageContext.ToolResults = allToolResults
						// 如果历史用户消息的 content 为空，使用占位符
						if strings.TrimSpace(mergedUserMsg.UserInputMessage.Content) == "" {
							mergedUserMsg.UserInputMessage.Content = "Tool execution result"
						}
						// logger.Debug("历史用户消息包含工具结果",
						// 	logger.Int("merged_messages", len(userMessagesBuffer)),
						// 	logger.Int("tool_results_count", len(allToolResults)))
					}

					mergedUserMsg.UserInputMessage.ModelId = modelId
					mergedUserMsg.UserInputMessage.Origin = "AI_EDITOR"
					history = append(history, mergedUserMsg)

					// 清空缓冲区
					userMessagesBuffer = nil

					// 添加assistant消息（只在有配对的user时添加）
					assistantMsg := types.HistoryAssistantMessage{}
					assistantContent, err := utils.GetMessageContent(msg.Content)
					if err == nil {
						assistantMsg.AssistantResponseMessage.Content = assistantContent
					} else {
						assistantMsg.AssistantResponseMessage.Content = ""
					}

					// 提取助手消息中的工具调用
					toolUses := extractToolUsesFromMessage(msg.Content)
					if len(toolUses) > 0 {
						assistantMsg.AssistantResponseMessage.ToolUses = toolUses
					} else {
						assistantMsg.AssistantResponseMessage.ToolUses = []types.ToolUseEntry{}
					}

					history = append(history, assistantMsg)
				}
				// 如果buffer为空，孤立的assistant消息被忽略（不添加到history）
			}
		}

		// 处理结尾的孤立user消息
		// 如果最后一条是user（作为currentMessage），buffer中可能还有倒数第二条及之前的孤立user消息
		// 这些孤立的user消息应该配对一个"OK"的assistant
		if len(userMessagesBuffer) > 0 {
			// 合并所有孤立的user消息
			mergedOrphanUserMsg := types.HistoryUserMessage{}
			var contentParts []string
			var allImages []types.CodeWhispererImage
			var allToolResults []types.ToolResult

			for _, userMsg := range userMessagesBuffer {
				messageContent, messageImages, err := processMessageContent(userMsg.Content)
				if err == nil && messageContent != "" {
					contentParts = append(contentParts, messageContent)
					if len(messageImages) > 0 {
						allImages = append(allImages, messageImages...)
					}
				}

				toolResults := extractToolResultsFromMessage(userMsg.Content)
				if len(toolResults) > 0 {
					allToolResults = append(allToolResults, toolResults...)
				}
			}

			mergedOrphanContent := strings.Join(contentParts, "\n")
			// 如果启用了 thinking 模式，在历史用户消息中也附加 hint
			if thinkingHint != "" && mergedOrphanContent != "" {
				mergedOrphanContent = AppendThinkingHint(mergedOrphanContent, thinkingHint)
			}
			mergedOrphanUserMsg.UserInputMessage.Content = mergedOrphanContent
			if len(allImages) > 0 {
				mergedOrphanUserMsg.UserInputMessage.Images = allImages
			}
			if len(allToolResults) > 0 {
				// 初始化 UserInputMessageContext 指针
				if mergedOrphanUserMsg.UserInputMessage.UserInputMessageContext == nil {
					mergedOrphanUserMsg.UserInputMessage.UserInputMessageContext = &types.MessageContext{}
				}
				mergedOrphanUserMsg.UserInputMessage.UserInputMessageContext.ToolResults = allToolResults
				// 如果孤立用户消息的 content 为空，使用占位符
				if strings.TrimSpace(mergedOrphanUserMsg.UserInputMessage.Content) == "" {
					mergedOrphanUserMsg.UserInputMessage.Content = "Tool execution result"
				}
			}

			mergedOrphanUserMsg.UserInputMessage.ModelId = modelId
			mergedOrphanUserMsg.UserInputMessage.Origin = "AI_EDITOR"
			history = append(history, mergedOrphanUserMsg)

			// 自动配对一个"OK"的assistant响应
			autoAssistantMsg := types.HistoryAssistantMessage{}
			autoAssistantMsg.AssistantResponseMessage.Content = "OK"
			autoAssistantMsg.AssistantResponseMessage.ToolUses = []types.ToolUseEntry{}
			history = append(history, autoAssistantMsg)

			logger.Debug("历史消息末尾存在孤立的user消息，已自动配对assistant",
				logger.Int("orphan_messages", len(userMessagesBuffer)))
		}


		// 验证并修复 toolUses 和 toolResults 的配对关系
		validateAndFixToolPairing(history)
		// 验证并修复空的 content
		validateAndFixEmptyContent(history)
		cwReq.ConversationState.History = history
	}

	// 验证并修复 currentMessage 的空 content
	if strings.TrimSpace(cwReq.ConversationState.CurrentMessage.UserInputMessage.Content) == "" {
		hasImages := len(cwReq.ConversationState.CurrentMessage.UserInputMessage.Images) > 0
		hasToolResults := cwReq.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext != nil &&
			len(cwReq.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.ToolResults) > 0
		hasTools := cwReq.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext != nil &&
			len(cwReq.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.Tools) > 0

		// 如果既没有图片、工具结果，也没有工具，填充占位文本
		if !hasImages && !hasToolResults && !hasTools {
			cwReq.ConversationState.CurrentMessage.UserInputMessage.Content = "忽略这条空消息"
			logger.Debug("修复 currentMessage 的空 content",
				logger.String("placeholder", "忽略这条空消息"))
		}
	}

	// 最终验证请求完整性 (KISS: 简化验证逻辑)
	if err := validateCodeWhispererRequest(&cwReq); err != nil {
		return cwReq, fmt.Errorf("请求验证失败: %v", err)
	}

	return cwReq, nil
}

// extractToolUsesFromMessage 从助手消息内容中提取工具调用
func extractToolUsesFromMessage(content any) []types.ToolUseEntry {
	var toolUses []types.ToolUseEntry

	switch v := content.(type) {
	case []any:
		for _, item := range v {
			if block, ok := item.(map[string]any); ok {
				if blockType, exists := block["type"]; exists {
					if typeStr, ok := blockType.(string); ok && typeStr == "tool_use" {
						toolUse := types.ToolUseEntry{}

						// 提取 id 作为 ToolUseId
						if id, ok := block["id"].(string); ok {
							toolUse.ToolUseId = id
						}

						// 提取 name 并标准化（处理超长名称）
						if name, ok := block["name"].(string); ok {
							toolUse.Name = NormalizeToolName(name)
						}

						// 验证必要字段：跳过无效的工具调用（toolUseId 或 name 为空）
						if toolUse.ToolUseId == "" || toolUse.Name == "" {
							logger.Debug("跳过无效的工具调用（缺少必要字段）",
								logger.String("toolUseId", toolUse.ToolUseId),
								logger.String("name", toolUse.Name))
							continue
						}

						// 过滤不支持的工具：web_search (静默过滤)
						if toolUse.Name == "web_search" || toolUse.Name == "websearch" {
							continue
						}

						// 提取 input
						if input, ok := block["input"].(map[string]any); ok {
							toolUse.Input = input
						} else {
							// 如果 input 不是 map 或不存在，设置为空对象
							toolUse.Input = map[string]any{}
						}

						toolUses = append(toolUses, toolUse)

						// logger.Debug("提取到历史工具调用", logger.String("tool_id", toolUse.ToolUseId), logger.String("tool_name", toolUse.Name))
					}
				}
			}
		}
	case []types.ContentBlock:
		for _, block := range v {
			if block.Type == "tool_use" {
				toolUse := types.ToolUseEntry{}

				if block.ID != nil {
					toolUse.ToolUseId = *block.ID
				}

				if block.Name != nil {
					toolUse.Name = NormalizeToolName(*block.Name)
				}

				// 验证必要字段：跳过无效的工具调用（toolUseId 或 name 为空）
				if toolUse.ToolUseId == "" || toolUse.Name == "" {
					logger.Debug("跳过无效的工具调用（缺少必要字段）",
						logger.String("toolUseId", toolUse.ToolUseId),
						logger.String("name", toolUse.Name))
					continue
				}

				// 过滤不支持的工具：web_search (静默过滤)
				if toolUse.Name == "web_search" || toolUse.Name == "websearch" {
					continue
				}

				if block.Input != nil {
					switch inp := (*block.Input).(type) {
					case map[string]any:
						toolUse.Input = inp
					default:
						toolUse.Input = map[string]any{
							"value": inp,
						}
					}
				} else {
					toolUse.Input = map[string]any{}
				}

				toolUses = append(toolUses, toolUse)
			}
		}
	case string:
		// 如果是纯文本，不包含工具调用
		return nil
	}

	return toolUses
}

// validateAndFixToolPairing 验证并修复历史记录中 toolUses 和 toolResults 的配对关系
func validateAndFixToolPairing(history []any) {
	// 遍历历史记录，查找 assistant-user 配对
	for i := 0; i < len(history)-1; i++ {
		// 检查当前是否为 assistant 消息
		assistantMsg, isAssistant := history[i].(types.HistoryAssistantMessage)
		if !isAssistant {
			continue
		}

		// 检查下一条是否为 user 消息
		if i+1 >= len(history) {
			break
		}
		userMsg, isUser := history[i+1].(types.HistoryUserMessage)
		if !isUser {
			continue
		}

		// 检查 user 消息是否有 toolResults
		if userMsg.UserInputMessage.UserInputMessageContext == nil ||
			len(userMsg.UserInputMessage.UserInputMessageContext.ToolResults) == 0 {
			continue
		}

		toolResults := userMsg.UserInputMessage.UserInputMessageContext.ToolResults

		// 如果 assistant 的 toolUses 为空，但 user 有 toolResults，需要重建 toolUses
		if len(assistantMsg.AssistantResponseMessage.ToolUses) == 0 {
			logger.Warn("检测到 toolUses 和 toolResults 不匹配，尝试从 toolResults 重建 toolUses",
				logger.Int("history_index", i),
				logger.Int("tool_results_count", len(toolResults)))

			// 从 toolResults 重建 toolUses
			var reconstructedToolUses []types.ToolUseEntry
			for _, result := range toolResults {
				if result.ToolUseId == "" {
					continue
				}

				// 创建一个基本的 toolUse 条目
				toolUse := types.ToolUseEntry{
					ToolUseId: result.ToolUseId,
					Name:      "unknown_tool", // 默认名称，因为 toolResult 中没有工具名称
					Input:     map[string]any{},
				}

				reconstructedToolUses = append(reconstructedToolUses, toolUse)
			}

			// 更新 assistant 消息的 toolUses
			assistantMsg.AssistantResponseMessage.ToolUses = reconstructedToolUses
			history[i] = assistantMsg

			logger.Debug("已重建 toolUses",
				logger.Int("history_index", i),
				logger.Int("reconstructed_count", len(reconstructedToolUses)))
		} else {
			// 验证 toolUses 和 toolResults 的 toolUseId 是否匹配
			toolUseIds := make(map[string]bool)
			for _, toolUse := range assistantMsg.AssistantResponseMessage.ToolUses {
				toolUseIds[toolUse.ToolUseId] = true
			}

			toolResultIds := make(map[string]bool)
			for _, result := range toolResults {
				toolResultIds[result.ToolUseId] = true
			}

			// 检查是否有不匹配的 ID
			var missingInToolUses []string
			for id := range toolResultIds {
				if !toolUseIds[id] {
					missingInToolUses = append(missingInToolUses, id)
				}
			}

			if len(missingInToolUses) > 0 {
				logger.Warn("检测到 toolResults 中有 toolUseId 在 toolUses 中不存在",
					logger.Int("history_index", i),
					logger.Any("missing_ids", missingInToolUses))

				// 为缺失的 toolUseId 添加占位 toolUse
				for _, missingId := range missingInToolUses {
					toolUse := types.ToolUseEntry{
						ToolUseId: missingId,
						Name:      "unknown_tool",
						Input:     map[string]any{},
					}
					assistantMsg.AssistantResponseMessage.ToolUses = append(
						assistantMsg.AssistantResponseMessage.ToolUses, toolUse)
				}

				history[i] = assistantMsg
				logger.Debug("已添加缺失的 toolUses",
					logger.Int("history_index", i),
					logger.Int("added_count", len(missingInToolUses)))
			}
		}
	}
}

// validateAndFixEmptyContent 验证并修复历史记录中的空 content
func validateAndFixEmptyContent(history []any) {
	for i, item := range history {
		switch msg := item.(type) {
		case types.HistoryUserMessage:
			if strings.TrimSpace(msg.UserInputMessage.Content) == "" {
				// 检查是否有图片或工具结果
				hasImages := len(msg.UserInputMessage.Images) > 0
				hasToolResults := msg.UserInputMessage.UserInputMessageContext != nil &&
					len(msg.UserInputMessage.UserInputMessageContext.ToolResults) > 0

				// 如果既没有图片也没有工具结果，填充占位文本
				if !hasImages && !hasToolResults {
					msg.UserInputMessage.Content = "忽略这条空消息"
					history[i] = msg
					logger.Debug("修复历史记录中的空 user content",
						logger.Int("history_index", i),
						logger.String("placeholder", "忽略这条空消息"))
				}
			}

		case types.HistoryAssistantMessage:
			if strings.TrimSpace(msg.AssistantResponseMessage.Content) == "" {
				// 检查是否有工具调用
				hasToolUses := len(msg.AssistantResponseMessage.ToolUses) > 0

				// 如果没有工具调用，填充占位文本
				if !hasToolUses {
					msg.AssistantResponseMessage.Content = "忽略这条空消息"
					history[i] = msg
					logger.Debug("修复历史记录中的空 assistant content",
						logger.Int("history_index", i),
						logger.String("placeholder", "忽略这条空消息"))
				}
			}
		}
	}
}
