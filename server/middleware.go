package server

import (
	"context"
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

// IP 并发限制器配置常量
const (
	defaultMaxConcurrentPerIP = 5             // 默认每个 IP 最大并发请求数
	defaultAcquireTimeout     = 60 * time.Second // 默认排队等待超时时间
	ipCleanupInterval         = 10 * time.Minute // IP 信号量清理间隔
)

// ipSemaphore 单个 IP 的信号量
type ipSemaphore struct {
	ch      chan struct{} // 信号量通道
	waiting int64         // 等待中的请求数（用于监控）
}

// IPConcurrencyLimiter IP 并发限制器（基于信号量，支持排队等待）
type IPConcurrencyLimiter struct {
	maxConcurrent  int
	acquireTimeout time.Duration
	semaphores     sync.Map // map[string]*ipSemaphore
}

// NewIPConcurrencyLimiter 创建 IP 并发限制器
func NewIPConcurrencyLimiter(maxConcurrent int, acquireTimeout time.Duration) *IPConcurrencyLimiter {
	l := &IPConcurrencyLimiter{
		maxConcurrent:  maxConcurrent,
		acquireTimeout: acquireTimeout,
	}
	go l.cleanupLoop()
	return l
}

// cleanupLoop 定期清理空闲的 IP 信号量，防止内存泄漏
func (l *IPConcurrencyLimiter) cleanupLoop() {
	ticker := time.NewTicker(ipCleanupInterval)
	defer ticker.Stop()
	for range ticker.C {
		l.semaphores.Range(func(key, value interface{}) bool {
			sem := value.(*ipSemaphore)
			// 只清理完全空闲的信号量（无活跃请求且无等待请求）
			if len(sem.ch) == l.maxConcurrent && atomic.LoadInt64(&sem.waiting) == 0 {
				l.semaphores.Delete(key)
			}
			return true
		})
	}
}

// getOrCreateSemaphore 获取或创建 IP 的信号量
func (l *IPConcurrencyLimiter) getOrCreateSemaphore(ip string) *ipSemaphore {
	if val, ok := l.semaphores.Load(ip); ok {
		return val.(*ipSemaphore)
	}
	// 创建新的信号量（预填充表示可用槽位）
	sem := &ipSemaphore{
		ch: make(chan struct{}, l.maxConcurrent),
	}
	for i := 0; i < l.maxConcurrent; i++ {
		sem.ch <- struct{}{}
	}
	actual, _ := l.semaphores.LoadOrStore(ip, sem)
	return actual.(*ipSemaphore)
}

// Acquire 尝试获取并发槽位（支持排队等待）
// 返回: 是否成功, 等待时间
func (l *IPConcurrencyLimiter) Acquire(ctx context.Context, ip string) (bool, time.Duration) {
	sem := l.getOrCreateSemaphore(ip)
	startTime := time.Now()

	// 先尝试非阻塞获取
	select {
	case <-sem.ch:
		return true, 0
	default:
		// 需要排队等待
	}

	// 记录等待状态
	atomic.AddInt64(&sem.waiting, 1)
	defer atomic.AddInt64(&sem.waiting, -1)

	// 创建超时 context
	timeoutCtx, cancel := context.WithTimeout(ctx, l.acquireTimeout)
	defer cancel()

	select {
	case <-sem.ch:
		return true, time.Since(startTime)
	case <-timeoutCtx.Done():
		return false, time.Since(startTime)
	}
}

// Release 释放并发槽位
func (l *IPConcurrencyLimiter) Release(ip string) {
	if val, ok := l.semaphores.Load(ip); ok {
		sem := val.(*ipSemaphore)
		// 非阻塞写入（防止重复释放导致阻塞）
		select {
		case sem.ch <- struct{}{}:
		default:
			logger.Warn("IP 信号量释放异常：槽位已满",
				logger.String("ip", ip))
		}
	}
}

// GetCurrentCount 获取指定 IP 当前并发数（用于调试）
func (l *IPConcurrencyLimiter) GetCurrentCount(ip string) int {
	if val, ok := l.semaphores.Load(ip); ok {
		sem := val.(*ipSemaphore)
		return l.maxConcurrent - len(sem.ch)
	}
	return 0
}

// GetWaitingCount 获取指定 IP 等待中的请求数
func (l *IPConcurrencyLimiter) GetWaitingCount(ip string) int64 {
	if val, ok := l.semaphores.Load(ip); ok {
		return atomic.LoadInt64(&val.(*ipSemaphore).waiting)
	}
	return 0
}

// GetStats 获取所有 IP 的并发统计（用于监控）
func (l *IPConcurrencyLimiter) GetStats() map[string]map[string]int64 {
	stats := make(map[string]map[string]int64)
	l.semaphores.Range(func(key, value interface{}) bool {
		ip := key.(string)
		sem := value.(*ipSemaphore)
		active := int64(l.maxConcurrent - len(sem.ch))
		waiting := atomic.LoadInt64(&sem.waiting)
		if active > 0 || waiting > 0 {
			stats[ip] = map[string]int64{
				"active":  active,
				"waiting": waiting,
			}
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

	acquireTimeout := defaultAcquireTimeout
	if envVal := os.Getenv("KIRO_IP_ACQUIRE_TIMEOUT"); envVal != "" {
		if val, err := time.ParseDuration(envVal); err == nil && val > 0 {
			acquireTimeout = val
		}
	}

	globalIPLimiter = NewIPConcurrencyLimiter(maxConcurrent, acquireTimeout)
	logger.Info("IP 并发限制器已初始化",
		logger.Int("max_concurrent_per_ip", maxConcurrent),
		logger.Duration("acquire_timeout", acquireTimeout))
	return globalIPLimiter
}

// IPConcurrencyMiddleware 创建基于 IP 的并发限制中间件
// 限制每个 IP 同时只能有 N 个请求在处理中，超限时排队等待
func IPConcurrencyMiddleware() gin.HandlerFunc {
	limiter := initIPLimiter()

	return func(c *gin.Context) {
		clientIP := c.ClientIP()

		// 调试日志：记录 IP 识别信息
		logger.Debug("IP 并发限制检查",
			logger.String("client_ip", clientIP),
			logger.String("x_forwarded_for", c.GetHeader("X-Forwarded-For")),
			logger.String("x_real_ip", c.GetHeader("X-Real-IP")),
			logger.String("remote_addr", c.Request.RemoteAddr),
			logger.Int("current_active", limiter.GetCurrentCount(clientIP)),
			logger.Int64("current_waiting", limiter.GetWaitingCount(clientIP)))

		// 尝试获取并发槽位（支持排队等待）
		acquired, waitTime := limiter.Acquire(c.Request.Context(), clientIP)
		if !acquired {
			logger.Warn("IP 并发请求排队超时",
				logger.String("client_ip", clientIP),
				logger.Int("current_count", limiter.GetCurrentCount(clientIP)),
				logger.Int64("waiting_count", limiter.GetWaitingCount(clientIP)),
				logger.Duration("wait_time", waitTime),
				logger.Duration("timeout", limiter.acquireTimeout))

			c.JSON(http.StatusTooManyRequests, gin.H{
				"error": gin.H{
					"message": "Too many concurrent requests from your IP. Queue timeout exceeded.",
					"code":    "rate_limited",
				},
			})
			c.Abort()
			return
		}

		// 记录等待时间（如果有）
		if waitTime > 0 {
			logger.Debug("IP 并发请求排队成功",
				logger.String("client_ip", clientIP),
				logger.Duration("wait_time", waitTime))
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
		"enabled":         true,
		"max_concurrent":  globalIPLimiter.maxConcurrent,
		"acquire_timeout": globalIPLimiter.acquireTimeout.String(),
		"active_ips":      globalIPLimiter.GetStats(),
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
