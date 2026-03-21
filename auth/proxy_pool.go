package auth

import (
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

// proxyBlacklistDuration 代理黑名单持续时间（失败后冷却1小时）
const proxyBlacklistDuration = 1 * time.Hour

// ProxyInfo 代理信息
type ProxyInfo struct {
	URL              string    `json:"url"`               // 代理URL，格式：http://username:password@ip:port
	LastCheck        time.Time `json:"last_check"`        // 最后使用时间
	FailureCount     int       `json:"failure_count"`     // 累计失败次数
	AssignedCount    int       `json:"-"`                 // 已分配账号数（运行时统计）
	BlacklistedUntil time.Time `json:"blacklisted_until"` // 黑名单解除时间
}

// IsAvailable 检查代理是否可用（未在黑名单中）
func (p *ProxyInfo) IsAvailable() bool {
	if p.BlacklistedUntil.IsZero() {
		return true
	}
	return time.Now().After(p.BlacklistedUntil)
}

// ProxyPoolManager 代理池管理器
// 简化模型：无定期健康检查，请求失败时立即拉黑1小时，到期自动恢复
type ProxyPoolManager struct {
	proxies       []*ProxyInfo            // 代理池
	tokenProxyMap map[string]string       // token索引 -> 代理URL映射（会话保持）
	proxyClients  map[string]*http.Client // 代理URL -> HTTP客户端缓存
	mutex         sync.RWMutex
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
			URL:       proxyURL,
			LastCheck: time.Now(),
		})
	}

	if len(proxies) == 0 {
		return nil, fmt.Errorf("没有有效的代理URL")
	}

	manager := &ProxyPoolManager{
		proxies:      proxies,
		tokenProxyMap: make(map[string]string),
		proxyClients:  make(map[string]*http.Client),
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

	logger.Info("代理池初始化成功（无定期检查，失败即拉黑1小时）",
		logger.Int("proxy_count", len(proxies)))

	return manager, nil
}

// GetProxyForToken 为token获取代理（延迟分配 + 会话保持）
// 返回值：proxyURL, client, error
// 如果所有代理都在黑名单中，返回 "", nil, nil（降级为直连）
func (pm *ProxyPoolManager) GetProxyForToken(tokenIndex string) (string, *http.Client, error) {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	// 检查是否已分配代理（会话保持）
	if proxyURL, exists := pm.tokenProxyMap[tokenIndex]; exists {
		// 检查代理是否仍可用（未在黑名单中）
		if pm.isProxyAvailable(proxyURL) {
			client := pm.proxyClients[proxyURL]
			return proxyURL, client, nil
		}
		// 代理在黑名单中，需要重新分配
		delete(pm.tokenProxyMap, tokenIndex)
	}

	// 选择可用代理
	proxyURL := pm.selectAvailableProxy()
	if proxyURL == "" {
		// 所有代理都在黑名单中，降级为直连
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

// selectAvailableProxy 选择可用代理进行分配（负载均衡）
// 内部方法：调用者必须持有 pm.mutex
func (pm *ProxyPoolManager) selectAvailableProxy() string {
	var availableProxies []*ProxyInfo
	for _, proxy := range pm.proxies {
		if proxy.IsAvailable() {
			availableProxies = append(availableProxies, proxy)
		}
	}

	if len(availableProxies) == 0 {
		logger.Warn("所有代理都在黑名单中，降级为直连")
		return ""
	}

	// 负载均衡：选择已分配数量最少的代理
	minAssigned := -1
	var selectedProxy *ProxyInfo
	for _, proxy := range availableProxies {
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

// isProxyAvailable 检查代理是否可用（未在黑名单中）
// 内部方法：调用者必须持有 pm.mutex（读锁即可）
func (pm *ProxyPoolManager) isProxyAvailable(proxyURL string) bool {
	for _, proxy := range pm.proxies {
		if proxy.URL == proxyURL {
			return proxy.IsAvailable()
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

// ReportProxyFailure 报告代理失败，立即拉黑1小时
func (pm *ProxyPoolManager) ReportProxyFailure(proxyURL string) {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	for _, proxy := range pm.proxies {
		if proxy.URL == proxyURL {
			proxy.FailureCount++
			proxy.BlacklistedUntil = time.Now().Add(proxyBlacklistDuration)
			logger.Warn("代理失败，拉黑1小时",
				logger.String("proxy_url", proxyURL),
				logger.Int("failure_count", proxy.FailureCount),
				logger.String("blacklisted_until", proxy.BlacklistedUntil.Format(time.RFC3339)))
			return
		}
	}
}

// Stop 停止代理池管理器（无后台任务，仅保留接口兼容）
func (pm *ProxyPoolManager) Stop() {
	// 无定期健康检查，无需停止
	logger.Info("代理池管理器已停止")
}

// GetStats 获取代理池统计信息
func (pm *ProxyPoolManager) GetStats() map[string]interface{} {
	pm.mutex.RLock()
	defer pm.mutex.RUnlock()

	stats := make(map[string]interface{})
	stats["total_proxies"] = len(pm.proxies)

	availableCount := 0
	blacklistedCount := 0
	now := time.Now()
	proxyStats := make([]map[string]interface{}, 0, len(pm.proxies))

	for _, proxy := range pm.proxies {
		isBlacklisted := !proxy.BlacklistedUntil.IsZero() && now.Before(proxy.BlacklistedUntil)
		if isBlacklisted {
			blacklistedCount++
		} else {
			availableCount++
		}
		proxyStats = append(proxyStats, map[string]interface{}{
			"url":               proxy.URL,
			"available":         !isBlacklisted,
			"failure_count":     proxy.FailureCount,
			"assigned_count":    proxy.AssignedCount,
			"last_check":        proxy.LastCheck,
			"blacklisted_until": proxy.BlacklistedUntil,
			"is_blacklisted":    isBlacklisted,
		})
	}

	stats["available_count"] = availableCount
	stats["blacklisted_count"] = blacklistedCount
	stats["assigned_tokens"] = len(pm.tokenProxyMap)
	stats["proxies"] = proxyStats

	return stats
}

// GetHealthyProxyCount 获取可用代理数量（未在黑名单中的）
func (pm *ProxyPoolManager) GetHealthyProxyCount() int {
	pm.mutex.RLock()
	defer pm.mutex.RUnlock()

	count := 0
	for _, proxy := range pm.proxies {
		if proxy.IsAvailable() {
			count++
		}
	}
	return count
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

// CleanupAndReindexTokenMappings 清理已删除token的代理映射并重建索引
// deletedIndices: 已删除的token索引列表（降序排列）
// 在批量删除token后调用，确保代理映射与新的token索引一致
func (pm *ProxyPoolManager) CleanupAndReindexTokenMappings(deletedIndices []int) {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	if len(deletedIndices) == 0 {
		return
	}

	// 构建删除集合
	deletedSet := make(map[int]bool, len(deletedIndices))
	for _, idx := range deletedIndices {
		deletedSet[idx] = true
	}

	// 重建 tokenProxyMap：删除已删除索引的映射，重新计算剩余索引
	newTokenProxyMap := make(map[string]string)
	for tokenIndexStr, proxyURL := range pm.tokenProxyMap {
		var oldIndex int
		if _, err := fmt.Sscanf(tokenIndexStr, "%d", &oldIndex); err != nil {
			continue // 跳过非数字索引
		}

		if deletedSet[oldIndex] {
			// 已删除的token，减少代理的分配计数
			for _, proxy := range pm.proxies {
				if proxy.URL == proxyURL && proxy.AssignedCount > 0 {
					proxy.AssignedCount--
					break
				}
			}
			continue
		}

		// 计算新索引（减去在它之前被删除的数量）
		newIndex := oldIndex
		for _, deletedIdx := range deletedIndices {
			if deletedIdx < oldIndex {
				newIndex--
			}
		}

		newTokenProxyMap[fmt.Sprintf("%d", newIndex)] = proxyURL
	}

	pm.tokenProxyMap = newTokenProxyMap

	logger.Debug("清理并重建token代理映射",
		logger.Int("deleted_count", len(deletedIndices)),
		logger.Int("remaining_mappings", len(newTokenProxyMap)))
}

// GetProxyList 获取代理列表（用于 API 展示）
func (pm *ProxyPoolManager) GetProxyList() []map[string]interface{} {
	pm.mutex.RLock()
	defer pm.mutex.RUnlock()

	now := time.Now()
	result := make([]map[string]interface{}, 0, len(pm.proxies))
	for i, proxy := range pm.proxies {
		isBlacklisted := !proxy.BlacklistedUntil.IsZero() && now.Before(proxy.BlacklistedUntil)
		result = append(result, map[string]interface{}{
			"index":             i,
			"url":               maskProxyURL(proxy.URL),
			"healthy":           !isBlacklisted,
			"failure_count":     proxy.FailureCount,
			"assigned_count":    proxy.AssignedCount,
			"last_check":        proxy.LastCheck,
			"blacklisted_until": proxy.BlacklistedUntil,
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
		URL:       proxyURL,
		LastCheck: time.Now(),
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
