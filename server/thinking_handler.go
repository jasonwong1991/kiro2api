package server

import (
	"strings"

	"kiro2api/converter"
	"kiro2api/parser"
)

// ThinkingStreamHandler 处理流式响应中的 thinking 标签
// 负责解析 <thinking>...</thinking> 标签并转换为适当的 SSE 事件
// 注意：此处理器只负责识别和过滤 thinking 内容，不管理上游 block 的生命周期
type ThinkingStreamHandler struct {
	inThinkingBlock      bool   // 当前是否在 thinking 块内
	buffer               string // 内容缓冲区
	pendingStartTagChars int    // 待处理的开始标签字符数（部分匹配）
	thinkingBlockIndex   int    // thinking 块的索引（使用高位避免与上游冲突）
	thinkingBlockStarted bool   // thinking 块是否已启动
	isOpenAIFormat       bool   // 是否为 OpenAI 格式输出
}

// NewThinkingStreamHandler 创建新的 thinking 流处理器
func NewThinkingStreamHandler(isOpenAIFormat bool) *ThinkingStreamHandler {
	return &ThinkingStreamHandler{
		inThinkingBlock:      false,
		buffer:               "",
		pendingStartTagChars: 0,
		thinkingBlockIndex:   100, // 使用高位索引，避免与上游 block index 冲突
		thinkingBlockStarted: false,
		isOpenAIFormat:       isOpenAIFormat,
	}
}

// ProcessContent 处理内容并返回需要发送的事件
// 返回值：events - 需要发送的事件，processedContent - 经过处理的普通文本内容
// 注意：此方法不会关闭上游的 text block，只会管理自己的 thinking block
func (h *ThinkingStreamHandler) ProcessContent(content string) ([]map[string]any, string) {
	if content == "" {
		return nil, ""
	}

	h.buffer += content
	var events []map[string]any
	var normalTextBuilder strings.Builder

	for len(h.buffer) > 0 {
		// 处理待处理的开始标签字符（部分匹配后续）
		if h.pendingStartTagChars > 0 {
			if len(h.buffer) < h.pendingStartTagChars {
				h.pendingStartTagChars -= len(h.buffer)
				h.buffer = ""
				break
			}
			h.buffer = h.buffer[h.pendingStartTagChars:]
			h.pendingStartTagChars = 0
			if h.buffer == "" {
				break
			}
			continue
		}

		if !h.inThinkingBlock {
			// 不在 thinking 块内，查找开始标签
			thinkStart := strings.Index(h.buffer, converter.ThinkingStartTag)

			if thinkStart == -1 {
				// 未找到完整的开始标签，检查部分匹配
				pending := pendingTagSuffix(h.buffer, converter.ThinkingStartTag)

				if pending == len(h.buffer) && pending > 0 {
					// 整个缓冲区可能是开始标签的前缀，进入 thinking 模式
					h.inThinkingBlock = true
					h.pendingStartTagChars = len(converter.ThinkingStartTag) - pending

					// 启动 thinking 块（仅 Anthropic 格式）
					if !h.isOpenAIFormat && !h.thinkingBlockStarted {
						events = append(events, h.createThinkingBlockStart(h.thinkingBlockIndex))
						h.thinkingBlockStarted = true
					}

					h.buffer = ""
					break
				}

				// 输出不匹配的部分
				emitLen := len(h.buffer) - pending
				if emitLen <= 0 {
					break
				}

				textChunk := h.buffer[:emitLen]
				if textChunk != "" {
					normalTextBuilder.WriteString(textChunk)
				}
				h.buffer = h.buffer[emitLen:]
			} else {
				// 找到开始标签
				beforeText := h.buffer[:thinkStart]
				if beforeText != "" {
					normalTextBuilder.WriteString(beforeText)
				}

				h.buffer = h.buffer[thinkStart+len(converter.ThinkingStartTag):]
				h.inThinkingBlock = true
				h.pendingStartTagChars = 0

				// 启动 thinking 块（仅 Anthropic 格式）
				if !h.isOpenAIFormat && !h.thinkingBlockStarted {
					events = append(events, h.createThinkingBlockStart(h.thinkingBlockIndex))
					h.thinkingBlockStarted = true
				}
			}
		} else {
			// 在 thinking 块内，查找结束标签
			thinkEnd := strings.Index(h.buffer, converter.ThinkingEndTag)

			if thinkEnd == -1 {
				// 未找到完整的结束标签，检查部分匹配
				pending := pendingTagSuffix(h.buffer, converter.ThinkingEndTag)
				emitLen := len(h.buffer) - pending
				if emitLen <= 0 {
					break
				}

				thinkingChunk := h.buffer[:emitLen]
				if thinkingChunk != "" {
					if h.isOpenAIFormat {
						events = append(events, h.createOpenAIReasoningDelta(thinkingChunk))
					} else {
						events = append(events, h.createThinkingDelta(h.thinkingBlockIndex, thinkingChunk))
					}
				}
				h.buffer = h.buffer[emitLen:]
			} else {
				// 找到结束标签
				thinkingChunk := h.buffer[:thinkEnd]
				if thinkingChunk != "" {
					if h.isOpenAIFormat {
						events = append(events, h.createOpenAIReasoningDelta(thinkingChunk))
					} else {
						events = append(events, h.createThinkingDelta(h.thinkingBlockIndex, thinkingChunk))
					}
				}

				h.buffer = h.buffer[thinkEnd+len(converter.ThinkingEndTag):]
				h.inThinkingBlock = false

				// 关闭 thinking 块（仅 Anthropic 格式）
				if h.thinkingBlockStarted && !h.isOpenAIFormat {
					events = append(events, h.createContentBlockStop(h.thinkingBlockIndex))
					h.thinkingBlockStarted = false
					// 递增索引，为下一个 thinking 块准备
					h.thinkingBlockIndex++
				}
			}
		}
	}

	return events, normalTextBuilder.String()
}

// IsInThinkingBlock 返回当前是否在 thinking 块内
func (h *ThinkingStreamHandler) IsInThinkingBlock() bool {
	return h.inThinkingBlock
}

// Flush 冲刷剩余缓冲区内容
func (h *ThinkingStreamHandler) Flush() []map[string]any {
	var events []map[string]any

	if h.buffer != "" {
		if h.inThinkingBlock {
			if h.isOpenAIFormat {
				events = append(events, h.createOpenAIReasoningDelta(h.buffer))
			} else {
				events = append(events, h.createThinkingDelta(h.thinkingBlockIndex, h.buffer))
			}
		}
		// 普通文本不在这里处理，由调用者处理
		h.buffer = ""
	}

	// 关闭未关闭的 thinking 块
	if h.thinkingBlockStarted && !h.isOpenAIFormat {
		events = append(events, h.createContentBlockStop(h.thinkingBlockIndex))
		h.thinkingBlockStarted = false
	}

	return events
}

// === 辅助函数 ===

// pendingTagSuffix 计算缓冲区末尾与标签前缀的匹配长度
func pendingTagSuffix(buffer string, tag string) int {
	if buffer == "" || tag == "" {
		return 0
	}

	maxLen := len(buffer)
	if maxLen > len(tag)-1 {
		maxLen = len(tag) - 1
	}

	for length := maxLen; length > 0; length-- {
		if buffer[len(buffer)-length:] == tag[:length] {
			return length
		}
	}
	return 0
}

// createThinkingBlockStart 创建 thinking 块开始事件
func (h *ThinkingStreamHandler) createThinkingBlockStart(index int) map[string]any {
	return map[string]any{
		"type":  "content_block_start",
		"index": index,
		"content_block": map[string]any{
			"type":     "thinking",
			"thinking": "",
		},
	}
}

// createThinkingDelta 创建 thinking delta 事件
func (h *ThinkingStreamHandler) createThinkingDelta(index int, text string) map[string]any {
	return map[string]any{
		"type":  "content_block_delta",
		"index": index,
		"delta": map[string]any{
			"type":     "thinking_delta",
			"thinking": text,
		},
	}
}

// createContentBlockStop 创建内容块停止事件
func (h *ThinkingStreamHandler) createContentBlockStop(index int) map[string]any {
	return map[string]any{
		"type":  "content_block_stop",
		"index": index,
	}
}

// createOpenAIReasoningDelta 创建 OpenAI 格式的 reasoning delta 事件
func (h *ThinkingStreamHandler) createOpenAIReasoningDelta(text string) map[string]any {
	return map[string]any{
		"type":             "reasoning_delta",
		"reasoning_content": text,
	}
}

// ThinkingEventProcessor 处理 thinking 相关的 SSE 事件
// 用于将内部事件格式转换为 parser.SSEEvent
type ThinkingEventProcessor struct {
	handler *ThinkingStreamHandler
}

// NewThinkingEventProcessor 创建 thinking 事件处理器
func NewThinkingEventProcessor(isOpenAIFormat bool) *ThinkingEventProcessor {
	return &ThinkingEventProcessor{
		handler: NewThinkingStreamHandler(isOpenAIFormat),
	}
}

// ProcessTextDelta 处理文本增量事件
// 返回需要发送的事件列表和经过过滤的普通文本内容
func (p *ThinkingEventProcessor) ProcessTextDelta(text string) ([]parser.SSEEvent, string) {
	events, normalText := p.handler.ProcessContent(text)

	var sseEvents []parser.SSEEvent
	for _, evt := range events {
		sseEvents = append(sseEvents, parser.SSEEvent{
			Event: evt["type"].(string),
			Data:  evt,
		})
	}

	return sseEvents, normalText
}

// Flush 冲刷剩余内容
func (p *ThinkingEventProcessor) Flush() []parser.SSEEvent {
	events := p.handler.Flush()

	var sseEvents []parser.SSEEvent
	for _, evt := range events {
		sseEvents = append(sseEvents, parser.SSEEvent{
			Event: evt["type"].(string),
			Data:  evt,
		})
	}

	return sseEvents
}

// IsInThinkingBlock 返回当前是否在 thinking 块内
func (p *ThinkingEventProcessor) IsInThinkingBlock() bool {
	return p.handler.IsInThinkingBlock()
}

// GetHandler 获取底层的 ThinkingStreamHandler
func (p *ThinkingEventProcessor) GetHandler() *ThinkingStreamHandler {
	return p.handler
}
