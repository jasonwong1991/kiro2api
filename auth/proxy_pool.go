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

// 代理冷却时间常量
const (
	// baseCooldownDuration 基础冷却时间（第1次失败后冷却1小时）
	baseCooldownDuration = 1 * time.Hour
	// maxCooldownDuration 最大冷却时间（24小时封顶）
	maxCooldownDuration = 24 * time.Hour
)

// ProxyInfo 代理信息
type ProxyInfo struct {
	URL              string    `json:"url"`                // 代理URL，格式：http://username:password@ip:port
	Healthy          bool      `json:"healthy"`            // 健康状态
	LastCheck        time.Time `json:"last_check"`         // 最后检查时间
	FailureCount     int       `json:"failure_count"`      // 连续失败次数
	AssignedCount    int       `json:"-"`                  // 已分配账号数（运行时统计）
	BlacklistedUntil time.Time `json:"blacklisted_until"`  // 黑名单解除时间（指数退避冷却）
	BlacklistCount   int       `json:"blacklist_count"`    // 被拉黑次数（用于计算指数退避）
}

// ProxyPoolManager 代理池管理器
type ProxyPoolManager struct {
	proxies             []*ProxyInfo            // 代理池
	tokenProxyMap       map[string]string       // token索引 -> 代理URL映射（会话保持）
	proxyClients        map[string]*http.Client // 代理URL -> HTTP客户端缓存
	mutex               sync.RWMutex
	healthCheckURL      string        // 健康检查URL
	healthCheckInterval time.Duration // 健康检查间隔
	maxFailures         int           // 最大失败次数（超过则标记为不健康）
	stopChan            chan struct{} // 停止信号
	proxyDisabled       bool          // 代理禁用标志（所有代理不健康时自动禁用）
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
// 返回值：proxyURL, client, error
// 如果所有代理都不可用，返回 "", nil, nil（降级为不使用代理）
// 注意：不在此处做预检测，完全信任后台健康检查结果，避免阻塞主线程
func (pm *ProxyPoolManager) GetProxyForToken(tokenIndex string) (string, *http.Client, error) {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	// 检查代理是否已禁用（所有代理不健康时自动禁用）
	if pm.proxyDisabled {
		return "", nil, nil
	}

	// 检查是否已分配代理（会话保持）
	if proxyURL, exists := pm.tokenProxyMap[tokenIndex]; exists {
		// 检查代理是否仍然健康
		if pm.isProxyHealthy(proxyURL) {
			client := pm.proxyClients[proxyURL]
			return proxyURL, client, nil
		}
		// 代理不健康，需要重新分配
		delete(pm.tokenProxyMap, tokenIndex)
	}

	// 选择健康代理
	proxyURL := pm.selectProxyForAssignment()
	if proxyURL == "" {
		return "", nil, nil
	}

	client := pm.proxyClients[proxyURL]

	// 绑定关系
	pm.tokenProxyMap[tokenIndex] = proxyURL

	// 更新分配计数
	for _, proxy := range pm.proxies {
		if proxy.URL == proxyURL {
			proxy.AssignedCount++
			break
		}
	}

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
		// 所有代理都不健康，禁用代理功能，等待健康检查恢复
		if !pm.proxyDisabled {
			pm.proxyDisabled = true
			logger.Warn("所有代理都不健康，禁用代理功能，等待健康检查恢复")
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
		// 连接池配置（防止连接耗尽）
		MaxIdleConns:          500, // 全局最大空闲连接数
		MaxIdleConnsPerHost:   50,  // 每个host最大空闲连接数
		MaxConnsPerHost:       500, // 每个host最大连接数（包括活跃+空闲）
		IdleConnTimeout:       90 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ForceAttemptHTTP2:     true,
		DisableCompression:    false,
	}

	return &http.Client{
		Transport: transport,
		// 不设置 client-level Timeout，因为流式请求（SSE）可能需要更长时间
		// 超时应由请求级别的 context 控制
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

// healthCheckResult 健康检查结果
type healthCheckResult struct {
	proxyURL   string
	success    bool
	statusCode int
	err        error
}

// performHealthCheck 执行健康检查（并发，不阻塞主线程）
func (pm *ProxyPoolManager) performHealthCheck() {
	// 先获取代理列表快照（短暂持锁）
	pm.mutex.RLock()
	proxyCount := len(pm.proxies)
	type proxySnapshot struct {
		url              string
		client           *http.Client
		blacklistedUntil time.Time
	}
	snapshots := make([]proxySnapshot, 0, proxyCount)
	for _, proxy := range pm.proxies {
		client := pm.proxyClients[proxy.URL]
		if client != nil {
			snapshots = append(snapshots, proxySnapshot{
				url:              proxy.URL,
				client:           client,
				blacklistedUntil: proxy.BlacklistedUntil,
			})
		}
	}
	healthCheckURL := pm.healthCheckURL
	pm.mutex.RUnlock()

	if len(snapshots) == 0 {
		return
	}

	// 过滤掉仍在黑名单冷却期内的代理
	now := time.Now()
	activeSnapshots := make([]proxySnapshot, 0, len(snapshots))
	skippedCount := 0
	for _, snap := range snapshots {
		if !snap.blacklistedUntil.IsZero() && now.Before(snap.blacklistedUntil) {
			// 仍在冷却期，跳过健康检查
			skippedCount++
			continue
		}
		activeSnapshots = append(activeSnapshots, snap)
	}

	if skippedCount > 0 {
		logger.Debug("跳过黑名单冷却期内的代理",
			logger.Int("skipped_count", skippedCount),
			logger.Int("active_count", len(activeSnapshots)))
	}

	if len(activeSnapshots) == 0 {
		logger.Debug("所有代理都在冷却期，跳过本次健康检查")
		return
	}

	logger.Debug("开始代理健康检查", logger.Int("proxy_count", len(activeSnapshots)))

	// 并发执行健康检查（不持锁）
	results := make(chan healthCheckResult, len(activeSnapshots))
	var wg sync.WaitGroup

	for _, snap := range activeSnapshots {
		wg.Add(1)
		go func(proxyURL string, client *http.Client) {
			defer wg.Done()

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			req, err := http.NewRequestWithContext(ctx, "GET", healthCheckURL, nil)
			if err != nil {
				results <- healthCheckResult{proxyURL: proxyURL, success: false, err: err}
				return
			}

			resp, err := client.Do(req)
			if err != nil {
				results <- healthCheckResult{proxyURL: proxyURL, success: false, err: err}
				return
			}
			resp.Body.Close()

			success := resp.StatusCode >= 200 && resp.StatusCode < 500
			results <- healthCheckResult{proxyURL: proxyURL, success: success, statusCode: resp.StatusCode}
		}(snap.url, snap.client)
	}

	// 等待所有检查完成
	go func() {
		wg.Wait()
		close(results)
	}()

	// 收集结果并更新状态（短暂持锁）
	resultMap := make(map[string]healthCheckResult)
	for result := range results {
		resultMap[result.proxyURL] = result
	}

	pm.mutex.Lock()
	for _, proxy := range pm.proxies {
		result, exists := resultMap[proxy.URL]
		if !exists {
			continue
		}

		proxy.LastCheck = time.Now()

		if result.success {
			pm.markProxyHealthy(proxy)
		} else {
			if result.err != nil {
				logger.Warn("代理健康检查失败",
					logger.String("proxy_url", proxy.URL),
					logger.Err(result.err),
					logger.Int("failure_count", proxy.FailureCount+1))
			} else {
				logger.Warn("代理返回异常状态码",
					logger.String("proxy_url", proxy.URL),
					logger.Int("status_code", result.statusCode))
			}
			pm.markProxyFailed(proxy)
		}
	}

	// 统计健康代理数量
	healthyCount := 0
	for _, proxy := range pm.proxies {
		if proxy.Healthy {
			healthyCount++
		}
	}

	// 如果有健康代理且代理功能已禁用，重新启用
	if healthyCount > 0 && pm.proxyDisabled {
		pm.proxyDisabled = false
		logger.Info("检测到健康代理，重新启用代理功能",
			logger.Int("healthy_count", healthyCount))
	}
	pm.mutex.Unlock()

	logger.Debug("代理健康检查完成",
		logger.Int("healthy_count", healthyCount),
		logger.Int("total_count", proxyCount))
}

// markProxyFailed 标记代理失败
// 内部方法：调用者必须持有 pm.mutex
func (pm *ProxyPoolManager) markProxyFailed(proxy *ProxyInfo) {
	proxy.FailureCount++
	if proxy.FailureCount >= pm.maxFailures {
		if proxy.Healthy {
			proxy.Healthy = false
			proxy.BlacklistCount++

			// 计算指数退避冷却时间：1小时 * 2^(blacklistCount-1)，最大24小时
			cooldown := baseCooldownDuration * time.Duration(1<<(proxy.BlacklistCount-1))
			if cooldown > maxCooldownDuration {
				cooldown = maxCooldownDuration
			}
			proxy.BlacklistedUntil = time.Now().Add(cooldown)

			logger.Warn("代理标记为不健康并加入黑名单",
				logger.String("proxy_url", proxy.URL),
				logger.Int("failure_count", proxy.FailureCount),
				logger.Int("blacklist_count", proxy.BlacklistCount),
				logger.Duration("cooldown", cooldown),
				logger.String("blacklisted_until", proxy.BlacklistedUntil.Format(time.RFC3339)))
		}
	}
}

// markProxyHealthy 标记代理健康
// 内部方法：调用者必须持有 pm.mutex
func (pm *ProxyPoolManager) markProxyHealthy(proxy *ProxyInfo) {
	if !proxy.Healthy || proxy.FailureCount > 0 {
		logger.Info("代理恢复健康",
			logger.String("proxy_url", proxy.URL),
			logger.Int("previous_failures", proxy.FailureCount),
			logger.Int("blacklist_count", proxy.BlacklistCount))
	}
	proxy.Healthy = true
	proxy.FailureCount = 0
	// 注意：不重置 BlacklistCount，保留历史记录用于下次计算退避时间
	// 只有连续成功多次后才考虑重置（可选优化）
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
	blacklistedCount := 0
	now := time.Now()
	proxyStats := make([]map[string]interface{}, 0, len(pm.proxies))

	for _, proxy := range pm.proxies {
		if proxy.Healthy {
			healthyCount++
		}
		isBlacklisted := !proxy.BlacklistedUntil.IsZero() && now.Before(proxy.BlacklistedUntil)
		if isBlacklisted {
			blacklistedCount++
		}
		proxyStats = append(proxyStats, map[string]interface{}{
			"url":               proxy.URL,
			"healthy":           proxy.Healthy,
			"failure_count":     proxy.FailureCount,
			"assigned_count":    proxy.AssignedCount,
			"last_check":        proxy.LastCheck,
			"blacklisted_until": proxy.BlacklistedUntil,
			"blacklist_count":   proxy.BlacklistCount,
			"is_blacklisted":    isBlacklisted,
		})
	}

	stats["healthy_count"] = healthyCount
	stats["blacklisted_count"] = blacklistedCount
	stats["assigned_tokens"] = len(pm.tokenProxyMap)
	stats["proxy_disabled"] = pm.proxyDisabled
	stats["proxies"] = proxyStats

	return stats
}

// GetHealthyProxyCount 获取健康代理数量
func (pm *ProxyPoolManager) GetHealthyProxyCount() int {
	pm.mutex.RLock()
	defer pm.mutex.RUnlock()

	count := 0
	for _, proxy := range pm.proxies {
		if proxy.Healthy {
			count++
		}
	}
	return count
}

// IsProxyDisabled 检查代理功能是否已禁用
func (pm *ProxyPoolManager) IsProxyDisabled() bool {
	pm.mutex.RLock()
	defer pm.mutex.RUnlock()
	return pm.proxyDisabled
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

// GetProxyList 获取代理列表（用于 API 展示）
func (pm *ProxyPoolManager) GetProxyList() []map[string]interface{} {
	pm.mutex.RLock()
	defer pm.mutex.RUnlock()

	result := make([]map[string]interface{}, 0, len(pm.proxies))
	for i, proxy := range pm.proxies {
		result = append(result, map[string]interface{}{
			"index":          i,
			"url":            maskProxyURL(proxy.URL),
			"healthy":        proxy.Healthy,
			"failure_count":  proxy.FailureCount,
			"assigned_count": proxy.AssignedCount,
			"last_check":     proxy.LastCheck,
		})
	}
	return result
}

// AddProxy 添加代理
func (pm *ProxyPoolManager) AddProxy(proxyURL string) error {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	// 验证代理 URL 格式
	if _, err := url.Parse(proxyURL); err != nil {
		return fmt.Errorf("无效的代理 URL: %w", err)
	}

	// 检查是否已存在
	for _, proxy := range pm.proxies {
		if proxy.URL == proxyURL {
			return fmt.Errorf("代理已存在")
		}
	}

	// 创建代理客户端
	client, err := pm.createProxyClient(proxyURL)
	if err != nil {
		return fmt.Errorf("创建代理客户端失败: %w", err)
	}

	// 添加代理
	pm.proxies = append(pm.proxies, &ProxyInfo{
		URL:          proxyURL,
		Healthy:      true,
		LastCheck:    time.Now(),
		FailureCount: 0,
	})
	pm.proxyClients[proxyURL] = client

	logger.Info("添加代理",
		logger.String("proxy_url", proxyURL),
		logger.Int("total_count", len(pm.proxies)))

	return nil
}

// RemoveProxy 删除代理
func (pm *ProxyPoolManager) RemoveProxy(index int) error {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	if index < 0 || index >= len(pm.proxies) {
		return fmt.Errorf("索引超出范围: %d", index)
	}

	proxyURL := pm.proxies[index].URL

	// 删除代理客户端
	delete(pm.proxyClients, proxyURL)

	// 删除代理
	pm.proxies = append(pm.proxies[:index], pm.proxies[index+1:]...)

	// 清理使用该代理的 token 绑定
	for tokenIndex, url := range pm.tokenProxyMap {
		if url == proxyURL {
			delete(pm.tokenProxyMap, tokenIndex)
		}
	}

	logger.Info("删除代理",
		logger.Int("index", index),
		logger.Int("remaining_count", len(pm.proxies)))

	return nil
}

// maskProxyURL 遮蔽代理 URL 中的敏感信息
func maskProxyURL(proxyURL string) string {
	parsed, err := url.Parse(proxyURL)
	if err != nil {
		return "****"
	}

	// 遮蔽用户名和密码
	if parsed.User != nil {
		username := parsed.User.Username()
		if len(username) > 2 {
			username = username[:2] + "****"
		}
		parsed.User = url.User(username)
	}

	return parsed.String()
}

// 辅助函数：从环境变量获取配置

func getHealthCheckURL() string {
	url := os.Getenv("KIRO_PROXY_HEALTH_CHECK_URL")
	if url == "" {
		url = "http://cp.cloudflare.com/generate_204" // 默认健康检查URL
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
// 支持两种方式：
// 1. 直接配置代理列表（逗号或换行分隔）
// 2. 配置文件路径（如 /path/to/proxies.txt），文件中一行一个代理
func LoadProxyPoolFromEnv() []string {
	proxyListStr := os.Getenv("KIRO_PROXY_POOL")
	if proxyListStr == "" {
		return nil
	}

	proxyListStr = strings.TrimSpace(proxyListStr)

	// 检查是否为文件路径
	if isFilePath(proxyListStr) {
		proxies, err := loadProxiesFromFile(proxyListStr)
		if err != nil {
			logger.Error("从文件加载代理池失败",
				logger.String("file_path", proxyListStr),
				logger.Err(err))
			return nil
		}
		if len(proxies) > 0 {
			logger.Info("从文件加载代理池",
				logger.String("file_path", proxyListStr),
				logger.Int("proxy_count", len(proxies)))
		}
		return proxies
	}

	// 直接解析代理列表（支持逗号分隔或换行分隔）
	proxyListStr = strings.ReplaceAll(proxyListStr, "\n", ",")
	proxies := strings.Split(proxyListStr, ",")

	var validProxies []string
	for _, proxy := range proxies {
		proxy = strings.TrimSpace(proxy)
		if proxy != "" && !strings.HasPrefix(proxy, "#") {
			validProxies = append(validProxies, proxy)
		}
	}

	if len(validProxies) > 0 {
		logger.Info("从环境变量加载代理池",
			logger.Int("proxy_count", len(validProxies)))
	}

	return validProxies
}

// isFilePath 判断字符串是否为文件路径
func isFilePath(s string) bool {
	// 以 / 或 ./ 或 ../ 开头，或者以 .txt 结尾
	if strings.HasPrefix(s, "/") || strings.HasPrefix(s, "./") || strings.HasPrefix(s, "../") {
		return true
	}
	if strings.HasSuffix(s, ".txt") || strings.HasSuffix(s, ".conf") || strings.HasSuffix(s, ".list") {
		return true
	}
	// 检查文件是否存在
	if _, err := os.Stat(s); err == nil {
		return true
	}
	return false
}

// loadProxiesFromFile 从文件加载代理列表
func loadProxiesFromFile(filePath string) ([]string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("读取代理文件失败: %w", err)
	}

	lines := strings.Split(string(data), "\n")
	var proxies []string

	for _, line := range lines {
		line = strings.TrimSpace(line)
		// 跳过空行和注释行
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		proxies = append(proxies, line)
	}

	return proxies, nil
}
