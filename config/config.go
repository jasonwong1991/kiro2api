package config

import (
	"os"
	"strconv"
)

// ModelMap 模型映射表 (Anthropic model -> Kiro modelId)
var ModelMap = map[string]string{
	"claude-opus-4-5-20251101":   "claude-opus-4.5",
	"claude-opus-4-5":            "claude-opus-4.5",
	"claude-sonnet-4-5":          "claude-sonnet-4.5",
	"claude-sonnet-4-5-20250929": "claude-sonnet-4.5",
	"claude-sonnet-4-20250514":   "claude-sonnet-4",
	"claude-3-7-sonnet-20250219": "claude-3.7-sonnet",
	"claude-3-5-haiku-20241022":  "auto",
	"claude-haiku-4-5-20251001":  "auto",
	// Thinking 模式模型别名（自动启用 thinking 模式）
	"claude-opus-4-5-20251101-thinking":   "claude-opus-4.5",
	"claude-opus-4-5-thinking":            "claude-opus-4.5",
	"claude-sonnet-4-5-thinking":          "claude-sonnet-4.5",
	"claude-sonnet-4-5-20250929-thinking": "claude-sonnet-4.5",
	"claude-sonnet-4-20250514-thinking":   "claude-sonnet-4",
}

// RefreshTokenURL 刷新token的URL (Social方式，固定 us-east-1)
const RefreshTokenURL = "https://prod.us-east-1.auth.desktop.kiro.dev/refreshToken"

// IdcRefreshTokenURLTemplate IdC认证方式的刷新token URL - 模板格式
// 格式: https://oidc.{region}.amazonaws.com/token
// 不同区域的账号需要使用对应区域的 OIDC 端点
const IdcRefreshTokenURLTemplate = "https://oidc.%s.amazonaws.com/token"

// CodeWhispererURL CodeWhisperer API的URL (固定 us-east-1)
const CodeWhispererURL = "https://q.us-east-1.amazonaws.com/generateAssistantResponse"

// McpURLTemplate MCP API URL 模板 (用于 WebSearch 等工具调用)
// 格式: https://q.{region}.amazonaws.com/mcp
const McpURLTemplate = "https://q.%s.amazonaws.com/mcp"

// DefaultMcpRegion 默认 MCP 区域
const DefaultMcpRegion = "us-east-1"

// MaxToolDescriptionLength 工具描述的最大长度（字符数）
// 可通过环境变量 MAX_TOOL_DESCRIPTION_LENGTH 配置，默认 10000
var MaxToolDescriptionLength = getEnvIntWithDefault("MAX_TOOL_DESCRIPTION_LENGTH", 10000)

// getEnvIntWithDefault 获取整数类型环境变量（带默认值）
func getEnvIntWithDefault(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
	}
	return defaultValue
}
