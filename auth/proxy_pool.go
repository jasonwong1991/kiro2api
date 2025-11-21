package auth

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"kiro2api/config"
	"kiro2api/logger"
)

// ProxyInfo 代理信息
type ProxyInfo struct {
	URL           string    `json:"url"`           // 代理URL，格式：http://username:password@ip:port
	Healthy       bool      `json:"healthy"`       // 健康状态
	LastCheck     time.Time `json:"last_check"`    // 最后检查时间
	FailureCount  int       `json:"failure_count"` // 连续失败次数
	AssignedCount int       `json:"-"`             // 已分配账号数（运行时统计）
}

// ProxyPoolManager 代理池管理器
type ProxyPoolManager struct {
	proxies          []*ProxyInfo          // 代理池
	tokenProxyMap    map[string]string     // token索引 -> 代理URL映射（会话保持）
	proxyClients     map[string]*http.Client // 代理URL -> HTTP客户端缓存
	mutex            sync.RWMutex
	healthCheckURL   string                // 健康检查URL
	healthCheckInterval time.Duration      // 健康检查间隔
	maxFailures      int                   // 最大失败次数（超过则标记为不健康）
	stopChan         chan struct{}         // 停止信号
}

// NewProxyPoolManager 创建代理池管理器
func NewProxyPoolManager(proxyURLs []string) (*ProxyPoolManager, error) {
	if len(proxyURLs) == 0 {
		return nil, nil // 未配置代理池，返回nil
	}

	proxies := make([]*ProxyInfo, 0, len(proxyURLs))
	for _, proxyURL := range proxyURLs {
		// 验证代理URL格式
		if _, err := url.Parse(proxyURL); err != nil {
			logger.Warn("无效的代理URL，跳过",
				logger.String("proxy_url", proxyURL),
				logger.Err(err))
			continue
		}
		proxies = append(proxies, &ProxyInfo{
			URL:          proxyURL,
			Healthy:      true, // 初始假定健康
			LastCheck:    time.Now(),
			FailureCount: 0,
		})
	}

	if len(proxies) == 0 {
		return nil, fmt.Errorf("没有有效的代理URL")
	}

	manager := &ProxyPoolManager{
		proxies:             proxies,
		tokenProxyMap:       make(map[string]string),
		proxyClients:        make(map[string]*http.Client),
		healthCheckURL:      getHealthCheckURL(),
		healthCheckInterval: getHealthCheckInterval(),
		maxFailures:         getMaxProxyFailures(),
		stopChan:            make(chan struct{}),
	}

	// 预创建HTTP客户端
	for _, proxy := range proxies {
		if client, err := manager.createProxyClient(proxy.URL); err == nil {
			manager.proxyClients[proxy.URL] = client
		} else {
			logger.Warn("创建代理客户端失败",
				logger.String("proxy_url", proxy.URL),
				logger.Err(err))
		}
	}

	// 启动健康检查
	go manager.startHealthCheck()

	logger.Info("代理池初始化成功",
		logger.Int("proxy_count", len(proxies)),
		logger.String("health_check_url", manager.healthCheckURL),
		logger.Duration("check_interval", manager.healthCheckInterval))

	return manager, nil
}

// GetProxyForToken 为token获取代理（延迟分配 + 会话保持）
func (pm *ProxyPoolManager) GetProxyForToken(tokenIndex string) (string, *http.Client, error) {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	// 检查是否已分配代理（会话保持）
	if proxyURL, exists := pm.tokenProxyMap[tokenIndex]; exists {
		// 检查代理是否仍然健康
		if pm.isProxyHealthy(proxyURL) {
			client := pm.proxyClients[proxyURL]
			return proxyURL, client, nil
		}
		// 代理不健康，需要重新分配
		logger.Warn("代理不健康，重新分配",
			logger.String("token_index", tokenIndex),
			logger.String("old_proxy", proxyURL))
		delete(pm.tokenProxyMap, tokenIndex)
	}

	// 分配新代理（负载均衡）
	proxyURL := pm.selectProxyForAssignment()
	if proxyURL == "" {
		return "", nil, fmt.Errorf("没有可用的代理")
	}

	// 绑定关系
	pm.tokenProxyMap[tokenIndex] = proxyURL

	// 更新分配计数
	for _, proxy := range pm.proxies {
		if proxy.URL == proxyURL {
			proxy.AssignedCount++
			break
		}
	}

	client := pm.proxyClients[proxyURL]

	logger.Debug("分配代理",
		logger.String("token_index", tokenIndex),
		logger.String("proxy_url", proxyURL),
		logger.Int("assigned_count", pm.getAssignedCount(proxyURL)))

	return proxyURL, client, nil
}

// selectProxyForAssignment 选择代理进行分配（负载均衡）
// 内部方法：调用者必须持有 pm.mutex
func (pm *ProxyPoolManager) selectProxyForAssignment() string {
	var healthyProxies []*ProxyInfo
	for _, proxy := range pm.proxies {
		if proxy.Healthy {
			healthyProxies = append(healthyProxies, proxy)
		}
	}

	if len(healthyProxies) == 0 {
		// 所有代理都不健康，回退到使用失败次数最少的
		logger.Warn("所有代理都不健康，选择失败次数最少的代理")
		minFailures := -1
		var fallbackProxy *ProxyInfo
		for _, proxy := range pm.proxies {
			if minFailures == -1 || proxy.FailureCount < minFailures {
				minFailures = proxy.FailureCount
				fallbackProxy = proxy
			}
		}
		if fallbackProxy != nil {
			return fallbackProxy.URL
		}
		return ""
	}

	// 负载均衡：选择已分配数量最少的代理
	minAssigned := -1
	var selectedProxy *ProxyInfo
	for _, proxy := range healthyProxies {
		if minAssigned == -1 || proxy.AssignedCount < minAssigned {
			minAssigned = proxy.AssignedCount
			selectedProxy = proxy
		}
	}

	if selectedProxy != nil {
		return selectedProxy.URL
	}
	return ""
}

// isProxyHealthy 检查代理是否健康
// 内部方法：调用者必须持有 pm.mutex（读锁即可）
func (pm *ProxyPoolManager) isProxyHealthy(proxyURL string) bool {
	for _, proxy := range pm.proxies {
		if proxy.URL == proxyURL {
			return proxy.Healthy
		}
	}
	return false
}

// getAssignedCount 获取代理的分配计数
// 内部方法：调用者必须持有 pm.mutex（读锁即可）
func (pm *ProxyPoolManager) getAssignedCount(proxyURL string) int {
	for _, proxy := range pm.proxies {
		if proxy.URL == proxyURL {
			return proxy.AssignedCount
		}
	}
	return 0
}

// createProxyClient 为代理URL创建HTTP客户端
func (pm *ProxyPoolManager) createProxyClient(proxyURL string) (*http.Client, error) {
	parsedURL, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("解析代理URL失败: %v", err)
	}

	// 检查TLS配置
	skipTLS := os.Getenv("GIN_MODE") == "debug"

	transport := &http.Transport{
		Proxy: http.ProxyURL(parsedURL),
		DialContext: (&net.Dialer{
			Timeout:   15 * time.Second,
			KeepAlive: config.HTTPClientKeepAlive,
			DualStack: true,
		}).DialContext,
		TLSHandshakeTimeout: config.HTTPClientTLSHandshakeTimeout,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: skipTLS,
			MinVersion:         tls.VersionTLS12,
			MaxVersion:         tls.VersionTLS13,
			CipherSuites: []uint16{
				tls.TLS_AES_256_GCM_SHA384,
				tls.TLS_CHACHA20_POLY1305_SHA256,
				tls.TLS_AES_128_GCM_SHA256,
			},
		},
		ForceAttemptHTTP2:  false,
		DisableCompression: false,
	}

	return &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second, // 设置请求超时
	}, nil
}

// startHealthCheck 启动健康检查协程
func (pm *ProxyPoolManager) startHealthCheck() {
	ticker := time.NewTicker(pm.healthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			pm.performHealthCheck()
		case <-pm.stopChan:
			logger.Info("停止代理健康检查")
			return
		}
	}
}

// performHealthCheck 执行健康检查
func (pm *ProxyPoolManager) performHealthCheck() {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	logger.Debug("开始代理健康检查", logger.Int("proxy_count", len(pm.proxies)))

	for _, proxy := range pm.proxies {
		client := pm.proxyClients[proxy.URL]
		if client == nil {
			logger.Warn("代理客户端不存在，跳过检查", logger.String("proxy_url", proxy.URL))
			continue
		}

		// 执行健康检查请求
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		req, err := http.NewRequestWithContext(ctx, "GET", pm.healthCheckURL, nil)
		if err != nil {
			cancel()
			logger.Warn("创建健康检查请求失败",
				logger.String("proxy_url", proxy.URL),
				logger.Err(err))
			pm.markProxyFailed(proxy)
			continue
		}

		resp, err := client.Do(req)
		cancel()

		if err != nil {
			logger.Warn("代理健康检查失败",
				logger.String("proxy_url", proxy.URL),
				logger.Err(err),
				logger.Int("failure_count", proxy.FailureCount+1))
			pm.markProxyFailed(proxy)
		} else {
			resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 500 {
				// 2xx-4xx都认为是成功（代理本身是通的）
				pm.markProxyHealthy(proxy)
			} else {
				logger.Warn("代理返回异常状态码",
					logger.String("proxy_url", proxy.URL),
					logger.Int("status_code", resp.StatusCode))
				pm.markProxyFailed(proxy)
			}
		}

		proxy.LastCheck = time.Now()
	}

	// 统计健康代理数量
	healthyCount := 0
	for _, proxy := range pm.proxies {
		if proxy.Healthy {
			healthyCount++
		}
	}

	logger.Debug("代理健康检查完成",
		logger.Int("healthy_count", healthyCount),
		logger.Int("total_count", len(pm.proxies)))
}

// markProxyFailed 标记代理失败
// 内部方法：调用者必须持有 pm.mutex
func (pm *ProxyPoolManager) markProxyFailed(proxy *ProxyInfo) {
	proxy.FailureCount++
	if proxy.FailureCount >= pm.maxFailures {
		if proxy.Healthy {
			proxy.Healthy = false
			logger.Warn("代理标记为不健康",
				logger.String("proxy_url", proxy.URL),
				logger.Int("failure_count", proxy.FailureCount))
		}
	}
}

// markProxyHealthy 标记代理健康
// 内部方法：调用者必须持有 pm.mutex
func (pm *ProxyPoolManager) markProxyHealthy(proxy *ProxyInfo) {
	if !proxy.Healthy || proxy.FailureCount > 0 {
		logger.Info("代理恢复健康",
			logger.String("proxy_url", proxy.URL),
			logger.Int("previous_failures", proxy.FailureCount))
	}
	proxy.Healthy = true
	proxy.FailureCount = 0
}

// ReportProxyFailure 报告代理失败（由外部调用）
func (pm *ProxyPoolManager) ReportProxyFailure(proxyURL string) {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	for _, proxy := range pm.proxies {
		if proxy.URL == proxyURL {
			pm.markProxyFailed(proxy)
			logger.Debug("外部报告代理失败",
				logger.String("proxy_url", proxyURL),
				logger.Int("failure_count", proxy.FailureCount))
			return
		}
	}
}

// Stop 停止代理池管理器
func (pm *ProxyPoolManager) Stop() {
	close(pm.stopChan)
}

// GetStats 获取代理池统计信息
func (pm *ProxyPoolManager) GetStats() map[string]interface{} {
	pm.mutex.RLock()
	defer pm.mutex.RUnlock()

	stats := make(map[string]interface{})
	stats["total_proxies"] = len(pm.proxies)

	healthyCount := 0
	proxyStats := make([]map[string]interface{}, 0, len(pm.proxies))

	for _, proxy := range pm.proxies {
		if proxy.Healthy {
			healthyCount++
		}
		proxyStats = append(proxyStats, map[string]interface{}{
			"url":            proxy.URL,
			"healthy":        proxy.Healthy,
			"failure_count":  proxy.FailureCount,
			"assigned_count": proxy.AssignedCount,
			"last_check":     proxy.LastCheck,
		})
	}

	stats["healthy_count"] = healthyCount
	stats["assigned_tokens"] = len(pm.tokenProxyMap)
	stats["proxies"] = proxyStats

	return stats
}

// ResetTokenProxy 重置token的代理绑定（用于强制重新分配）
func (pm *ProxyPoolManager) ResetTokenProxy(tokenIndex string) {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	if proxyURL, exists := pm.tokenProxyMap[tokenIndex]; exists {
		delete(pm.tokenProxyMap, tokenIndex)
		// 减少分配计数
		for _, proxy := range pm.proxies {
			if proxy.URL == proxyURL && proxy.AssignedCount > 0 {
				proxy.AssignedCount--
				break
			}
		}
		logger.Debug("重置token代理绑定",
			logger.String("token_index", tokenIndex),
			logger.String("proxy_url", proxyURL))
	}
}

// 辅助函数：从环境变量获取配置

func getHealthCheckURL() string {
	url := os.Getenv("KIRO_PROXY_HEALTH_CHECK_URL")
	if url == "" {
		url = "https://www.google.com" // 默认健康检查URL
	}
	return url
}

func getHealthCheckInterval() time.Duration {
	interval := os.Getenv("KIRO_PROXY_HEALTH_CHECK_INTERVAL")
	if interval == "" {
		return 60 * time.Second // 默认60秒
	}
	duration, err := time.ParseDuration(interval)
	if err != nil {
		logger.Warn("无效的健康检查间隔配置，使用默认值60s",
			logger.String("invalid_value", interval))
		return 60 * time.Second
	}
	return duration
}

func getMaxProxyFailures() int {
	maxFailures := os.Getenv("KIRO_PROXY_MAX_FAILURES")
	if maxFailures == "" {
		return 3 // 默认3次失败
	}
	var count int
	if _, err := fmt.Sscanf(maxFailures, "%d", &count); err != nil || count <= 0 {
		logger.Warn("无效的最大失败次数配置，使用默认值3",
			logger.String("invalid_value", maxFailures))
		return 3
	}
	return count
}

// LoadProxyPoolFromEnv 从环境变量加载代理池配置
func LoadProxyPoolFromEnv() []string {
	proxyListStr := os.Getenv("KIRO_PROXY_POOL")
	if proxyListStr == "" {
		return nil
	}

	// 支持逗号分隔或换行分隔
	proxyListStr = strings.ReplaceAll(proxyListStr, "\n", ",")
	proxies := strings.Split(proxyListStr, ",")

	var validProxies []string
	for _, proxy := range proxies {
		proxy = strings.TrimSpace(proxy)
		if proxy != "" {
			validProxies = append(validProxies, proxy)
		}
	}

	if len(validProxies) > 0 {
		logger.Info("从环境变量加载代理池",
			logger.Int("proxy_count", len(validProxies)))
	}

	return validProxies
}
