package types

// AnthropicTool 表示 Anthropic API 的工具结构
type AnthropicTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// ToolChoice 表示工具选择策略
type ToolChoice struct {
	Type string `json:"type"`           // "auto", "any", "tool"
	Name string `json:"name,omitempty"` // 当type为"tool"时指定的工具名称
}

// AnthropicRequest 表示 Anthropic API 的请求结构
type AnthropicRequest struct {
	Model       string                    `json:"model"`
	MaxTokens   int                       `json:"max_tokens"`
	Messages    []AnthropicRequestMessage `json:"messages"`
	System      []AnthropicSystemMessage  `json:"system,omitempty"`
	Tools       []AnthropicTool           `json:"tools,omitempty"`
	ToolChoice  any                       `json:"tool_choice,omitempty"` // 可以是string或ToolChoice对象
	Stream      bool                      `json:"stream"`
	Temperature *float64                  `json:"temperature,omitempty"`
	Metadata    map[string]any            `json:"metadata,omitempty"`
	Thinking    any                       `json:"thinking,omitempty"` // 支持bool/string/dict格式的thinking配置
}

// AnthropicStreamResponse 表示 Anthropic 流式响应的结构
type AnthropicStreamResponse struct {
	Type         string `json:"type"`
	Index        int    `json:"index"`
	ContentDelta struct {
		Text string `json:"text"`
		Type string `json:"type"`
	} `json:"delta,omitempty"`
	Content []struct {
		Text string `json:"text"`
		Type string `json:"type"`
	} `json:"content,omitempty"`
	StopReason   string `json:"stop_reason,omitempty"`
	StopSequence string `json:"stop_sequence,omitempty"`
	Usage        *Usage `json:"usage,omitempty"`
}

// AnthropicRequestMessage 表示 Anthropic API 的消息结构
type AnthropicRequestMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"` // 可以是 string 或 []ContentBlock
}

type AnthropicSystemMessage struct {
	Type string `json:"type"`
	Text string `json:"text"` // 可以是 string 或 []ContentBlock
}

// ContentBlock 表示消息内容块的结构
type ContentBlock struct {
	Type      string       `json:"type"`
	Text      *string      `json:"text,omitempty"`
	Thinking  *string      `json:"thinking,omitempty"` // thinking 块的内容
	ToolUseId *string      `json:"tool_use_id,omitempty"`
	Content   any          `json:"content,omitempty"`  // tool_result的内容，可以是string、[]any或map[string]any
	Name      *string      `json:"name,omitempty"`     // tool_use的名称
	Input     *any         `json:"input,omitempty"`    // tool_use的输入参数
	ID        *string      `json:"id,omitempty"`       // tool_use的唯一标识符
	IsError   *bool        `json:"is_error,omitempty"` // tool_result是否表示错误
	Source    *ImageSource `json:"source,omitempty"`   // 图片数据源 (type="image"时使用)
	// 注意：document 类型也使用 source 字段，但结构不同，在 parseContentBlock 中特殊处理
	Document *DocumentSource `json:"-"` // 文档数据源 (type="document"时使用，不直接从JSON解析)
}

// ImageSource 表示图片数据源的结构
type ImageSource struct {
	Type      string `json:"type"`       // "base64"
	MediaType string `json:"media_type"` // "image/jpeg", "image/png", "image/gif", "image/webp"
	Data      string `json:"data"`       // base64编码的图片数据
}

// DocumentSource 表示文档数据源的结构 (Anthropic API document content block)
type DocumentSource struct {
	Type      string `json:"type"`                 // "base64", "text", "url"
	MediaType string `json:"media_type,omitempty"` // "application/pdf", "text/plain"
	Data      string `json:"data,omitempty"`       // base64编码的文档数据或纯文本内容
	URL       string `json:"url,omitempty"`        // URL来源 (type="url"时使用)
}
