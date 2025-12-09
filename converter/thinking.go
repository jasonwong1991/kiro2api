package converter

import (
	"fmt"
	"strings"
)

// Thinking 模式相关常量
const (
	DefaultMaxThinkingLength = 16000
	ThinkingStartTag         = "<thinking>"
	ThinkingEndTag           = "</thinking>"
)

// ThinkingConfig 解析后的 thinking 配置
type ThinkingConfig struct {
	Enabled      bool
	BudgetTokens int
}

// ParseThinkingConfig 解析 thinking 字段配置
// 支持三种格式：
// 1. bool: true/false
// 2. string: "enabled"/"disabled"
// 3. dict: {"type": "enabled", "budget_tokens": N} 或 {"enabled": true, "budget_tokens": N}
func ParseThinkingConfig(thinking any) ThinkingConfig {
	if thinking == nil {
		return ThinkingConfig{Enabled: false, BudgetTokens: DefaultMaxThinkingLength}
	}

	switch v := thinking.(type) {
	case bool:
		return ThinkingConfig{Enabled: v, BudgetTokens: DefaultMaxThinkingLength}

	case string:
		enabled := strings.EqualFold(v, "enabled")
		return ThinkingConfig{Enabled: enabled, BudgetTokens: DefaultMaxThinkingLength}

	case map[string]any:
		config := ThinkingConfig{BudgetTokens: DefaultMaxThinkingLength}

		// 检查 type 字段
		if typeVal, ok := v["type"].(string); ok {
			config.Enabled = strings.EqualFold(typeVal, "enabled")
		}

		// 检查 enabled 字段（作为备选）
		if !config.Enabled {
			if enabledVal, ok := v["enabled"].(bool); ok {
				config.Enabled = enabledVal
			}
		}

		// 检查 budget_tokens 字段
		if budget, ok := v["budget_tokens"]; ok {
			switch b := budget.(type) {
			case int:
				if b > 0 {
					config.BudgetTokens = b
					// 如果设置了 budget_tokens > 0，也视为启用
					if !config.Enabled {
						config.Enabled = true
					}
				}
			case float64:
				if b > 0 {
					config.BudgetTokens = int(b)
					if !config.Enabled {
						config.Enabled = true
					}
				}
			case int64:
				if b > 0 {
					config.BudgetTokens = int(b)
					if !config.Enabled {
						config.Enabled = true
					}
				}
			}
		}

		return config

	default:
		return ThinkingConfig{Enabled: false, BudgetTokens: DefaultMaxThinkingLength}
	}
}

// IsThinkingModeEnabled 快速检查是否启用了 thinking 模式
func IsThinkingModeEnabled(thinking any) bool {
	return ParseThinkingConfig(thinking).Enabled
}

// GetThinkingHint 生成要附加到用户消息末尾的 thinking hint
func GetThinkingHint(config ThinkingConfig) string {
	if !config.Enabled {
		return ""
	}
	return fmt.Sprintf("<thinking_mode>interleaved</thinking_mode><max_thinking_length>%d</max_thinking_length>", config.BudgetTokens)
}

// AppendThinkingHint 在内容末尾附加 thinking hint（如果尚未存在）
func AppendThinkingHint(content string, hint string) string {
	if hint == "" {
		return content
	}

	content = strings.TrimSpace(content)

	// 检查是否已经包含 hint
	if strings.HasSuffix(content, hint) {
		return content
	}

	// 添加分隔符
	if content == "" {
		return hint
	}

	separator := "\n"
	if strings.HasSuffix(content, "\n") || strings.HasSuffix(content, "\r") {
		separator = ""
	}

	return content + separator + hint
}

// WrapThinkingContent 将 thinking 内容包装为 XML 格式
func WrapThinkingContent(text string) string {
	return ThinkingStartTag + text + ThinkingEndTag
}

// ReasoningEffortToBudget 将 OpenAI 的 reasoning_effort 转换为 budget_tokens
func ReasoningEffortToBudget(effort string) int {
	switch strings.ToLower(effort) {
	case "low":
		return 4000
	case "medium":
		return 16000
	case "high":
		return 32000
	default:
		return DefaultMaxThinkingLength
	}
}

// ConvertReasoningEffortToThinking 将 OpenAI 的 reasoning_effort 转换为 thinking 配置
func ConvertReasoningEffortToThinking(effort string) any {
	if effort == "" {
		return nil
	}

	return map[string]any{
		"type":          "enabled",
		"budget_tokens": ReasoningEffortToBudget(effort),
	}
}

// ExtractThinkingContent 从文本中提取 thinking 内容并返回剩余文本
// 返回值: (thinkingContent, normalContent)
// 用于非流式响应的 thinking 内容解析
func ExtractThinkingContent(text string) (string, string) {
	var thinkingParts []string
	var normalParts []string

	remaining := text

	for {
		startIdx := strings.Index(remaining, ThinkingStartTag)
		if startIdx == -1 {
			// 没有更多 thinking 标签
			if remaining != "" {
				normalParts = append(normalParts, remaining)
			}
			break
		}

		// 添加 thinking 标签之前的内容到普通文本
		if startIdx > 0 {
			normalParts = append(normalParts, remaining[:startIdx])
		}

		// 查找结束标签
		afterStart := remaining[startIdx+len(ThinkingStartTag):]
		endIdx := strings.Index(afterStart, ThinkingEndTag)

		if endIdx == -1 {
			// 未找到结束标签，将剩余内容作为 thinking 内容
			thinkingParts = append(thinkingParts, afterStart)
			break
		}

		// 提取 thinking 内容
		thinkingParts = append(thinkingParts, afterStart[:endIdx])

		// 更新剩余文本
		remaining = afterStart[endIdx+len(ThinkingEndTag):]
	}

	return strings.Join(thinkingParts, ""), strings.Join(normalParts, "")
}
