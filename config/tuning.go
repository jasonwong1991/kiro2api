package config

import "time"

// Tuning 性能和行为调优参数
// 从硬编码提取为可配置常量，遵循 KISS 原则
const (
	// ========== 解析器配置 ==========

	// ParserMaxErrors 解析器容忍的最大错误次数
	// 用于所有解析器，防止死循环
	ParserMaxErrors = 5

	// ========== Token缓存配置 ==========

	// TokenCacheTTL Token缓存的生存时间
	// 过期后需要重新刷新
	TokenCacheTTL = 5 * time.Minute

	// HTTPClientKeepAlive HTTP客户端Keep-Alive间隔
	HTTPClientKeepAlive = 30 * time.Second

	// HTTPClientTLSHandshakeTimeout HTTP客户端TLS握手超时
	HTTPClientTLSHandshakeTimeout = 15 * time.Second

	// ========== 重试配置 ==========

	// RetryMaxAttempts 最大重试次数（不含首次请求）
	RetryMaxAttempts = 2

	// UpstreamRetryDelay 上游请求重试间隔
	UpstreamRetryDelay = 500 * time.Millisecond

	// RetryableStatusCodes 可重试的 HTTP 状态码
	// 429: Too Many Requests
	// 500: Internal Server Error
	// 502: Bad Gateway
	// 503: Service Unavailable
	// 504: Gateway Timeout
)

// RetryableStatusCodes 可重试的状态码列表
var RetryableStatusCodes = []int{429, 500, 502, 503, 504}
