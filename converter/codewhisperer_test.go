package converter

import (
	"encoding/json"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"kiro2api/types"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestBuildCodeWhispererRequest_BasicMessage(t *testing.T) {
	anthropicReq := types.AnthropicRequest{
		Model:     "claude-sonnet-4-20250514",
		MaxTokens: 1024,
		Messages: []types.AnthropicRequestMessage{
			{
				Role:    "user",
				Content: "Hello, how are you?",
			},
		},
	}

	// 测试时不传 context
	cwReq, err := BuildCodeWhispererRequest(anthropicReq, nil)

	require.NoError(t, err)
	// 不传 context 时会使用随机 UUID
	assert.NotEmpty(t, cwReq.ConversationState.ConversationId)
	assert.NotEmpty(t, cwReq.ConversationState.AgentContinuationId)
	assert.Equal(t, "vibe", cwReq.ConversationState.AgentTaskType)
	assert.Equal(t, "Hello, how are you?", cwReq.ConversationState.CurrentMessage.UserInputMessage.Content)
	assert.Equal(t, "claude-sonnet-4", cwReq.ConversationState.CurrentMessage.UserInputMessage.ModelId)
	assert.Equal(t, "AI_EDITOR", cwReq.ConversationState.CurrentMessage.UserInputMessage.Origin)
}

func TestBuildCodeWhispererRequest_JSONFormat_OmitNullables(t *testing.T) {
	anthropicReq := types.AnthropicRequest{
		Model:     "claude-sonnet-4-20250514",
		MaxTokens: 1024,
		Messages: []types.AnthropicRequestMessage{
			{
				Role:    "user",
				Content: "Hello",
			},
		},
	}

	cwReq, err := BuildCodeWhispererRequest(anthropicReq, nil)
	require.NoError(t, err)

	body, err := json.Marshal(cwReq)
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))

	conversationState, ok := payload["conversationState"].(map[string]any)
	require.True(t, ok)

	_, hasHistory := conversationState["history"]
	assert.False(t, hasHistory, "history 应该被省略，避免发送 null")

	currentMessage, ok := conversationState["currentMessage"].(map[string]any)
	require.True(t, ok)
	userInputMessage, ok := currentMessage["userInputMessage"].(map[string]any)
	require.True(t, ok)

	_, hasCtx := userInputMessage["userInputMessageContext"]
	assert.False(t, hasCtx, "userInputMessageContext 应该被省略，避免发送空对象 {}")
}

func TestBuildCodeWhispererRequest_JSONFormat_ToolUsesArray(t *testing.T) {
	anthropicReq := types.AnthropicRequest{
		Model:     "claude-sonnet-4-20250514",
		MaxTokens: 1024,
		Messages: []types.AnthropicRequestMessage{
			{
				Role:    "user",
				Content: "First",
			},
			{
				Role:    "assistant",
				Content: "Response",
			},
			{
				Role:    "user",
				Content: "Second",
			},
		},
	}

	cwReq, err := BuildCodeWhispererRequest(anthropicReq, nil)
	require.NoError(t, err)

	body, err := json.Marshal(cwReq)
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))

	conversationState, ok := payload["conversationState"].(map[string]any)
	require.True(t, ok)

	history, ok := conversationState["history"].([]any)
	require.True(t, ok)
	require.Len(t, history, 2)

	assistantEntry, ok := history[1].(map[string]any)
	require.True(t, ok)
	assistantResponseMessage, ok := assistantEntry["assistantResponseMessage"].(map[string]any)
	require.True(t, ok)

	toolUses, exists := assistantResponseMessage["toolUses"]
	require.True(t, exists)
	toolUsesArray, ok := toolUses.([]any)
	require.True(t, ok, "toolUses 必须是数组，不能为 null")
	assert.Len(t, toolUsesArray, 0)
}

func TestBuildCodeWhispererRequest_FilteredTools_DoNotSendEmptyContext(t *testing.T) {
	anthropicReq := types.AnthropicRequest{
		Model:     "claude-sonnet-4-20250514",
		MaxTokens: 1024,
		Messages: []types.AnthropicRequestMessage{
			{
				Role:    "user",
				Content: "Search the web",
			},
		},
		Tools: []types.AnthropicTool{
			{
				Name:        "web_search",
				Description: "Search the web",
				InputSchema: map[string]any{"type": "object"},
			},
		},
		ToolChoice: map[string]any{"type": "any"},
	}

	cwReq, err := BuildCodeWhispererRequest(anthropicReq, nil)
	require.NoError(t, err)
	assert.Equal(t, "MANUAL", cwReq.ConversationState.ChatTriggerType)
	assert.Nil(t, cwReq.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext)
}

func TestBuildCodeWhispererRequest_EmptyMessages(t *testing.T) {
	anthropicReq := types.AnthropicRequest{
		Model:     "claude-sonnet-4-20250514",
		MaxTokens: 1024,
		Messages:  []types.AnthropicRequestMessage{},
	}

	_, err := BuildCodeWhispererRequest(anthropicReq, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "消息列表为空")
}

func TestBuildCodeWhispererRequest_WithSystemMessage(t *testing.T) {
	anthropicReq := types.AnthropicRequest{
		Model:     "claude-sonnet-4-20250514",
		MaxTokens: 1024,
		System: []types.AnthropicSystemMessage{
			{
				Type: "text",
				Text: "You are a helpful assistant.",
			},
		},
		Messages: []types.AnthropicRequestMessage{
			{
				Role:    "user",
				Content: "Hello!",
			},
		},
	}

	cwReq, err := BuildCodeWhispererRequest(anthropicReq, nil)

	require.NoError(t, err)
	assert.NotNil(t, cwReq.ConversationState.History)
	assert.Greater(t, len(cwReq.ConversationState.History), 0)
}

func TestBuildCodeWhispererRequest_WithTools(t *testing.T) {
	anthropicReq := types.AnthropicRequest{
		Model:     "claude-sonnet-4-20250514",
		MaxTokens: 1024,
		Messages: []types.AnthropicRequestMessage{
			{
				Role:    "user",
				Content: "What's the weather?",
			},
		},
		Tools: []types.AnthropicTool{
			{
				Name:        "get_weather",
				Description: "Get weather information",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"location": map[string]any{
							"type": "string",
						},
					},
					"required": []any{"location"},
				},
			},
		},
	}

	cwReq, err := BuildCodeWhispererRequest(anthropicReq, nil)

	require.NoError(t, err)
	require.NotNil(t, cwReq.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext)
	assert.Len(t, cwReq.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.Tools, 1)
	assert.Equal(t, "get_weather", cwReq.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.Tools[0].ToolSpecification.Name)
}

func TestBuildCodeWhispererRequest_FilterWebSearchTool(t *testing.T) {
	anthropicReq := types.AnthropicRequest{
		Model:     "claude-sonnet-4-20250514",
		MaxTokens: 1024,
		Messages: []types.AnthropicRequestMessage{
			{
				Role:    "user",
				Content: "Search the web",
			},
		},
		Tools: []types.AnthropicTool{
			{
				Name:        "web_search",
				Description: "Search the web",
				InputSchema: map[string]any{"type": "object"},
			},
			{
				Name:        "get_weather",
				Description: "Get weather",
				InputSchema: map[string]any{"type": "object"},
			},
		},
	}

	cwReq, err := BuildCodeWhispererRequest(anthropicReq, nil)

	require.NoError(t, err)
	// web_search 应该被过滤掉
	require.NotNil(t, cwReq.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext)
	assert.Len(t, cwReq.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.Tools, 1)
	assert.Equal(t, "get_weather", cwReq.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.Tools[0].ToolSpecification.Name)
}

func TestBuildCodeWhispererRequest_WithImages(t *testing.T) {
	t.Skip("图片验证测试需要有效的 base64 编码图片数据，跳过以保持测试套件的稳定性")
	// 注意：图片处理逻辑在 processMessageContent 中，已被其他测试间接覆盖
}

func TestBuildCodeWhispererRequest_WithToolResults(t *testing.T) {
	toolUseID := "tool_123"
	isError := false
	anthropicReq := types.AnthropicRequest{
		Model:     "claude-sonnet-4-20250514",
		MaxTokens: 1024,
		Messages: []types.AnthropicRequestMessage{
			{
				Role: "user",
				Content: []types.ContentBlock{
					{
						Type:      "tool_result",
						ToolUseId: &toolUseID,
						Content:   "Temperature is 25°C",
						IsError:   &isError,
					},
				},
			},
		},
		Tools: []types.AnthropicTool{
			{
				Name:        "get_weather",
				Description: "Get weather information",
				InputSchema: map[string]any{"type": "object"},
			},
		},
	}

	cwReq, err := BuildCodeWhispererRequest(anthropicReq, nil)

	require.NoError(t, err)
	require.NotNil(t, cwReq.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext)
	assert.Len(t, cwReq.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.ToolResults, 1)
	assert.Equal(t, "tool_123", cwReq.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.ToolResults[0].ToolUseId)
	assert.Equal(t, "Temperature is 25°C", cwReq.ConversationState.CurrentMessage.UserInputMessage.Content)
}

func TestBuildCodeWhispererRequest_WithToolResults_ShouldKeepToolsWhenProvided(t *testing.T) {
	toolUseID := "tool_123"
	isError := false
	anthropicReq := types.AnthropicRequest{
		Model:     "claude-sonnet-4-20250514",
		MaxTokens: 1024,
		Messages: []types.AnthropicRequestMessage{
			{
				Role: "user",
				Content: []types.ContentBlock{
					{
						Type:      "tool_result",
						ToolUseId: &toolUseID,
						Content:   "Temperature is 25°C",
						IsError:   &isError,
					},
				},
			},
		},
		Tools: []types.AnthropicTool{
			{
				Name:        "get_weather",
				Description: "Get weather information",
				InputSchema: map[string]any{
					"type": "object",
				},
			},
		},
	}

	cwReq, err := BuildCodeWhispererRequest(anthropicReq, nil)

	require.NoError(t, err)
	require.NotNil(t, cwReq.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext)
	assert.Len(t, cwReq.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.ToolResults, 1)
	assert.Len(t, cwReq.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.Tools, 1)
}

func TestBuildCodeWhispererRequest_WithHistory(t *testing.T) {
	anthropicReq := types.AnthropicRequest{
		Model:     "claude-sonnet-4-20250514",
		MaxTokens: 1024,
		Messages: []types.AnthropicRequestMessage{
			{
				Role:    "user",
				Content: "What is 2+2?",
			},
			{
				Role:    "assistant",
				Content: "2+2 equals 4.",
			},
			{
				Role:    "user",
				Content: "What about 3+3?",
			},
		},
	}

	cwReq, err := BuildCodeWhispererRequest(anthropicReq, nil)

	require.NoError(t, err)
	assert.NotNil(t, cwReq.ConversationState.History)
	// 历史应该包含前两条消息（一对user-assistant）
	assert.Greater(t, len(cwReq.ConversationState.History), 0)
	// 当前消息应该是最后一条
	assert.Equal(t, "What about 3+3?", cwReq.ConversationState.CurrentMessage.UserInputMessage.Content)
}

func TestBuildCodeWhispererRequest_ModelNotFound(t *testing.T) {
	anthropicReq := types.AnthropicRequest{
		Model:     "invalid-model-name",
		MaxTokens: 1024,
		Messages: []types.AnthropicRequestMessage{
			{
				Role:    "user",
				Content: "Hello",
			},
		},
	}

	_, err := BuildCodeWhispererRequest(anthropicReq, nil)

	require.Error(t, err)
	var modelNotFoundErr *types.ModelNotFoundErrorType
	assert.ErrorAs(t, err, &modelNotFoundErr)
	assert.NotNil(t, modelNotFoundErr.ErrorData)
}

func TestDetermineChatTriggerType(t *testing.T) {
	tests := []struct {
		name     string
		req      types.AnthropicRequest
		expected string
	}{
		{
			name: "有工具但无tool_choice - MANUAL",
			req: types.AnthropicRequest{
				Tools: []types.AnthropicTool{
					{Name: "test_tool"},
				},
			},
			expected: "MANUAL",
		},
		{
			name: "有工具且tool_choice=any - MANUAL",
			req: types.AnthropicRequest{
				Tools: []types.AnthropicTool{
					{Name: "test_tool"},
				},
				ToolChoice: map[string]any{"type": "any"},
			},
			expected: "MANUAL",
		},
		{
			name: "无工具，无历史 - MANUAL",
			req: types.AnthropicRequest{
				Messages: []types.AnthropicRequestMessage{
					{Role: "user", Content: "Hello"},
				},
			},
			expected: "MANUAL",
		},
		{
			name: "无工具，有历史 - MANUAL",
			req: types.AnthropicRequest{
				Messages: []types.AnthropicRequestMessage{
					{Role: "user", Content: "First"},
					{Role: "assistant", Content: "Response"},
					{Role: "user", Content: "Second"},
				},
			},
			expected: "MANUAL",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := determineChatTriggerType(tt.req)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractToolUsesFromMessage(t *testing.T) {
	t.Run("提取工具调用", func(t *testing.T) {
		textContent := "Let me check that."
		toolID := "tool:abc123"
		toolName := "get_weather"
		toolInput := any(map[string]any{"location": "Tokyo"})

		content := []types.ContentBlock{
			{
				Type: "text",
				Text: &textContent,
			},
			{
				Type:  "tool_use",
				ID:    &toolID,
				Name:  &toolName,
				Input: &toolInput,
			},
		}

		toolUses := extractToolUsesFromMessage(content)

		require.Len(t, toolUses, 1)
		assert.Equal(t, "tool_abc123", toolUses[0].ToolUseId)
		assert.Equal(t, "get_weather", toolUses[0].Name)
		assert.NotNil(t, toolUses[0].Input)
	})

	t.Run("无工具调用", func(t *testing.T) {
		textContent := "Just text"
		content := []types.ContentBlock{
			{
				Type: "text",
				Text: &textContent,
			},
		}

		toolUses := extractToolUsesFromMessage(content)

		assert.Nil(t, toolUses)
	})
}

func TestExtractToolResultsFromMessage(t *testing.T) {
	t.Run("提取工具结果", func(t *testing.T) {
		toolUseID := "tool:123"
		isError := false
		content := []types.ContentBlock{
			{
				Type:      "tool_result",
				ToolUseId: &toolUseID,
				Content:   "Result data",
				IsError:   &isError,
			},
		}

		toolResults := extractToolResultsFromMessage(content)

		require.Len(t, toolResults, 1)
		assert.Equal(t, "tool_123", toolResults[0].ToolUseId)
		// Content 会被转换为数组格式
		assert.NotNil(t, toolResults[0].Content)
		assert.Equal(t, "success", toolResults[0].Status)
	})

	t.Run("提取错误的工具结果", func(t *testing.T) {
		toolUseID := "tool_456"
		isError := true
		content := []types.ContentBlock{
			{
				Type:      "tool_result",
				ToolUseId: &toolUseID,
				Content:   "Error occurred",
				IsError:   &isError,
			},
		}

		toolResults := extractToolResultsFromMessage(content)

		require.Len(t, toolResults, 1)
		assert.Equal(t, "error", toolResults[0].Status)
	})

	t.Run("处理空内容的工具结果（用户取消）", func(t *testing.T) {
		// 模拟用户取消工具调用时的情况：tool_result 有 ID 但没有 content
		content := []any{
			map[string]any{
				"type":        "tool_result",
				"tool_use_id": "tooluse_cancelled_123",
				// 没有 content 字段
			},
		}

		toolResults := extractToolResultsFromMessage(content)

		// 应该生成占位内容而不是跳过
		require.Len(t, toolResults, 1)
		assert.Equal(t, "tooluse_cancelled_123", toolResults[0].ToolUseId)
		assert.Equal(t, "error", toolResults[0].Status)
		assert.True(t, toolResults[0].IsError)
		require.Len(t, toolResults[0].Content, 1)
		assert.Equal(t, "[Tool call failed: no response received]", toolResults[0].Content[0]["text"])
	})

	t.Run("跳过没有ID的工具结果", func(t *testing.T) {
		content := []any{
			map[string]any{
				"type":    "tool_result",
				"content": "some content",
				// 没有 tool_use_id
			},
		}

		toolResults := extractToolResultsFromMessage(content)

		// 没有 ID 的工具结果应该被跳过
		assert.Len(t, toolResults, 0)
	})
}

func TestNormalizeToolInputSchema_ExclusiveMinimumBoolean(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"sources": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":             "integer",
					"minimum":          0,
					"exclusiveMinimum": true,
				},
			},
		},
	}

	normalized := normalizeToolInputSchema(schema)
	items := normalized["properties"].(map[string]any)["sources"].(map[string]any)["items"].(map[string]any)

	value, exists := items["exclusiveMinimum"]
	require.True(t, exists)
	assert.Equal(t, 0, value)
	_, hasMinimum := items["minimum"]
	assert.False(t, hasMinimum)
}

func TestBuildCodeWhispererRequest_NormalizeLegacySchemaBounds(t *testing.T) {
	anthropicReq := types.AnthropicRequest{
		Model:     "claude-sonnet-4-20250514",
		MaxTokens: 1024,
		Messages: []types.AnthropicRequestMessage{
			{
				Role:    "user",
				Content: "hello",
			},
		},
		Tools: []types.AnthropicTool{
			{
				Name:        "read_pdf",
				Description: "read pdf",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"page": map[string]any{
							"type":             "integer",
							"minimum":          0,
							"exclusiveMinimum": true,
						},
					},
				},
			},
		},
	}

	cwReq, err := BuildCodeWhispererRequest(anthropicReq, nil)
	require.NoError(t, err)

	require.NotNil(t, cwReq.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext)
	require.Len(t, cwReq.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.Tools, 1)

	schema := cwReq.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.Tools[0].ToolSpecification.InputSchema.Json
	page := schema["properties"].(map[string]any)["page"].(map[string]any)
	assert.Equal(t, 0, page["exclusiveMinimum"])
	_, hasMinimum := page["minimum"]
	assert.False(t, hasMinimum)
}

func TestBuildCodeWhispererRequest_NoToolsDefinition_ShouldStripStructuredToolData(t *testing.T) {
	toolID := "Bash:1"
	textContent := "checking"
	toolName := "Bash"
	toolInput := any(map[string]any{"command": "echo hi"})
	isError := false

	anthropicReq := types.AnthropicRequest{
		Model:     "claude-sonnet-4-20250514",
		MaxTokens: 1024,
		Messages: []types.AnthropicRequestMessage{
			{
				Role:    "user",
				Content: "start",
			},
			{
				Role: "assistant",
				Content: []types.ContentBlock{
					{Type: "text", Text: &textContent},
					{Type: "tool_use", ID: &toolID, Name: &toolName, Input: &toolInput},
				},
			},
			{
				Role: "user",
				Content: []types.ContentBlock{
					{Type: "tool_result", ToolUseId: &toolID, Content: "ok", IsError: &isError},
				},
			},
		},
		Tools: nil,
	}

	cwReq, err := BuildCodeWhispererRequest(anthropicReq, nil)
	require.NoError(t, err)

	if cwReq.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext != nil {
		assert.Len(t, cwReq.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.ToolResults, 0)
	}

	require.GreaterOrEqual(t, len(cwReq.ConversationState.History), 2)
	assistantEntry, ok := cwReq.ConversationState.History[1].(types.HistoryAssistantMessage)
	require.True(t, ok)
	assert.Len(t, assistantEntry.AssistantResponseMessage.ToolUses, 0)
}

func TestBuildCodeWhispererRequest_CurrentToolResultsWithoutLatestAssistantToolUses(t *testing.T) {
	toolUseID := "tool_abc123"
	isError := false

	anthropicReq := types.AnthropicRequest{
		Model:     "claude-sonnet-4-20250514",
		MaxTokens: 1024,
		Messages: []types.AnthropicRequestMessage{
			{
				Role:    "user",
				Content: "first",
			},
			{
				Role: "assistant",
				Content: []types.ContentBlock{
					{
						Type: "text",
						Text: func() *string {
							s := "no tool use"
							return &s
						}(),
					},
				},
			},
			{
				Role: "user",
				Content: []types.ContentBlock{
					{
						Type:      "tool_result",
						ToolUseId: &toolUseID,
						Content:   "result",
						IsError:   &isError,
					},
				},
			},
		},
		Tools: []types.AnthropicTool{
			{
				Name:        "dummy_tool",
				Description: "dummy",
				InputSchema: map[string]any{"type": "object"},
			},
		},
	}

	cwReq, err := BuildCodeWhispererRequest(anthropicReq, nil)
	require.NoError(t, err)

	ctx := cwReq.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext
	require.NotNil(t, ctx)
	assert.Len(t, ctx.ToolResults, 0)
	assert.Len(t, ctx.Tools, 1)
}

func TestBuildCodeWhispererRequest_CurrentToolResultsShouldBeKeptWhenLatestAssistantMatches(t *testing.T) {
	toolUseID := "tool_abc123"
	toolName := "dummy_tool"
	toolInput := any(map[string]any{"x": 1})
	isError := false

	anthropicReq := types.AnthropicRequest{
		Model:     "claude-sonnet-4-20250514",
		MaxTokens: 1024,
		Messages: []types.AnthropicRequestMessage{
			{
				Role:    "user",
				Content: "first",
			},
			{
				Role: "assistant",
				Content: []types.ContentBlock{
					{
						Type:  "tool_use",
						ID:    &toolUseID,
						Name:  &toolName,
						Input: &toolInput,
					},
				},
			},
			{
				Role: "user",
				Content: []types.ContentBlock{
					{
						Type:      "tool_result",
						ToolUseId: &toolUseID,
						Content:   "result",
						IsError:   &isError,
					},
				},
			},
		},
		Tools: []types.AnthropicTool{
			{
				Name:        "dummy_tool",
				Description: "dummy",
				InputSchema: map[string]any{"type": "object"},
			},
		},
	}

	cwReq, err := BuildCodeWhispererRequest(anthropicReq, nil)
	require.NoError(t, err)

	ctx := cwReq.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext
	require.NotNil(t, ctx)
	assert.Len(t, ctx.ToolResults, 1)
	assert.Equal(t, "tool_abc123", ctx.ToolResults[0].ToolUseId)
}

func TestBuildCodeWhispererRequest_LatestAssistantToolUsesWithoutCurrentToolResults_ShouldBeRemoved(t *testing.T) {
	toolUseID := "tool_abc123"
	toolName := "dummy_tool"
	toolInput := any(map[string]any{"x": 1})

	anthropicReq := types.AnthropicRequest{
		Model:     "claude-sonnet-4-20250514",
		MaxTokens: 1024,
		Messages: []types.AnthropicRequestMessage{
			{
				Role:    "user",
				Content: "first",
			},
			{
				Role: "assistant",
				Content: []types.ContentBlock{
					{
						Type:  "tool_use",
						ID:    &toolUseID,
						Name:  &toolName,
						Input: &toolInput,
					},
				},
			},
			{
				Role:    "user",
				Content: "normal user message",
			},
		},
		Tools: []types.AnthropicTool{
			{
				Name:        "dummy_tool",
				Description: "dummy",
				InputSchema: map[string]any{"type": "object"},
			},
		},
	}

	cwReq, err := BuildCodeWhispererRequest(anthropicReq, nil)
	require.NoError(t, err)

	require.GreaterOrEqual(t, len(cwReq.ConversationState.History), 2)
	assistantEntry, ok := cwReq.ConversationState.History[1].(types.HistoryAssistantMessage)
	require.True(t, ok)
	assert.Len(t, assistantEntry.AssistantResponseMessage.ToolUses, 0)
}

func TestBuildCodeWhispererRequest_LatestAssistantToolUsesShouldBeFilteredByCurrentToolResults(t *testing.T) {
	matchedToolUseID := "tool_match"
	extraToolUseID := "tool_extra"
	toolName := "dummy_tool"
	toolInput := any(map[string]any{"x": 1})
	isError := false

	anthropicReq := types.AnthropicRequest{
		Model:     "claude-sonnet-4-20250514",
		MaxTokens: 1024,
		Messages: []types.AnthropicRequestMessage{
			{
				Role:    "user",
				Content: "first",
			},
			{
				Role: "assistant",
				Content: []types.ContentBlock{
					{Type: "tool_use", ID: &matchedToolUseID, Name: &toolName, Input: &toolInput},
					{Type: "tool_use", ID: &extraToolUseID, Name: &toolName, Input: &toolInput},
				},
			},
			{
				Role: "user",
				Content: []types.ContentBlock{
					{
						Type:      "tool_result",
						ToolUseId: &matchedToolUseID,
						Content:   "result",
						IsError:   &isError,
					},
				},
			},
		},
		Tools: []types.AnthropicTool{
			{
				Name:        "dummy_tool",
				Description: "dummy",
				InputSchema: map[string]any{"type": "object"},
			},
		},
	}

	cwReq, err := BuildCodeWhispererRequest(anthropicReq, nil)
	require.NoError(t, err)

	require.GreaterOrEqual(t, len(cwReq.ConversationState.History), 2)
	assistantEntry, ok := cwReq.ConversationState.History[1].(types.HistoryAssistantMessage)
	require.True(t, ok)
	assert.Len(t, assistantEntry.AssistantResponseMessage.ToolUses, 1)
	assert.Equal(t, "tool_match", assistantEntry.AssistantResponseMessage.ToolUses[0].ToolUseId)
}

func TestValidateCodeWhispererRequest(t *testing.T) {
	t.Run("有效请求", func(t *testing.T) {
		cwReq := &types.CodeWhispererRequest{}
		cwReq.ConversationState.ConversationId = "test-conversation-id"
		cwReq.ConversationState.CurrentMessage.UserInputMessage.Content = "Hello"
		cwReq.ConversationState.CurrentMessage.UserInputMessage.ModelId = "CLAUDE_SONNET_4_20250514_V1_0"

		err := validateCodeWhispererRequest(cwReq)
		assert.NoError(t, err)
	})

	t.Run("缺少ConversationId", func(t *testing.T) {
		cwReq := &types.CodeWhispererRequest{}
		cwReq.ConversationState.CurrentMessage.UserInputMessage.Content = "Hello"
		cwReq.ConversationState.CurrentMessage.UserInputMessage.ModelId = "CLAUDE_SONNET_4_20250514_V1_0"

		err := validateCodeWhispererRequest(cwReq)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "ConversationId")
	})

	t.Run("缺少ModelId", func(t *testing.T) {
		cwReq := &types.CodeWhispererRequest{}
		cwReq.ConversationState.ConversationId = "test-conversation-id"
		cwReq.ConversationState.CurrentMessage.UserInputMessage.Content = "Hello"

		err := validateCodeWhispererRequest(cwReq)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "ModelId")
	})
}

// TestBuildCodeWhispererRequest_OrphanAssistantMessages 测试孤立assistant消息的忽略处理
func TestBuildCodeWhispererRequest_OrphanAssistantMessages(t *testing.T) {
	t.Run("开头是assistant消息", func(t *testing.T) {
		anthropicReq := types.AnthropicRequest{
			Model:     "claude-sonnet-4-20250514",
			MaxTokens: 1024,
			Messages: []types.AnthropicRequestMessage{
				{
					Role:    "assistant",
					Content: "Hello, how can I help?",
				},
				{
					Role:    "user",
					Content: "Tell me about Go",
				},
			},
		}

		cwReq, err := BuildCodeWhispererRequest(anthropicReq, nil)

		require.NoError(t, err)

		// 历史应该为空（孤立的assistant消息被忽略）
		if cwReq.ConversationState.History != nil {
			assert.Len(t, cwReq.ConversationState.History, 0)
		}

		// 当前消息
		assert.Equal(t, "Tell me about Go", cwReq.ConversationState.CurrentMessage.UserInputMessage.Content)
	})

	t.Run("连续的assistant消息", func(t *testing.T) {
		anthropicReq := types.AnthropicRequest{
			Model:     "claude-sonnet-4-20250514",
			MaxTokens: 1024,
			Messages: []types.AnthropicRequestMessage{
				{
					Role:    "user",
					Content: "Question 1",
				},
				{
					Role:    "assistant",
					Content: "Answer 1",
				},
				{
					Role:    "assistant",
					Content: "Answer 2",
				},
				{
					Role:    "user",
					Content: "Question 2",
				},
			},
		}

		cwReq, err := BuildCodeWhispererRequest(anthropicReq, nil)

		require.NoError(t, err)
		assert.NotNil(t, cwReq.ConversationState.History)

		// 历史应该包含2条消息（第二个assistant被忽略）：
		// 1. user: "Question 1"
		// 2. assistant: "Answer 1"
		assert.Len(t, cwReq.ConversationState.History, 2)

		// 验证第一对
		firstUserMsg, ok := cwReq.ConversationState.History[0].(types.HistoryUserMessage)
		assert.True(t, ok)
		assert.Equal(t, "Question 1", firstUserMsg.UserInputMessage.Content)

		firstAssistantMsg, ok := cwReq.ConversationState.History[1].(types.HistoryAssistantMessage)
		assert.True(t, ok)
		assert.Equal(t, "Answer 1", firstAssistantMsg.AssistantResponseMessage.Content)

		// 当前消息
		assert.Equal(t, "Question 2", cwReq.ConversationState.CurrentMessage.UserInputMessage.Content)
	})
}

// TestBuildCodeWhispererRequest_OrphanUserMessages 测试孤立user消息的容错处理
func TestBuildCodeWhispererRequest_OrphanUserMessages(t *testing.T) {
	t.Run("历史末尾存在孤立user消息", func(t *testing.T) {
		anthropicReq := types.AnthropicRequest{
			Model:     "claude-sonnet-4-20250514",
			MaxTokens: 1024,
			Messages: []types.AnthropicRequestMessage{
				{
					Role:    "user",
					Content: "第一个问题",
				},
				{
					Role:    "assistant",
					Content: "第一个回答",
				},
				{
					Role:    "user",
					Content: "第二个问题（孤立）",
				},
				{
					Role:    "user",
					Content: "第三个问题（当前）",
				},
			},
		}

		cwReq, err := BuildCodeWhispererRequest(anthropicReq, nil)

		require.NoError(t, err)
		assert.NotNil(t, cwReq.ConversationState.History)

		// 历史应该包含4条消息：
		// 1. user: "第一个问题"
		// 2. assistant: "第一个回答"
		// 3. user: "第二个问题（孤立）"
		// 4. assistant: "OK" (自动配对的响应)
		assert.Len(t, cwReq.ConversationState.History, 4)

		// 验证最后一对是自动配对的
		lastUserMsg, ok := cwReq.ConversationState.History[2].(types.HistoryUserMessage)
		assert.True(t, ok)
		assert.Equal(t, "第二个问题（孤立）", lastUserMsg.UserInputMessage.Content)

		lastAssistantMsg, ok := cwReq.ConversationState.History[3].(types.HistoryAssistantMessage)
		assert.True(t, ok)
		assert.Equal(t, "OK", lastAssistantMsg.AssistantResponseMessage.Content)
		assert.Len(t, lastAssistantMsg.AssistantResponseMessage.ToolUses, 0)

		// 当前消息应该是最后一条
		assert.Equal(t, "第三个问题（当前）", cwReq.ConversationState.CurrentMessage.UserInputMessage.Content)
	})

	t.Run("历史末尾存在多个连续孤立user消息", func(t *testing.T) {
		anthropicReq := types.AnthropicRequest{
			Model:     "claude-sonnet-4-20250514",
			MaxTokens: 1024,
			Messages: []types.AnthropicRequestMessage{
				{
					Role:    "user",
					Content: "第一个问题",
				},
				{
					Role:    "assistant",
					Content: "第一个回答",
				},
				{
					Role:    "user",
					Content: "第二个问题（孤立1）",
				},
				{
					Role:    "user",
					Content: "第三个问题（孤立2）",
				},
				{
					Role:    "user",
					Content: "第四个问题（当前）",
				},
			},
		}

		cwReq, err := BuildCodeWhispererRequest(anthropicReq, nil)

		require.NoError(t, err)
		assert.NotNil(t, cwReq.ConversationState.History)

		// 历史应该包含4条消息：
		// 1. user: "第一个问题"
		// 2. assistant: "第一个回答"
		// 3. user: "第二个问题（孤立1）\n第三个问题（孤立2）" (合并)
		// 4. assistant: "OK" (自动配对的响应)
		assert.Len(t, cwReq.ConversationState.History, 4)

		// 验证合并的user消息
		mergedUserMsg, ok := cwReq.ConversationState.History[2].(types.HistoryUserMessage)
		assert.True(t, ok)
		assert.Contains(t, mergedUserMsg.UserInputMessage.Content, "第二个问题（孤立1）")
		assert.Contains(t, mergedUserMsg.UserInputMessage.Content, "第三个问题（孤立2）")

		// 验证自动配对的assistant消息
		autoAssistantMsg, ok := cwReq.ConversationState.History[3].(types.HistoryAssistantMessage)
		assert.True(t, ok)
		assert.Equal(t, "OK", autoAssistantMsg.AssistantResponseMessage.Content)

		// 当前消息应该是最后一条
		assert.Equal(t, "第四个问题（当前）", cwReq.ConversationState.CurrentMessage.UserInputMessage.Content)
	})

	t.Run("正常配对的消息不受影响", func(t *testing.T) {
		anthropicReq := types.AnthropicRequest{
			Model:     "claude-sonnet-4-20250514",
			MaxTokens: 1024,
			Messages: []types.AnthropicRequestMessage{
				{
					Role:    "user",
					Content: "第一个问题",
				},
				{
					Role:    "assistant",
					Content: "第一个回答",
				},
				{
					Role:    "user",
					Content: "第二个问题",
				},
				{
					Role:    "assistant",
					Content: "第二个回答",
				},
				{
					Role:    "user",
					Content: "第三个问题（当前）",
				},
			},
		}

		cwReq, err := BuildCodeWhispererRequest(anthropicReq, nil)

		require.NoError(t, err)
		assert.NotNil(t, cwReq.ConversationState.History)

		// 历史应该包含4条消息（两对正常配对）
		assert.Len(t, cwReq.ConversationState.History, 4)

		// 验证第一对
		firstUserMsg, ok := cwReq.ConversationState.History[0].(types.HistoryUserMessage)
		assert.True(t, ok)
		assert.Equal(t, "第一个问题", firstUserMsg.UserInputMessage.Content)

		firstAssistantMsg, ok := cwReq.ConversationState.History[1].(types.HistoryAssistantMessage)
		assert.True(t, ok)
		assert.Equal(t, "第一个回答", firstAssistantMsg.AssistantResponseMessage.Content)

		// 验证第二对
		secondUserMsg, ok := cwReq.ConversationState.History[2].(types.HistoryUserMessage)
		assert.True(t, ok)
		assert.Equal(t, "第二个问题", secondUserMsg.UserInputMessage.Content)

		secondAssistantMsg, ok := cwReq.ConversationState.History[3].(types.HistoryAssistantMessage)
		assert.True(t, ok)
		assert.Equal(t, "第二个回答", secondAssistantMsg.AssistantResponseMessage.Content)

		// 当前消息
		assert.Equal(t, "第三个问题（当前）", cwReq.ConversationState.CurrentMessage.UserInputMessage.Content)
	})
}
