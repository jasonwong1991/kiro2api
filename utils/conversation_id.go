package utils

import (
	"crypto/md5"
	"fmt"
	"sync"

	"github.com/gin-gonic/gin"
)

// ConversationIDManager 会话ID管理器 (SOLID-SRP: 单一职责)
type ConversationIDManager struct {
	mu    sync.RWMutex      // 保护cache的并发访问（虽然现在不使用缓存，保留用于兼容性）
	cache map[string]string // 保留字段用于兼容性
}

// NewConversationIDManager 创建新的会话ID管理器
func NewConversationIDManager() *ConversationIDManager {
	return &ConversationIDManager{
		cache: make(map[string]string),
	}
}

// GenerateConversationID 生成完全随机的会话ID
// 改进：每次请求生成新的随机ID，避免规律性模式，增强账号安全
func (c *ConversationIDManager) GenerateConversationID(ctx *gin.Context) string {
	// 检查是否有自定义的会话ID头（优先级最高）
	if customConvID := ctx.GetHeader("X-Conversation-ID"); customConvID != "" {
		return customConvID
	}

	// 生成完全随机的会话ID
	randomID := GenerateRandomHex(16) // 生成16字节的随机十六进制字符串
	return fmt.Sprintf("conv-%s", randomID)
}

// GetOrCreateConversationID 获取或创建会话ID（向后兼容）
func (c *ConversationIDManager) GetOrCreateConversationID(ctx *gin.Context) string {
	return c.GenerateConversationID(ctx)
}

// InvalidateOldSessions 清理过期的会话缓存（保留用于兼容性）
func (c *ConversationIDManager) InvalidateOldSessions() {
	// 由于现在使用随机ID，不再需要缓存清理
	c.mu.Lock()
	c.cache = make(map[string]string)
	c.mu.Unlock()
}

// 全局实例 - 单例模式 (SOLID-DIP: 提供抽象访问)
var globalConversationIDManager = NewConversationIDManager()

// GenerateStableConversationID 生成会话ID的全局函数
// 注意：现在生成的是随机ID，名称保留用于向后兼容
func GenerateStableConversationID(ctx *gin.Context) string {
	return globalConversationIDManager.GetOrCreateConversationID(ctx)
}

// GenerateStableAgentContinuationID 生成随机的代理延续GUID
// 改进：每次请求生成新的随机GUID，避免规律性模式
func GenerateStableAgentContinuationID(ctx *gin.Context) string {
	// 向后兼容：如果没有提供context，使用随机UUID
	if ctx == nil {
		return GenerateUUID()
	}

	// 检查是否有自定义的代理延续ID头（优先级最高）
	if customAgentID := ctx.GetHeader("X-Agent-Continuation-ID"); customAgentID != "" {
		return customAgentID
	}

	// 生成完全随机的GUID
	return GenerateUUID()
}

// generateDeterministicGUID 基于输入字符串生成确定性GUID (保留用于特殊场景)
// 遵循UUID v5规范，使用MD5哈希生成标准GUID格式
func generateDeterministicGUID(input, namespace string) string {
	// 在输入中加入命名空间以避免冲突
	namespacedInput := fmt.Sprintf("%s|%s", namespace, input)

	// 生成MD5哈希
	hash := md5.Sum([]byte(namespacedInput))

	// 按照UUID格式重新排列字节
	// 设置版本位 (Version 5 - 基于命名空间的UUID)
	hash[6] = (hash[6] & 0x0f) | 0x50 // Version 5
	hash[8] = (hash[8] & 0x3f) | 0x80 // Variant bits

	// 格式化为标准GUID格式: xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx
	return fmt.Sprintf("%x-%x-%x-%x-%x",
		hash[0:4], hash[4:6], hash[6:8], hash[8:10], hash[10:16])
}

// ExtractClientInfo 提取客户端信息用于调试和日志
func ExtractClientInfo(ctx *gin.Context) map[string]string {
	return map[string]string{
		"client_ip":            ctx.ClientIP(),
		"user_agent":           ctx.GetHeader("User-Agent"),
		"custom_conv_id":       ctx.GetHeader("X-Conversation-ID"),
		"custom_agent_cont_id": ctx.GetHeader("X-Agent-Continuation-ID"),
		"forwarded_for":        ctx.GetHeader("X-Forwarded-For"),
	}
}
