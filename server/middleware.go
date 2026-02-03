package server

import (
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"kiro2api/logger"
	"kiro2api/utils"

	"github.com/gin-gonic/gin"
)

// 默认每个 IP 最大并发请求数
const defaultMaxConcurrentPerIP = 5

// ipCleanupInterval IP 计数器清理间隔
const ipCleanupInterval = 10 * time.Minute

// IPConcurrencyLimiter IP 并发限制器
type IPConcurrencyLimiter struct {
	maxConcurrent int64
	counters      sync.Map // map[string]*int64
}

// NewIPConcurrencyLimiter 创建 IP 并发限制器
func NewIPConcurrencyLimiter(maxConcurrent int) *IPConcurrencyLimiter {
	l := &IPConcurrencyLimiter{
		maxConcurrent: int64(maxConcurrent),
	}
	go l.cleanupLoop()
	return l
}

// cleanupLoop 定期清理计数为0的IP条目，防止内存泄漏
func (l *IPConcurrencyLimiter) cleanupLoop() {
	ticker := time.NewTicker(ipCleanupInterval)
	defer ticker.Stop()
	for range ticker.C {
		l.counters.Range(func(key, value interface{}) bool {
			counter := value.(*int64)
			if atomic.LoadInt64(counter) == 0 {
				l.counters.Delete(key)
			}
			return true
		})
	}
}

// Acquire 尝试获取并发槽位，返回是否成功
func (l *IPConcurrencyLimiter) Acquire(ip string) bool {
	counterPtr, _ := l.counters.LoadOrStore(ip, new(int64))
	counter := counterPtr.(*int64)
	newVal := atomic.AddInt64(counter, 1)
	if newVal > l.maxConcurrent {
		atomic.AddInt64(counter, -1)
		return false
	}
	return true
}

// Release 释放并发槽位
func (l *IPConcurrencyLimiter) Release(ip string) {
	if counterPtr, ok := l.counters.Load(ip); ok {
		counter := counterPtr.(*int64)
		// 防御性检查：使用 CAS 避免计数器变为负数
		for {
			old := atomic.LoadInt64(counter)
			if old <= 0 {
				return
			}
			if atomic.CompareAndSwapInt64(counter, old, old-1) {
				return
			}
		}
	}
}

// GetCurrentCount 获取指定 IP 当前并发数（用于调试）
func (l *IPConcurrencyLimiter) GetCurrentCount(ip string) int64 {
	if counterPtr, ok := l.counters.Load(ip); ok {
		return atomic.LoadInt64(counterPtr.(*int64))
	}
	return 0
}

// GetStats 获取所有 IP 的并发统计（用于监控）
func (l *IPConcurrencyLimiter) GetStats() map[string]int64 {
	stats := make(map[string]int64)
	l.counters.Range(func(key, value interface{}) bool {
		ip := key.(string)
		count := atomic.LoadInt64(value.(*int64))
		if count > 0 {
			stats[ip] = count
		}
		return true
	})
	return stats
}

// 全局 IP 并发限制器实例
var globalIPLimiter *IPConcurrencyLimiter

// initIPLimiter 初始化 IP 并发限制器
func initIPLimiter() *IPConcurrencyLimiter {
	if globalIPLimiter != nil {
		return globalIPLimiter
	}

	maxConcurrent := defaultMaxConcurrentPerIP
	if envVal := os.Getenv("KIRO_MAX_CONCURRENT_PER_IP"); envVal != "" {
		if val, err := strconv.Atoi(envVal); err == nil && val > 0 {
			maxConcurrent = val
		}
	}

	globalIPLimiter = NewIPConcurrencyLimiter(maxConcurrent)
	logger.Info("IP 并发限制器已初始化",
		logger.Int("max_concurrent_per_ip", maxConcurrent))
	return globalIPLimiter
}

// IPConcurrencyMiddleware 创建基于 IP 的并发限制中间件
// 限制每个 IP 同时只能有 N 个请求在处理中
func IPConcurrencyMiddleware() gin.HandlerFunc {
	limiter := initIPLimiter()

	return func(c *gin.Context) {
		// 获取客户端真实 IP
		clientIP := c.ClientIP()

		// 尝试获取并发槽位
		if !limiter.Acquire(clientIP) {
			logger.Warn("IP 并发请求超限",
				logger.String("client_ip", clientIP),
				logger.Int64("current_count", limiter.GetCurrentCount(clientIP)),
				logger.Int64("max_concurrent", limiter.maxConcurrent))

			c.JSON(http.StatusTooManyRequests, gin.H{
				"error": gin.H{
					"message": "Too many concurrent requests from your IP. Please wait and retry.",
					"code":    "rate_limited",
				},
			})
			c.Abort()
			return
		}

		// 确保请求结束时释放槽位
		defer limiter.Release(clientIP)

		c.Next()
	}
}

// GetIPLimiterStats 获取 IP 限制器统计信息（供管理 API 使用）
func GetIPLimiterStats() map[string]interface{} {
	if globalIPLimiter == nil {
		return map[string]interface{}{
			"enabled": false,
		}
	}
	return map[string]interface{}{
		"enabled":        true,
		"max_concurrent": globalIPLimiter.maxConcurrent,
		"active_ips":     globalIPLimiter.GetStats(),
	}
}

// PathBasedAuthMiddleware 创建基于路径的API密钥验证中间件
func PathBasedAuthMiddleware(authToken string, protectedPrefixes []string) gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path

		// 检查是否需要认证
		if !requiresAuth(path, protectedPrefixes) {
			logger.Debug("跳过认证", logger.String("path", path))
			c.Next()
			return
		}

		if !validateAPIKey(c, authToken) {
			c.Abort()
			return
		}

		c.Next()
	}
}

// RequestIDMiddleware 为每个请求注入 request_id 并通过响应头返回
// - 优先使用客户端的 X-Request-ID
// - 若无则生成一个UUID（utils.GenerateUUID）
func RequestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		rid := c.GetHeader("X-Request-ID")
		if rid == "" {
			rid = "req_" + utils.GenerateUUID()
		}
		c.Set("request_id", rid)
		c.Writer.Header().Set("X-Request-ID", rid)
		c.Next()
	}
}

// GetRequestID 从上下文读取 request_id（若不存在返回空串）
func GetRequestID(c *gin.Context) string {
	if v, ok := c.Get("request_id"); ok {
		if s, ok2 := v.(string); ok2 {
			return s
		}
	}
	return ""
}

// GetMessageID 从上下文读取 message_id（若不存在返回空串）
func GetMessageID(c *gin.Context) string {
	if v, ok := c.Get("message_id"); ok {
		if s, ok2 := v.(string); ok2 {
			return s
		}
	}
	return ""
}

// addReqFields 注入标准请求字段，统一上下游日志可追踪（DRY）
func addReqFields(c *gin.Context, fields ...logger.Field) []logger.Field {
	rid := GetRequestID(c)
	mid := GetMessageID(c)
	// 预留容量避免重复分配
	out := make([]logger.Field, 0, len(fields)+2)
	if rid != "" {
		out = append(out, logger.String("request_id", rid))
	}
	if mid != "" {
		out = append(out, logger.String("message_id", mid))
	}
	out = append(out, fields...)
	return out
}

// requiresAuth 检查指定路径是否需要认证
func requiresAuth(path string, protectedPrefixes []string) bool {
	for _, prefix := range protectedPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// extractAPIKey 提取API密钥的通用逻辑
func extractAPIKey(c *gin.Context) string {
	apiKey := c.GetHeader("Authorization")
	if apiKey == "" {
		apiKey = c.GetHeader("x-api-key")
	} else {
		apiKey = strings.TrimPrefix(apiKey, "Bearer ")
	}
	return apiKey
}

// validateAPIKey 验证API密钥 - 重构后的版本
func validateAPIKey(c *gin.Context, authToken string) bool {
	providedApiKey := extractAPIKey(c)

	if providedApiKey == "" {
		logger.Warn("请求缺少Authorization或x-api-key头")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "401"})
		return false
	}

	if providedApiKey != authToken {
		logger.Error("authToken验证失败",
			logger.String("expected", "***"),
			logger.String("provided", "***"))
		c.JSON(http.StatusUnauthorized, gin.H{"error": "401"})
		return false
	}

	return true
}
