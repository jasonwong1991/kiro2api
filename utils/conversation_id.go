package utils

import (
	"crypto/md5"
	"fmt"
	"sync"
	"time"

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

// GenerateConversationID 生成稳定的会话ID（默认按 1 小时时间窗）
// 设计目标：
// - 同一客户端（IP+User-Agent）在同一小时内保持一致，便于上游保持会话连续性
// - 不同客户端或跨小时生成不同 ID
func (c *ConversationIDManager) GenerateConversationID(ctx *gin.Context) string {
	// 检查是否有自定义的会话ID头（优先级最高）
	if customConvID := ctx.GetHeader("X-Conversation-ID"); customConvID != "" {
		return cleanConversationID(customConvID)
	}

	// 向后兼容：缺失 context 时退化为随机（避免空指针）
	if ctx == nil {
		return GenerateUUID()
	}

	// 以小时为窗口生成稳定 ID（标准UUID格式）
	clientIP := ctx.ClientIP()
	userAgent := ctx.GetHeader("User-Agent")
	hourBucket := time.Now().UTC().Format("2006010215")
	input := fmt.Sprintf("%s|%s|%s", clientIP, userAgent, hourBucket)

	return generateDeterministicGUID(input, "conversation")
}

// GetOrCreateConversationID 获取或创建会话ID（向后兼容）
func (c *ConversationIDManager) GetOrCreateConversationID(ctx *gin.Context) string {
	return c.GenerateConversationID(ctx)
}

// InvalidateOldSessions 清理过期的会话缓存（保留用于兼容性）
func (c *ConversationIDManager) InvalidateOldSessions() {
	// 当前实现为纯哈希生成；保留清理入口用于兼容旧调用方/并发测试
	c.mu.Lock()
	c.cache = make(map[string]string)
	c.mu.Unlock()
}

// 全局实例 - 单例模式 (SOLID-DIP: 提供抽象访问)
var globalConversationIDManager = NewConversationIDManager()

// GenerateStableConversationID 生成会话ID的全局函数
func GenerateStableConversationID(ctx *gin.Context) string {
	return globalConversationIDManager.GetOrCreateConversationID(ctx)
}

// GenerateStableAgentContinuationID 生成稳定的代理延续 GUID（UUID 格式，默认按 1 小时时间窗）
func GenerateStableAgentContinuationID(ctx *gin.Context) string {
	// 向后兼容：如果没有提供context，使用随机UUID
	if ctx == nil {
		return GenerateUUID()
	}

	// 检查是否有自定义的代理延续ID头（优先级最高）
	if customAgentID := ctx.GetHeader("X-Agent-Continuation-ID"); customAgentID != "" {
		return customAgentID
	}

	clientIP := ctx.ClientIP()
	userAgent := ctx.GetHeader("User-Agent")
	hourBucket := time.Now().UTC().Format("2006010215")
	input := fmt.Sprintf("%s|%s|%s", clientIP, userAgent, hourBucket)
	return generateDeterministicGUID(input, "agent_continuation")
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

// cleanConversationID 清理 conversationId，移除可能的前缀
func cleanConversationID(id string) string {
	// 移除 "uuid64:" 前缀（防御性编程）
	if len(id) > 7 && id[:7] == "uuid64:" {
		return id[7:]
	}
	return id
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
