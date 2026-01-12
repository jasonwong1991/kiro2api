package auth

import (
	"fmt"
	"kiro2api/config"
	"kiro2api/logger"
	"kiro2api/types"
	"math/rand"
	"os"
	"sync"
	"time"
)

// SelectionStrategy token选择策略类型
type SelectionStrategy string

const (
	StrategySequential SelectionStrategy = "sequential"  // 顺序选择
	StrategyRandom     SelectionStrategy = "random"      // 随机选择
	StrategyRoundRobin SelectionStrategy = "round_robin" // 轮询选择
)

// MinAvailableThreshold 最小可用余额阈值
// 余额 <= 此值视为耗尽（即只有 > 0.1 才可用）
const MinAvailableThreshold = 0.1

// TokenManager 简化的token管理器
type TokenManager struct {
	cache        *SimpleTokenCache
	configs      []AuthConfig
	mutex        sync.RWMutex
	lastRefresh  time.Time
	configOrder  []string          // 配置顺序
	currentIndex int               // 当前使用的token索引
	exhausted    map[string]bool   // 已耗尽的token记录
	invalidated  map[int]time.Time // 失效的token记录（索引 -> 失效时间）
	strategy     SelectionStrategy // token选择策略
	configPath   string            // 配置文件路径（用于同步更新）

	// 活跃池相关字段（用于 round_robin 策略 + KIRO_BATCH_SIZE）
	batchSize      int   // 活跃池大小（0=使用全部账号）
	activePool     []int // 活跃账号池（存储配置索引）
	poolRoundRobin int   // 活跃池内轮询索引

	// 自动管理相关字段
	autoRemoveInvalid bool // 是否自动删除失效的账号

	// 代理池管理器
	proxyPool *ProxyPoolManager // 代理池（可选）

	// 定时刷新相关字段
	refreshTicker *time.Ticker // 定时器
	refreshStop   chan bool    // 停止信号
}

// SimpleTokenCache 简化的token缓存（纯数据结构，无锁）
// 所有并发访问由 TokenManager.mutex 统一管理
type SimpleTokenCache struct {
	tokens map[string]*CachedToken
	ttl    time.Duration
}

// CachedToken 缓存的token信息
type CachedToken struct {
	Token         types.TokenInfo
	UsageInfo     *types.UsageLimits
	CachedAt      time.Time // 上次成功刷新的时间
	LastCheckAt   time.Time // 上次尝试刷新的时间（包括失败的尝试）
	LastUsed      time.Time
	Available     float64
	NextResetTime time.Time // 额度重置时间
}

// NewSimpleTokenCache 创建简单的token缓存
func NewSimpleTokenCache(ttl time.Duration) *SimpleTokenCache {
	return &SimpleTokenCache{
		tokens: make(map[string]*CachedToken),
		ttl:    ttl,
	}
}

// NewTokenManager 创建新的token管理器
func NewTokenManager(configs []AuthConfig) *TokenManager {
	// 生成配置顺序
	configOrder := generateConfigOrder(configs)

	// 从环境变量读取策略配置
	strategy := getSelectionStrategy()

	// 获取配置文件路径（如果使用文件配置）
	configPath := getConfigPath()

	// 获取批次大小配置
	batchSize := getBatchSize()

	// 获取自动删除失效账号配置
	autoRemove := getAutoRemoveInvalid()

	// 初始化代理池（如果配置了）
	proxyURLs := LoadProxyPoolFromEnv()
	var proxyPool *ProxyPoolManager
	if len(proxyURLs) > 0 {
		var err error
		proxyPool, err = NewProxyPoolManager(proxyURLs)
		if err != nil {
			logger.Warn("初始化代理池失败，将不使用代理",
				logger.Err(err),
				logger.Int("proxy_count", len(proxyURLs)))
		} else {
			logger.Info("代理池初始化成功",
				logger.Int("proxy_count", len(proxyURLs)))
		}
	}

	logger.Info("TokenManager初始化",
		logger.Int("config_count", len(configs)),
		logger.Int("config_order_count", len(configOrder)),
		logger.String("selection_strategy", string(strategy)),
		logger.Int("batch_size", batchSize),
		logger.Bool("auto_remove_invalid", autoRemove),
		logger.String("config_path", configPath),
		logger.Bool("proxy_pool_enabled", proxyPool != nil))

	tm := &TokenManager{
		cache:             NewSimpleTokenCache(config.TokenCacheTTL),
		configs:           configs,
		configOrder:       configOrder,
		currentIndex:      0,
		exhausted:         make(map[string]bool),
		invalidated:       make(map[int]time.Time),
		strategy:          strategy,
		configPath:        configPath,
		batchSize:         batchSize,
		activePool:        []int{}, // 初始为空，首次使用时构建
		poolRoundRobin:    0,
		autoRemoveInvalid: autoRemove,
		proxyPool:         proxyPool,
		refreshStop:       make(chan bool, 1),
	}

	// 如果使用活跃池策略，启动定时刷新任务
	if batchSize > 0 && strategy == StrategyRoundRobin {
		// 每5分钟刷新一次活跃池中快要过期的 token
		tm.refreshTicker = time.NewTicker(5 * time.Minute)
		go tm.startPeriodicRefresh()
		logger.Info("启动活跃池定时刷新任务", logger.Duration("interval", 5*time.Minute))
	}

	return tm
}

// getConfigPath 获取配置文件路径
func getConfigPath() string {
	authToken := os.Getenv("KIRO_AUTH_TOKEN")
	if authToken == "" {
		return ""
	}

	// 如果是 JSON 字符串，不是文件路径
	if isLikelyJSONConfigValue(authToken) {
		return ""
	}

	// 否则认为是文件路径（即使文件暂时不存在，也返回路径用于后续保存）
	return authToken
}

// getSelectionStrategy 从环境变量获取选择策略
func getSelectionStrategy() SelectionStrategy {
	strategyEnv := os.Getenv("TOKEN_SELECTION_STRATEGY")
	if strategyEnv == "" {
		return StrategyRoundRobin // 默认使用轮询策略
	}

	strategy := SelectionStrategy(strategyEnv)
	switch strategy {
	case StrategySequential, StrategyRandom, StrategyRoundRobin:
		return strategy
	default:
		logger.Warn("未知的token选择策略，使用默认策略",
			logger.String("invalid_strategy", strategyEnv),
			logger.String("default_strategy", string(StrategyRoundRobin)))
		return StrategyRoundRobin
	}
}

// getBatchSize 从环境变量获取批次大小
func getBatchSize() int {
	batchSizeEnv := os.Getenv("KIRO_BATCH_SIZE")
	if batchSizeEnv == "" {
		return 0 // 0 表示不使用分批策略（使用全部账号）
	}

	var batchSize int
	if _, err := fmt.Sscanf(batchSizeEnv, "%d", &batchSize); err != nil {
		logger.Warn("无效的批次大小配置，使用默认值0",
			logger.String("invalid_value", batchSizeEnv),
			logger.Err(err))
		return 0
	}

	if batchSize < 0 {
		logger.Warn("批次大小不能为负数，使用默认值0",
			logger.Int("invalid_value", batchSize))
		return 0
	}

	return batchSize
}

// getAutoRemoveInvalid 从环境变量获取是否自动删除失效账号
func getAutoRemoveInvalid() bool {
	autoRemoveEnv := os.Getenv("KIRO_AUTO_REMOVE_INVALID")
	if autoRemoveEnv == "" {
		return false // 默认不自动删除，保守策略
	}

	// 支持的值: true, false, 1, 0, yes, no
	switch autoRemoveEnv {
	case "true", "1", "yes", "YES", "True", "TRUE":
		return true
	case "false", "0", "no", "NO", "False", "FALSE":
		return false
	default:
		logger.Warn("无效的自动删除配置，使用默认值false",
			logger.String("invalid_value", autoRemoveEnv))
		return false
	}
}

// getBestToken 获取最优可用token
// 统一锁管理：所有操作在单一锁保护下完成，避免多次加锁/解锁
func (tm *TokenManager) getBestToken() (types.TokenInfo, error) {
	tm.mutex.Lock()
	defer tm.mutex.Unlock()

	// 已移除全局刷新逻辑，改为按需刷新单个token

	// 选择最优token（内部方法，不加锁）
	bestToken := tm.selectBestTokenUnlocked()
	if bestToken == nil {
		return types.TokenInfo{}, fmt.Errorf("没有可用的token")
	}

	// 更新最后使用时间（在锁内，安全）
	bestToken.LastUsed = time.Now()

	// 扣减本地余额（API 不返回余额，必须手动计算）
	// 使用 max 保证余额不为负，避免负值显示在 Dashboard
	oldAvailable := bestToken.Available
	if bestToken.Available > 0 {
		bestToken.Available = max(bestToken.Available-1.0, 0)
	}

	// 如果余额降到接近0（< 1.0），异步触发刷新来验证真实余额
	// 避免死循环：刷新后如果真实余额 < 0.1（MinAvailableThreshold），账号将不再被选中
	if bestToken.Available < 1.0 && oldAvailable >= 1.0 {
		// 提取 token index 用于异步刷新
		tokenIndex := -1
		for i := range tm.configs {
			cacheKey := fmt.Sprintf(config.TokenCacheKeyFormat, i)
			if tm.cache.tokens[cacheKey] == bestToken {
				tokenIndex = i
				break
			}
		}
		if tokenIndex >= 0 {
			// 异步刷新，不阻塞当前请求
			go tm.verifyLowBalanceToken(tokenIndex)
		}
	}

	return bestToken.Token, nil
}

// GetBestTokenWithUsage 获取最优可用token（包含使用信息）
// 统一锁管理：所有操作在单一锁保护下完成
func (tm *TokenManager) GetBestTokenWithUsage() (*types.TokenWithUsage, error) {
	tm.mutex.Lock()
	defer tm.mutex.Unlock()

	// 已移除全局刷新逻辑，改为按需刷新单个token

	// 选择最优token（内部方法，不加锁）
	bestToken := tm.selectBestTokenUnlocked()
	if bestToken == nil {
		return nil, fmt.Errorf("没有可用的token")
	}

	// 更新最后使用时间（在锁内，安全）
	bestToken.LastUsed = time.Now()

	// 扣减本地余额（API 不返回余额，必须手动计算）
	oldAvailable := bestToken.Available
	if bestToken.Available > 0 {
		bestToken.Available = max(bestToken.Available-1.0, 0)
	}
	available := bestToken.Available

	// 如果余额降到接近0（< 1.0），异步触发刷新来验证真实余额
	if bestToken.Available < 1.0 && oldAvailable >= 1.0 {
		tokenIndex := -1
		for i := range tm.configs {
			cacheKey := fmt.Sprintf(config.TokenCacheKeyFormat, i)
			if tm.cache.tokens[cacheKey] == bestToken {
				tokenIndex = i
				break
			}
		}
		if tokenIndex >= 0 {
			go tm.verifyLowBalanceToken(tokenIndex)
		}
	}

	// 构造 TokenWithUsage
	tokenWithUsage := &types.TokenWithUsage{
		TokenInfo:       bestToken.Token,
		UsageLimits:     bestToken.UsageInfo,
		AvailableCount:  available, // 使用扣减后的余额
		LastUsageCheck:  bestToken.LastUsed,
		IsUsageExceeded: available <= 0,
	}

	logger.Debug("返回TokenWithUsage",
		logger.Float64("available_count", available),
		logger.Bool("is_exceeded", tokenWithUsage.IsUsageExceeded))

	return tokenWithUsage, nil
}

// selectBestTokenUnlocked 按配置顺序选择下一个可用token
// 内部方法：调用者必须持有 tm.mutex
// 重构说明：从selectBestToken改为Unlocked后缀，明确锁约定
func (tm *TokenManager) selectBestTokenUnlocked() *CachedToken {
	// 调用者已持有 tm.mutex，无需额外加锁

	// 根据策略选择token
	switch tm.strategy {
	case StrategyRandom:
		return tm.selectRandomToken()
	case StrategyRoundRobin:
		return tm.selectRoundRobinToken()
	case StrategySequential:
		return tm.selectSequentialToken()
	default:
		logger.Warn("未知策略，回退到轮询",
			logger.String("strategy", string(tm.strategy)))
		return tm.selectRoundRobinToken()
	}
}

// selectSequentialToken 顺序选择策略（粘性策略）
func (tm *TokenManager) selectSequentialToken() *CachedToken {
	// 如果没有配置顺序，降级到按map遍历顺序
	if len(tm.configOrder) == 0 {
		for key, cached := range tm.cache.tokens {
			if cached.IsUsable() {
				logger.Debug("顺序策略选择token（无顺序配置）",
					logger.String("selected_key", key),
					logger.Float64("available_count", cached.Available))
				return cached
			}
		}
		return nil
	}

	// 从当前索引开始，找到第一个可用的token
	for attempts := 0; attempts < len(tm.configOrder); attempts++ {
		currentKey := tm.configOrder[tm.currentIndex]

		// 检查这个token是否存在且可用
		if cached, exists := tm.cache.tokens[currentKey]; exists {

			// 检查token是否可用
			if cached.IsUsable() {
				logger.Debug("顺序策略选择token",
					logger.String("selected_key", currentKey),
					logger.Int("index", tm.currentIndex),
					logger.Float64("available_count", cached.Available))
				return cached
			}
		}

		// 标记当前token为已耗尽，移动到下一个
		tm.exhausted[currentKey] = true
		tm.currentIndex = (tm.currentIndex + 1) % len(tm.configOrder)

		logger.Debug("token不可用，切换到下一个",
			logger.String("exhausted_key", currentKey),
			logger.Int("next_index", tm.currentIndex))
	}

	// 所有token都不可用
	logger.Warn("所有token都不可用",
		logger.Int("total_count", len(tm.configOrder)),
		logger.Int("exhausted_count", len(tm.exhausted)))

	return nil
}

// selectRoundRobinToken 轮询选择策略
// 根据 batchSize 决定使用全局轮询还是活跃池轮询
func (tm *TokenManager) selectRoundRobinToken() *CachedToken {
	if tm.batchSize <= 0 {
		// 全局轮询模式（使用全部账号）
		return tm.selectRoundRobinAll()
	}
	// 活跃池轮询模式（维护固定数量的健康账号）
	return tm.selectRoundRobinWithPool()
}

// selectRandomToken 随机选择策略
func (tm *TokenManager) selectRandomToken() *CachedToken {
	if len(tm.configOrder) == 0 {
		return nil
	}

	// 收集所有可用的token
	var availableTokens []*CachedToken
	var availableKeys []string

	for _, key := range tm.configOrder {
		if cached, exists := tm.cache.tokens[key]; exists {
			if cached.IsUsable() {
				availableTokens = append(availableTokens, cached)
				availableKeys = append(availableKeys, key)
			}
		}
	}

	if len(availableTokens) == 0 {
		logger.Warn("所有token都不可用（随机策略）",
			logger.Int("total_count", len(tm.configOrder)))
		return nil
	}

	// 随机选择一个
	randomIndex := rand.Intn(len(availableTokens))
	selected := availableTokens[randomIndex]

	logger.Debug("随机策略选择token",
		logger.String("selected_key", availableKeys[randomIndex]),
		logger.Int("available_count_in_pool", len(availableTokens)),
		logger.Float64("available_count", selected.Available))

	return selected
}

// ============================================================================
// 活跃池管理方法（用于 RoundRobin + KIRO_BATCH_SIZE）
// ============================================================================

// isAccountHealthy 判断账号是否健康
// 健康标准：未禁用 + 未失效 + 有余额
func (tm *TokenManager) isAccountHealthy(index int) bool {
	return tm.isAccountHealthyWithRefresh(index, false)
}

// isAccountHealthyWithRefresh 检查账号是否健康，可选择是否强制刷新
// 健康标准：未禁用 + 未失效 + 有余额
// forceRefresh: 如果为 true，对于没有缓存的账号会尝试刷新以获取最新状态
func (tm *TokenManager) isAccountHealthyWithRefresh(index int, forceRefresh bool) bool {
	if index < 0 || index >= len(tm.configs) {
		return false
	}

	// 检查是否禁用
	if tm.configs[index].Disabled {
		return false
	}

	// 检查是否失效
	if _, isInvalid := tm.invalidated[index]; isInvalid {
		return false
	}

	// 检查缓存
	cacheKey := fmt.Sprintf(config.TokenCacheKeyFormat, index)
	if cached, exists := tm.cache.tokens[cacheKey]; exists {
		// 注意：不检查 token 是否过期，因为过期的 token 可以刷新
		// 只检查余额是否足够（> 阈值才可用，<= 阈值视为耗尽）
		return cached.Available > MinAvailableThreshold
	}

	// 如果没有缓存且需要强制刷新
	if forceRefresh {
		logger.Debug("账号无缓存，尝试刷新获取状态", logger.Int("index", index))

		// 刷新账号获取最新状态（注意：这里需要避免死锁，使用内部方法）
		cfg := tm.configs[index]
		token, err := tm.refreshSingleToken(cfg, index)
		if err != nil {
			logger.Debug("刷新账号失败", logger.Int("index", index), logger.Err(err))
			// 如果是 token 失效错误，标记为失效
			if types.IsTokenInvalidError(err) {
				tm.invalidated[index] = time.Now()
			}
			return false
		}

		// 清除失效标记
		delete(tm.invalidated, index)

		// 检查使用限制
		checker := tm.getUsageCheckerForToken(index)
		if usage, checkErr := checker.CheckUsageLimits(token); checkErr == nil {
			available := CalculateAvailableCount(usage)
			nextResetTime := GetNextResetTime(usage)

			// 更新缓存
			now := time.Now()
			tm.cache.tokens[cacheKey] = &CachedToken{
				Token:         token,
				UsageInfo:     usage,
				CachedAt:      now,
				LastCheckAt:   now,
				Available:     available,
				NextResetTime: nextResetTime,
			}

			logger.Debug("成功刷新账号状态",
				logger.Int("index", index),
				logger.Float64("available", available))

			// 返回健康状态（> 阈值才可用）
			return available > MinAvailableThreshold
		} else {
			logger.Debug("检查使用限制失败", logger.Int("index", index), logger.Err(checkErr))
			// 如果是 token 失效错误，标记为失效
			if types.IsTokenInvalidError(checkErr) {
				tm.invalidated[index] = time.Now()
			}
			return false
		}
	}

	// 如果没有缓存且不强制刷新，认为不健康
	return false
}

// buildActivePool 构建活跃池
// 从所有账号中选择最多 batchSize 个健康账号
func (tm *TokenManager) buildActivePool() {
	var healthyIndices []int

	// 第一轮：收集所有已缓存的健康账号
	for i := range tm.configs {
		if tm.isAccountHealthy(i) {
			healthyIndices = append(healthyIndices, i)
		}
	}

	// 如果已缓存的健康账号不足 batchSize，尝试刷新未缓存的账号
	if len(healthyIndices) < tm.batchSize {
		logger.Debug("已缓存健康账号不足，尝试刷新未缓存账号",
			logger.Int("cached_healthy", len(healthyIndices)),
			logger.Int("batch_size", tm.batchSize))

		// 记录已处理的账号，避免重复检查
		checkedIndices := make(map[int]bool)
		for _, idx := range healthyIndices {
			checkedIndices[idx] = true
		}

		// 第二轮：尝试刷新未缓存的账号
		for i := range tm.configs {
			// 跳过已检查的账号
			if checkedIndices[i] {
				continue
			}

			// 跳过禁用的账号
			if tm.configs[i].Disabled {
				continue
			}

			// 跳过已标记为失效的账号
			if _, isInvalid := tm.invalidated[i]; isInvalid {
				continue
			}

			// 检查是否有缓存，如果没有则尝试刷新
			cacheKey := fmt.Sprintf(config.TokenCacheKeyFormat, i)
			if _, exists := tm.cache.tokens[cacheKey]; !exists {
				// 尝试刷新获取状态
				if tm.isAccountHealthyWithRefresh(i, true) {
					healthyIndices = append(healthyIndices, i)
					logger.Debug("成功刷新并添加账号到活跃池",
						logger.Int("index", i))

					// 如果已达到 batchSize，停止搜索
					if len(healthyIndices) >= tm.batchSize {
						break
					}
				}
			}
		}
	}

	// 如果健康账号数 <= batchSize，使用全部健康账号
	if len(healthyIndices) <= tm.batchSize {
		tm.activePool = healthyIndices
	} else {
		// 否则只取前 batchSize 个
		tm.activePool = healthyIndices[:tm.batchSize]
	}

	// 重置轮询索引
	tm.poolRoundRobin = 0

	logger.Debug("构建活跃池",
		logger.Int("pool_size", len(tm.activePool)),
		logger.Int("batch_size", tm.batchSize),
		logger.Int("healthy_total", len(healthyIndices)))
}

// replaceUnhealthyAccount 替换不健康账号
// 从池外找一个健康账号补充，返回是否成功替换
func (tm *TokenManager) replaceUnhealthyAccount(unhealthyIndex int) bool {
	// 从 activePool 中移除不健康账号
	newPool := []int{}
	for _, idx := range tm.activePool {
		if idx != unhealthyIndex {
			newPool = append(newPool, idx)
		}
	}

	// 查找池外的健康账号
	inPool := make(map[int]bool)
	for _, idx := range tm.activePool {
		inPool[idx] = true
	}

	var replacement int = -1
	for i := range tm.configs {
		// 跳过已在池中的账号
		if inPool[i] {
			continue
		}
		// 找到第一个健康的账号（强制刷新以获取最新状态）
		if tm.isAccountHealthyWithRefresh(i, true) {
			replacement = i
			break
		}
	}

	// 如果找到替换账号，加入池中
	if replacement != -1 {
		newPool = append(newPool, replacement)
		tm.activePool = newPool

		logger.Info("替换不健康账号",
			logger.Int("removed_index", unhealthyIndex),
			logger.Int("added_index", replacement),
			logger.Int("pool_size", len(tm.activePool)))
		return true
	}

	// 没有找到替换账号，保留移除后的池
	tm.activePool = newPool

	logger.Warn("无法找到替换账号",
		logger.Int("removed_index", unhealthyIndex),
		logger.Int("remaining_pool_size", len(tm.activePool)))
	return false
}

// selectRoundRobinWithPool 活跃池轮询选择
func (tm *TokenManager) selectRoundRobinWithPool() *CachedToken {
	// 如果活跃池为空，构建活跃池
	if len(tm.activePool) == 0 {
		tm.buildActivePool()

		// 构建后仍为空，返回 nil
		if len(tm.activePool) == 0 {
			logger.Warn("无法构建活跃池，没有健康账号")
			return nil
		}
	}

	// 在活跃池内轮询查找可用 token
	poolSize := len(tm.activePool)
	startIndex := tm.poolRoundRobin % poolSize

	// 记录本轮发现的不健康账号，避免重复处理
	unhealthyAccounts := make(map[int]bool)

	for attempt := 0; attempt < poolSize; attempt++ {
		relativeIndex := (startIndex + attempt) % poolSize
		configIndex := tm.activePool[relativeIndex]
		currentKey := tm.configOrder[configIndex]

		// 检查账号是否健康
		if !tm.isAccountHealthy(configIndex) {
			// 记录不健康账号
			if !unhealthyAccounts[configIndex] {
				unhealthyAccounts[configIndex] = true
				// 获取详细的不健康原因
				reason := "未知"
				if tm.configs[configIndex].Disabled {
					reason = "账号已禁用"
				} else if _, isInvalid := tm.invalidated[configIndex]; isInvalid {
					reason = "账号已失效"
				} else if cached, exists := tm.cache.tokens[currentKey]; exists {
					// 不再检查过期时间，因为过期的 token 会在使用时刷新
					// 注意：先检查是否已耗尽（<=0），再检查是否快耗尽（< 阈值）
					if cached.Available <= 0 {
						reason = fmt.Sprintf("余额已耗尽 (当前: %.2f)", cached.Available)
					} else if cached.Available < MinAvailableThreshold {
						reason = fmt.Sprintf("余额不足 (当前: %.2f, 阈值: %.2f)", cached.Available, MinAvailableThreshold)
					}
				} else {
					reason = "缓存不存在"
				}
				logger.Info("活跃池中发现不健康账号",
					logger.Int("config_index", configIndex),
					logger.Int("pool_index", relativeIndex),
					logger.String("reason", reason))
			}
			// 继续下一次尝试，不立即替换
			continue
		}

		// 获取 token
		if cached, exists := tm.cache.tokens[currentKey]; exists {
			// 如果 token 过期，尝试刷新
			if time.Now().After(cached.Token.ExpiresAt) {
				logger.Debug("活跃池中token过期，尝试刷新",
					logger.Int("config_index", configIndex),
					logger.String("expired_at", cached.Token.ExpiresAt.Format(time.RFC3339)))

				// 刷新 token
				cfg := tm.configs[configIndex]
				if !cfg.Disabled {
					if token, err := tm.refreshSingleToken(cfg, configIndex); err == nil {
						// 清除失效标记
						delete(tm.invalidated, configIndex)

						// 检查使用限制
						checker := tm.getUsageCheckerForToken(configIndex)
						if usage, checkErr := checker.CheckUsageLimits(token); checkErr == nil {
							available := CalculateAvailableCount(usage)
							nextResetTime := GetNextResetTime(usage)

							// 更新缓存
							now := time.Now()
							tm.cache.tokens[currentKey] = &CachedToken{
								Token:         token,
								UsageInfo:     usage,
								CachedAt:      now,
								LastCheckAt:   now,
								Available:     available,
								NextResetTime: nextResetTime,
							}
							cached = tm.cache.tokens[currentKey]

							logger.Info("成功刷新过期token",
								logger.Int("config_index", configIndex),
								logger.Float64("available", available))
						} else {
							logger.Warn("刷新后检查使用限制失败",
								logger.Int("config_index", configIndex),
								logger.Err(checkErr))
							if types.IsTokenInvalidError(checkErr) {
								tm.invalidated[configIndex] = time.Now()
							}
							continue
						}
					} else {
						logger.Warn("刷新token失败",
							logger.Int("config_index", configIndex),
							logger.Err(err))
						if types.IsTokenInvalidError(err) {
							tm.invalidated[configIndex] = time.Now()
						}
						continue
					}
				}
			}

			// 检查 token 是否可用（余额 > 阈值才可用）
			if cached.Available > MinAvailableThreshold {
				// 移动到下一个位置
				tm.poolRoundRobin = (relativeIndex + 1) % poolSize

				logger.Debug("活跃池轮询选择token",
					logger.String("selected_key", currentKey),
					logger.Int("config_index", configIndex),
					logger.Int("pool_index", relativeIndex),
					logger.Int("pool_size", poolSize),
					logger.Float64("available_count", cached.Available))
				return cached
			}
		}
	}

	// 一轮结束后，批量处理不健康账号（只替换一个）
	if len(unhealthyAccounts) > 0 {
		// 只替换第一个不健康账号，避免过度刷新
		for unhealthyIndex := range unhealthyAccounts {
			logger.Info("尝试替换不健康账号",
				logger.Int("config_index", unhealthyIndex),
				logger.Int("unhealthy_count", len(unhealthyAccounts)))

			replaced := tm.replaceUnhealthyAccount(unhealthyIndex)
			if !replaced {
				// 如果找不到替换账号，只移除这一个不健康账号
				logger.Warn("无可用替换账号，仅移除不健康账号",
					logger.Int("removed_index", unhealthyIndex),
					logger.Int("remaining_pool_size", len(tm.activePool)))
			}
			// 只处理一个，避免连续刷新
			break
		}
	}

	// 池内所有账号都不可用，尝试重建活跃池
	logger.Info("活跃池内所有账号不可用，尝试重建活跃池")
	tm.buildActivePool()

	// 重建后再次尝试
	if len(tm.activePool) > 0 {
		configIndex := tm.activePool[0]
		currentKey := tm.configOrder[configIndex]
		if cached, exists := tm.cache.tokens[currentKey]; exists {
			if cached.IsUsable() {
				tm.poolRoundRobin = 1 % len(tm.activePool)
				return cached
			}
		}
	}

	logger.Warn("活跃池轮询失败，无可用token")
	return nil
}

// selectRoundRobinAll 全部账号轮询（原逻辑，batchSize=0 时使用）
func (tm *TokenManager) selectRoundRobinAll() *CachedToken {
	if len(tm.configOrder) == 0 {
		return nil
	}

	startIndex := tm.currentIndex
	for attempts := 0; attempts < len(tm.configOrder); attempts++ {
		currentKey := tm.configOrder[tm.currentIndex]
		configIndex := tm.currentIndex

		// 检查缓存是否存在且新鲜
		cached, exists := tm.cache.tokens[currentKey]
		needRefresh := false

		if !exists {
			// 缓存不存在，需要刷新
			needRefresh = true
			logger.Debug("账号缓存不存在，需要刷新",
				logger.String("key", currentKey),
				logger.Int("index", configIndex))
		} else if time.Since(cached.CachedAt) > config.TokenCacheTTL {
			// 缓存过期，需要刷新
			needRefresh = true
			logger.Debug("账号缓存过期，需要刷新",
				logger.String("key", currentKey),
				logger.Int("index", configIndex),
				logger.String("cached_at", cached.CachedAt.Format(time.RFC3339)))
		}

		// 如果需要刷新且账号未被禁用/失效
		if needRefresh && configIndex < len(tm.configs) {
			cfg := tm.configs[configIndex]
			if !cfg.Disabled {
				if _, isInvalid := tm.invalidated[configIndex]; !isInvalid {
					// 按需刷新单个账号
					logger.Debug("按需刷新单个账号",
						logger.Int("index", configIndex),
						logger.String("auth_type", cfg.AuthType))

					if token, err := tm.refreshSingleToken(cfg, configIndex); err == nil {
						// 清除失效标记
						delete(tm.invalidated, configIndex)

						// 检查使用限制
						checker := tm.getUsageCheckerForToken(configIndex)
						if usage, checkErr := checker.CheckUsageLimits(token); checkErr == nil {
							available := CalculateAvailableCount(usage)
							nextResetTime := GetNextResetTime(usage)

							// 更新缓存
							now := time.Now()
							cached = &CachedToken{
								Token:         token,
								UsageInfo:     usage,
								CachedAt:      now,
								LastCheckAt:   now,
								Available:     available,
								NextResetTime: nextResetTime,
							}
							tm.cache.tokens[currentKey] = cached
						} else if types.IsTokenInvalidError(checkErr) {
							tm.invalidated[configIndex] = time.Now()
						}
					} else if types.IsTokenInvalidError(err) {
						tm.invalidated[configIndex] = time.Now()
					}
				}
			}
		}

		// 再次检查缓存（刷新后的结果）
		if cached, exists := tm.cache.tokens[currentKey]; exists {
			if cached.IsUsable() {
				logger.Debug("全局轮询策略选择token",
					logger.String("selected_key", currentKey),
					logger.Int("index", tm.currentIndex),
					logger.Float64("available_count", cached.Available))

				// 轮询策略：每次使用后移动到下一个
				tm.currentIndex = (tm.currentIndex + 1) % len(tm.configOrder)
				return cached
			}
		}

		// 移动到下一个token
		tm.currentIndex = (tm.currentIndex + 1) % len(tm.configOrder)
	}

	// 恢复到起始索引
	tm.currentIndex = startIndex

	logger.Warn("所有token都不可用（全局轮询策略）",
		logger.Int("total_count", len(tm.configOrder)))

	return nil
}

// refreshCacheUnlocked 刷新token缓存
// 已弃用：此函数曾用于全局刷新所有token，现已改为按需刷新策略
// 保留此函数仅供参考，不应再被调用
// 内部方法：调用者必须持有 tm.mutex
// Deprecated: DO NOT USE
func (tm *TokenManager) refreshCacheUnlocked() error {
	logger.Debug("开始刷新token缓存")

	// 刷新所有非禁用且未失效的 token
	var refreshIndices []int
	for i, cfg := range tm.configs {
		// 跳过禁用的账号
		if cfg.Disabled {
			continue
		}
		// 跳过已失效的账号（避免重复刷新失败）
		if _, isInvalid := tm.invalidated[i]; isInvalid {
			continue
		}
		refreshIndices = append(refreshIndices, i)
	}

	logger.Debug("刷新所有有效token",
		logger.Int("refresh_count", len(refreshIndices)))

	// 刷新指定索引的 token
	for _, i := range refreshIndices {
		cfg := tm.configs[i]
		if cfg.Disabled {
			continue
		}

		// 优化：检查是否额度耗尽且未到重置日期
		cacheKey := fmt.Sprintf(config.TokenCacheKeyFormat, i)
		if cached, exists := tm.cache.tokens[cacheKey]; exists {
			// 如果额度耗尽（available <= 0）且重置日期未到（在未来）
			if cached.Available <= 0 && !cached.NextResetTime.IsZero() && time.Now().Before(cached.NextResetTime) {
				logger.Info("跳过刷新：额度耗尽且未到重置日期",
					logger.Int("config_index", i),
					logger.String("auth_type", cfg.AuthType),
					logger.Float64("available", cached.Available),
					logger.String("next_reset", cached.NextResetTime.Format(time.RFC3339)),
					logger.String("time_until_reset", time.Until(cached.NextResetTime).Round(time.Hour).String()))
				continue
			}
		}

		// 刷新token
		token, err := tm.refreshSingleToken(cfg, i)
		if err != nil {
			// 刷新失败时，更新LastCheckAt但保留旧的缓存状态
			if cached, exists := tm.cache.tokens[cacheKey]; exists {
				cached.LastCheckAt = time.Now()
				logger.Debug("刷新失败但保留旧缓存",
					logger.Int("config_index", i),
					logger.String("auth_type", cfg.AuthType),
					logger.Float64("cached_available", cached.Available),
					logger.String("cached_at", cached.CachedAt.Format(time.RFC3339)))
			}

			// 检查是否是 token 失效错误
			if types.IsTokenInvalidError(err) {
				logger.Warn("检测到token失效",
					logger.Int("config_index", i),
					logger.String("auth_type", cfg.AuthType),
					logger.Err(err))
				// 记录失效时间
				tm.invalidated[i] = time.Now()
			} else {
				logger.Warn("刷新单个token失败",
					logger.Int("config_index", i),
					logger.String("auth_type", cfg.AuthType),
					logger.Err(err))
			}
			continue
		}

		// 如果刷新成功，清除失效标记
		delete(tm.invalidated, i)

		// 检查使用限制
		var usageInfo *types.UsageLimits
		var available float64
		var nextResetTime time.Time

		checker := tm.getUsageCheckerForToken(i)
		if usage, checkErr := checker.CheckUsageLimits(token); checkErr == nil {
			usageInfo = usage
			available = CalculateAvailableCount(usage)
			nextResetTime = GetNextResetTime(usage)
		} else {
			// 检查是否是 token 失效错误
			if types.IsTokenInvalidError(checkErr) {
				logger.Warn("使用限制检查检测到token失效",
					logger.Int("config_index", i),
					logger.String("auth_type", cfg.AuthType),
					logger.Err(checkErr))
				// 记录失效时间
				tm.invalidated[i] = time.Now()
			} else {
				logger.Warn("检查使用限制失败", logger.Err(checkErr))
			}
		}

		// 更新缓存（直接访问，已在tm.mutex保护下）
		cacheKey = fmt.Sprintf(config.TokenCacheKeyFormat, i)
		now := time.Now()
		tm.cache.tokens[cacheKey] = &CachedToken{
			Token:         token,
			UsageInfo:     usageInfo,
			CachedAt:      now, // 成功刷新时间
			LastCheckAt:   now, // 最后检查时间
			Available:     available,
			NextResetTime: nextResetTime,
		}

		logger.Debug("token缓存更新",
			logger.String("cache_key", cacheKey),
			logger.Float64("available", available),
			logger.String("next_reset", nextResetTime.Format(time.RFC3339)))
	}

	// 自动删除失效的账号（如果启用）
	if tm.autoRemoveInvalid && len(tm.invalidated) > 0 {
		removed, err := tm.removeInvalidTokensUnlocked()
		if err != nil {
			logger.Warn("自动删除失效账号失败", logger.Err(err))
		} else if removed > 0 {
			logger.Info("自动删除失效账号成功",
				logger.Int("removed_count", removed),
				logger.Int("remaining_count", len(tm.configs)))
		}
	}

	tm.lastRefresh = time.Now()
	return nil
}

// IsUsable 检查缓存的token是否可用
func (ct *CachedToken) IsUsable() bool {
	// 检查token是否过期
	if time.Now().After(ct.Token.ExpiresAt) {
		return false
	}

	// 检查可用次数是否大于阈值（> 阈值才可用，<= 阈值视为耗尽）
	return ct.Available > MinAvailableThreshold
}

// *** 已删除 set 和 updateLastUsed 方法 ***
// SimpleTokenCache 现在是纯数据结构，所有访问由 TokenManager.mutex 保护
// set 操作：直接通过 tm.cache.tokens[key] = value 完成
// updateLastUsed 操作：已合并到 getBestToken 方法中

// CalculateAvailableCount 计算可用次数 (基于CREDIT资源类型，返回浮点精度)
func CalculateAvailableCount(usage *types.UsageLimits) float64 {
	for _, breakdown := range usage.UsageBreakdownList {
		if breakdown.ResourceType == "CREDIT" {
			var totalAvailable float64

			// 优先使用免费试用额度 (如果存在且处于ACTIVE状态)
			if breakdown.FreeTrialInfo != nil && breakdown.FreeTrialInfo.FreeTrialStatus == "ACTIVE" {
				freeTrialAvailable := breakdown.FreeTrialInfo.UsageLimitWithPrecision - breakdown.FreeTrialInfo.CurrentUsageWithPrecision
				totalAvailable += freeTrialAvailable
			}

			// 加上基础额度
			baseAvailable := breakdown.UsageLimitWithPrecision - breakdown.CurrentUsageWithPrecision
			totalAvailable += baseAvailable

			if totalAvailable < 0 {
				return 0.0
			}
			return totalAvailable
		}
	}
	return 0.0
}

// GetNextResetTime 获取下次重置时间
// Kiro 额度固定在每月1日 08:00 (UTC+8) 重置
func GetNextResetTime(usage *types.UsageLimits) time.Time {
	now := time.Now()
	currentYear := now.Year()
	currentMonth := now.Month()
	currentDay := now.Day()

	// 计算下次重置时间（每月1日 08:00）
	var resetTime time.Time
	if currentDay == 1 && now.Hour() < 8 {
		// 如果是1号且还没到8点，重置时间是今天8点
		resetTime = time.Date(currentYear, currentMonth, 1, 8, 0, 0, 0, time.Local)
	} else {
		// 否则重置时间是下个月1日8点
		nextMonth := currentMonth + 1
		nextYear := currentYear
		if nextMonth > 12 {
			nextMonth = 1
			nextYear++
		}
		resetTime = time.Date(nextYear, nextMonth, 1, 8, 0, 0, 0, time.Local)
	}

	return resetTime
}

// getUsageCheckerForToken 获取指定token索引的usage checker（带代理支持）
// 内部方法：调用者必须确保索引有效
func (tm *TokenManager) getUsageCheckerForToken(tokenIndex int) *UsageLimitsChecker {
	// 如果没有代理池，使用默认客户端
	if tm.proxyPool == nil {
		return NewUsageLimitsChecker()
	}

	// 获取代理客户端
	tokenIndexStr := fmt.Sprintf("%d", tokenIndex)
	_, client, err := tm.proxyPool.GetProxyForToken(tokenIndexStr)
	if err != nil {
		logger.Warn("获取代理失败，usage checker使用默认客户端",
			logger.String("token_index", tokenIndexStr),
			logger.Err(err))
		return NewUsageLimitsChecker()
	}

	return NewUsageLimitsChecker(client)
}

// generateConfigOrder 生成token配置的顺序
func generateConfigOrder(configs []AuthConfig) []string {
	var order []string

	for i := range configs {
		// 使用索引生成cache key，与refreshCache中的逻辑保持一致
		cacheKey := fmt.Sprintf(config.TokenCacheKeyFormat, i)
		order = append(order, cacheKey)
	}

	logger.Debug("生成配置顺序",
		logger.Int("config_count", len(configs)),
		logger.Any("order", order))

	return order
}

// TokenStatus token 状态信息
type TokenStatus struct {
	Index          int                `json:"index"`
	AuthType       string             `json:"auth_type"`
	RefreshToken   string             `json:"refresh_token_preview"` // 只显示前后各4位
	Disabled       bool               `json:"disabled"`
	IsInvalid      bool               `json:"is_invalid"`
	InvalidatedAt  *time.Time         `json:"invalidated_at,omitempty"`
	RefreshStatus  string             `json:"refresh_status"` // not_refreshed: 未刷新, active: 正常, invalid: 失效
	Available      float64            `json:"available"`
	UsageInfo      *types.UsageLimits `json:"usage_info,omitempty"`
	LastUsed       *time.Time         `json:"last_used,omitempty"`
	NextResetDate  *time.Time         `json:"next_reset_date,omitempty"`
	DaysUntilReset int                `json:"days_until_reset"`
}

// GetAllTokensStatus 获取所有 token 的状态
func (tm *TokenManager) GetAllTokensStatus() []TokenStatus {
	tm.mutex.RLock()
	defer tm.mutex.RUnlock()

	statuses := make([]TokenStatus, 0, len(tm.configs))

	for i, cfg := range tm.configs {
		status := TokenStatus{
			Index:        i,
			AuthType:     cfg.AuthType,
			RefreshToken: maskToken(cfg.RefreshToken),
			Disabled:     cfg.Disabled,
		}

		// 检查是否失效
		if invalidTime, exists := tm.invalidated[i]; exists {
			status.IsInvalid = true
			status.InvalidatedAt = &invalidTime
		}

		// 获取缓存信息
		cacheKey := fmt.Sprintf(config.TokenCacheKeyFormat, i)
		if cached, exists := tm.cache.tokens[cacheKey]; exists {
			// 已刷新过的账号
			if status.IsInvalid {
				status.RefreshStatus = "invalid"
			} else {
				status.RefreshStatus = "active"
			}
			status.Available = cached.Available
			status.UsageInfo = cached.UsageInfo
			if !cached.LastUsed.IsZero() {
				status.LastUsed = &cached.LastUsed
			}
			// 添加重置日期信息
			if !cached.NextResetTime.IsZero() {
				status.NextResetDate = &cached.NextResetTime
				// 计算距离重置的天数
				daysUntil := int(time.Until(cached.NextResetTime).Hours() / 24)
				if daysUntil < 0 {
					daysUntil = 0 // 已过期则显示 0
				}
				status.DaysUntilReset = daysUntil
			}
		} else {
			// 缓存中没有记录，说明从未刷新过
			status.RefreshStatus = "not_refreshed"
		}

		statuses = append(statuses, status)
	}

	return statuses
}

// GetTokenStatus 获取单个 token 的状态
func (tm *TokenManager) GetTokenStatus(index int) (*TokenStatus, error) {
	tm.mutex.RLock()
	defer tm.mutex.RUnlock()

	if index < 0 || index >= len(tm.configs) {
		return nil, fmt.Errorf("索引超出范围: %d", index)
	}

	cfg := tm.configs[index]
	status := &TokenStatus{
		Index:        index,
		AuthType:     cfg.AuthType,
		RefreshToken: maskToken(cfg.RefreshToken),
		Disabled:     cfg.Disabled,
	}

	// 检查是否失效
	if invalidTime, exists := tm.invalidated[index]; exists {
		status.IsInvalid = true
		status.InvalidatedAt = &invalidTime
	}

	// 获取缓存信息
	cacheKey := fmt.Sprintf(config.TokenCacheKeyFormat, index)
	if cached, exists := tm.cache.tokens[cacheKey]; exists {
		// 已刷新过的账号
		if status.IsInvalid {
			status.RefreshStatus = "invalid"
		} else {
			status.RefreshStatus = "active"
		}
		status.Available = cached.Available
		status.UsageInfo = cached.UsageInfo
		if !cached.LastUsed.IsZero() {
			status.LastUsed = &cached.LastUsed
		}
		// 添加重置日期信息
		if !cached.NextResetTime.IsZero() {
			status.NextResetDate = &cached.NextResetTime
			// 计算距离重置的天数
			daysUntil := int(time.Until(cached.NextResetTime).Hours() / 24)
			if daysUntil < 0 {
				daysUntil = 0 // 已过期则显示 0
			}
			status.DaysUntilReset = daysUntil
		}
	} else {
		// 缓存中没有记录，说明从未刷新过
		status.RefreshStatus = "not_refreshed"
	}

	return status, nil
}

// RemoveToken 删除单个 token（仅失效的可删除）
func (tm *TokenManager) RemoveToken(index int) error {
	tm.mutex.Lock()
	defer tm.mutex.Unlock()

	if index < 0 || index >= len(tm.configs) {
		return fmt.Errorf("索引超出范围: %d", index)
	}

	// 检查是否失效
	if _, exists := tm.invalidated[index]; !exists {
		return fmt.Errorf("只能删除失效的 token，索引 %d 的 token 未失效", index)
	}

	// 从配置中移除
	tm.configs = append(tm.configs[:index], tm.configs[index+1:]...)

	// 更新失效记录（索引需要调整）
	newInvalidated := make(map[int]time.Time)
	for i, t := range tm.invalidated {
		if i < index {
			newInvalidated[i] = t
		} else if i > index {
			newInvalidated[i-1] = t
		}
		// i == index 的被删除，不添加
	}
	tm.invalidated = newInvalidated

	// 重新生成配置顺序
	tm.configOrder = generateConfigOrder(tm.configs)

	// 清理缓存中对应的 token
	cacheKey := fmt.Sprintf(config.TokenCacheKeyFormat, index)
	delete(tm.cache.tokens, cacheKey)

	logger.Info("删除失效token",
		logger.Int("index", index),
		logger.Int("remaining_count", len(tm.configs)))

	// 同步配置文件（即使未显式指定文件路径，也尽量落到默认 tokens.json）
	configPath := tm.configPath
	if configPath == "" {
		configPath = DefaultConfigPath
	}
	if err := SaveConfigToFile(tm.configs, configPath); err != nil {
		logger.Warn("同步配置文件失败", logger.Err(err))
		return fmt.Errorf("%w: %v", ErrConfigPersistence, err)
	}

	return nil
}

// removeInvalidTokensUnlocked 批量删除所有失效的 token（内部方法，调用者必须持有锁）
func (tm *TokenManager) removeInvalidTokensUnlocked() (int, error) {
	if len(tm.invalidated) == 0 {
		return 0, nil
	}

	// 收集需要删除的索引（从大到小排序，避免删除时索引错乱）
	indices := make([]int, 0, len(tm.invalidated))
	for i := range tm.invalidated {
		indices = append(indices, i)
	}

	// 排序（降序）
	for i := 0; i < len(indices); i++ {
		for j := i + 1; j < len(indices); j++ {
			if indices[i] < indices[j] {
				indices[i], indices[j] = indices[j], indices[i]
			}
		}
	}

	// 从后往前删除
	removedCount := 0
	for _, index := range indices {
		if index >= 0 && index < len(tm.configs) {
			tm.configs = append(tm.configs[:index], tm.configs[index+1:]...)
			removedCount++

			// 清理缓存
			cacheKey := fmt.Sprintf(config.TokenCacheKeyFormat, index)
			delete(tm.cache.tokens, cacheKey)
		}
	}

	// 清空失效记录
	tm.invalidated = make(map[int]time.Time)

	// 重新生成配置顺序
	tm.configOrder = generateConfigOrder(tm.configs)

	logger.Info("批量删除失效token（内部）",
		logger.Int("removed_count", removedCount),
		logger.Int("remaining_count", len(tm.configs)))

	// 同步配置文件（即使未显式指定文件路径，也尽量落到默认 tokens.json）
	configPath := tm.configPath
	if configPath == "" {
		configPath = DefaultConfigPath
	}
	if err := SaveConfigToFile(tm.configs, configPath); err != nil {
		logger.Warn("同步配置文件失败", logger.Err(err))
		return removedCount, fmt.Errorf("%w: %v", ErrConfigPersistence, err)
	}

	return removedCount, nil
}

// RemoveInvalidTokens 批量删除所有失效的 token（公开方法，带锁）
func (tm *TokenManager) RemoveInvalidTokens() (int, error) {
	tm.mutex.Lock()
	defer tm.mutex.Unlock()

	return tm.removeInvalidTokensUnlocked()
}

// ExportTokens 导出 token 配置（支持单个或全部）
func (tm *TokenManager) ExportTokens(indices []int) ([]AuthConfig, error) {
	tm.mutex.RLock()
	defer tm.mutex.RUnlock()

	// 如果 indices 为空，导出全部
	if len(indices) == 0 {
		exported := make([]AuthConfig, len(tm.configs))
		copy(exported, tm.configs)
		return exported, nil
	}

	// 导出指定索引的配置
	exported := make([]AuthConfig, 0, len(indices))
	for _, index := range indices {
		if index < 0 || index >= len(tm.configs) {
			return nil, fmt.Errorf("索引超出范围: %d", index)
		}
		exported = append(exported, tm.configs[index])
	}

	return exported, nil
}

// maskToken 遮蔽 token，只显示前后各4位
func maskToken(token string) string {
	if len(token) <= 8 {
		return "****"
	}
	return token[:4] + "****" + token[len(token)-4:]
}

// SyncConfigFile 同步配置到文件（如果使用文件配置）
func (tm *TokenManager) SyncConfigFile() error {
	if tm.configPath == "" {
		return fmt.Errorf("未使用文件配置，无需同步")
	}

	tm.mutex.RLock()
	configs := make([]AuthConfig, len(tm.configs))
	copy(configs, tm.configs)
	tm.mutex.RUnlock()

	return SaveConfigToFile(configs, tm.configPath)
}

// InitializeBatchTokens 在启动时初始化首批 token
// 如果设置了 batchSize，初始化 batchSize 数量的账号
// 否则只初始化第一个账号
func (tm *TokenManager) InitializeBatchTokens() error {
	tm.mutex.Lock()
	defer tm.mutex.Unlock()

	logger.Info("开始初始化首批token")

	// 确定需要初始化的账号数量
	var initCount int
	if tm.batchSize > 0 {
		// 使用活跃池模式：刷新 batchSize 数量的账号
		initCount = tm.batchSize
		if initCount > len(tm.configs) {
			initCount = len(tm.configs)
		}
		logger.Info("使用活跃池模式",
			logger.Int("batch_size", tm.batchSize),
			logger.Int("init_count", initCount),
			logger.Int("total_configs", len(tm.configs)))
	} else {
		// 常规模式：只刷新第一个账号
		initCount = 1
		logger.Info("使用常规模式，只刷新第一个账号")
	}

	// 刷新指定数量的账号（确保跳过失效/禁用账号，直到找到足够的有效账号）
	successCount := 0
	for i := 0; i < len(tm.configs) && successCount < initCount; i++ {
		cfg := tm.configs[i]
		if cfg.Disabled {
			logger.Debug("跳过禁用的账号",
				logger.Int("index", i))
			continue
		}

		// 刷新 token
		token, err := tm.refreshSingleToken(cfg, i)
		if err != nil {
			// 检查是否是 token 失效错误
			if types.IsTokenInvalidError(err) {
				logger.Warn("初始化时检测到token失效",
					logger.Int("index", i),
					logger.String("auth_type", cfg.AuthType),
					logger.Err(err))
				// 记录失效时间
				tm.invalidated[i] = time.Now()
			} else {
				logger.Warn("初始化刷新token失败",
					logger.Int("index", i),
					logger.String("auth_type", cfg.AuthType),
					logger.Err(err))
			}
			continue
		}

		// 检查使用限制
		var usageInfo *types.UsageLimits
		var available float64
		var nextResetTime time.Time

		checker := tm.getUsageCheckerForToken(i)
		if usage, checkErr := checker.CheckUsageLimits(token); checkErr == nil {
			usageInfo = usage
			available = CalculateAvailableCount(usage)
			nextResetTime = GetNextResetTime(usage)
		} else {
			// 检查是否是 token 失效错误
			if types.IsTokenInvalidError(checkErr) {
				logger.Warn("初始化时使用限制检查检测到token失效",
					logger.Int("index", i),
					logger.String("auth_type", cfg.AuthType),
					logger.Err(checkErr))
				// 记录失效时间
				tm.invalidated[i] = time.Now()
			} else {
				logger.Warn("初始化时检查使用限制失败",
					logger.Int("index", i),
					logger.Err(checkErr))
			}
		}

		// 更新缓存
		cacheKey := fmt.Sprintf(config.TokenCacheKeyFormat, i)
		now := time.Now()
		tm.cache.tokens[cacheKey] = &CachedToken{
			Token:         token,
			UsageInfo:     usageInfo,
			CachedAt:      now,
			LastCheckAt:   now,
			Available:     available,
			NextResetTime: nextResetTime,
		}

		// 只有健康账号（available > 阈值）才计入成功数并加入活跃池
		if available > MinAvailableThreshold {
			successCount++
			// 活跃池模式：将健康账号加入活跃池
			if tm.batchSize > 0 {
				tm.activePool = append(tm.activePool, i)
			}
			logger.Info("成功初始化健康token",
				logger.Int("index", i),
				logger.String("auth_type", cfg.AuthType),
				logger.Float64("available", available),
				logger.String("next_reset", nextResetTime.Format(time.RFC3339)))
		} else {
			logger.Warn("初始化token但余额为0，继续刷新更多账号",
				logger.Int("index", i),
				logger.String("auth_type", cfg.AuthType),
				logger.Float64("available", available),
				logger.String("next_reset", nextResetTime.Format(time.RFC3339)))
		}
	}

	// 更新最后刷新时间
	tm.lastRefresh = time.Now()

	logger.Info("首批token初始化完成",
		logger.Int("success_count", successCount),
		logger.Int("desired_count", initCount),
		logger.Int("active_pool_size", len(tm.activePool)),
		logger.Int("invalid_count", len(tm.invalidated)),
		logger.Int("total_configs", len(tm.configs)))

	if successCount == 0 {
		return fmt.Errorf("没有成功初始化任何健康token（余额>0）")
	}

	return nil
}

// verifyLowBalanceToken 验证低余额账号的真实余额
// 当本地计算的余额降到接近0时，异步调用此方法刷新获取真实余额
func (tm *TokenManager) verifyLowBalanceToken(index int) {
	logger.Info("触发低余额账号验证",
		logger.Int("index", index))

	tm.mutex.Lock()
	if index < 0 || index >= len(tm.configs) {
		tm.mutex.Unlock()
		return
	}
	cfg := tm.configs[index]
	if cfg.Disabled {
		tm.mutex.Unlock()
		return
	}
	tm.mutex.Unlock()

	// 刷新 token 获取真实余额
	token, err := tm.refreshSingleToken(cfg, index)
	if err != nil {
		logger.Warn("低余额验证：刷新token失败",
			logger.Int("index", index),
			logger.Err(err))
		if types.IsTokenInvalidError(err) {
			tm.mutex.Lock()
			tm.invalidated[index] = time.Now()
			tm.mutex.Unlock()
		}
		return
	}

	// 检查使用限制获取真实余额
	checker := tm.getUsageCheckerForToken(index)
	usage, checkErr := checker.CheckUsageLimits(token)
	if checkErr != nil {
		logger.Warn("低余额验证：检查使用限制失败",
			logger.Int("index", index),
			logger.Err(checkErr))
		if types.IsTokenInvalidError(checkErr) {
			tm.mutex.Lock()
			tm.invalidated[index] = time.Now()
			tm.mutex.Unlock()
		}
		return
	}

	available := CalculateAvailableCount(usage)
	nextResetTime := GetNextResetTime(usage)

	// 更新缓存
	tm.mutex.Lock()
	cacheKey := fmt.Sprintf(config.TokenCacheKeyFormat, index)
	now := time.Now()
	tm.cache.tokens[cacheKey] = &CachedToken{
		Token:         token,
		UsageInfo:     usage,
		CachedAt:      now,
		LastCheckAt:   now,
		Available:     available,
		NextResetTime: nextResetTime,
	}

	// 清除失效标记
	delete(tm.invalidated, index)
	tm.mutex.Unlock()

	if available < MinAvailableThreshold {
		logger.Warn("低余额验证：账号余额已耗尽",
			logger.Int("index", index),
			logger.Float64("available", available),
			logger.Float64("threshold", MinAvailableThreshold))
	} else {
		logger.Info("低余额验证：账号仍有余额",
			logger.Int("index", index),
			logger.Float64("available", available))
	}
}

// RefreshResult 单个账号刷新结果
type RefreshResult struct {
	Index   int          `json:"index"`
	Success bool         `json:"success"`
	Error   string       `json:"error,omitempty"`
	Status  *TokenStatus `json:"status,omitempty"`
}

// RefreshToken 刷新单个账号的状态（公开方法）
// POST /v1/admin/tokens/:index/refresh
func (tm *TokenManager) RefreshToken(index int) (*RefreshResult, error) {
	tm.mutex.Lock()
	defer tm.mutex.Unlock()

	if index < 0 || index >= len(tm.configs) {
		return nil, fmt.Errorf("索引超出范围: %d", index)
	}

	cfg := tm.configs[index]
	if cfg.Disabled {
		return &RefreshResult{
			Index:   index,
			Success: false,
			Error:   "账号已禁用",
		}, nil
	}

	// 刷新 token
	token, err := tm.refreshSingleToken(cfg, index)
	if err != nil {
		// 检查是否是 token 失效错误
		if types.IsTokenInvalidError(err) {
			logger.Warn("刷新时检测到token失效",
				logger.Int("index", index),
				logger.String("auth_type", cfg.AuthType),
				logger.Err(err))
			// 记录失效时间
			tm.invalidated[index] = time.Now()
		}

		return &RefreshResult{
			Index:   index,
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	// 如果刷新成功，清除失效标记
	delete(tm.invalidated, index)

	// 检查使用限制
	var usageInfo *types.UsageLimits
	var available float64
	var nextResetTime time.Time

	checker := tm.getUsageCheckerForToken(index)
	if usage, checkErr := checker.CheckUsageLimits(token); checkErr == nil {
		usageInfo = usage
		available = CalculateAvailableCount(usage)
		nextResetTime = GetNextResetTime(usage)
	} else {
		// 检查是否是 token 失效错误
		if types.IsTokenInvalidError(checkErr) {
			logger.Warn("使用限制检查检测到token失效",
				logger.Int("index", index),
				logger.String("auth_type", cfg.AuthType),
				logger.Err(checkErr))
			// 记录失效时间
			tm.invalidated[index] = time.Now()

			return &RefreshResult{
				Index:   index,
				Success: false,
				Error:   checkErr.Error(),
			}, nil
		}
		logger.Warn("检查使用限制失败", logger.Err(checkErr))
	}

	// 更新缓存
	cacheKey := fmt.Sprintf(config.TokenCacheKeyFormat, index)
	now := time.Now()
	tm.cache.tokens[cacheKey] = &CachedToken{
		Token:         token,
		UsageInfo:     usageInfo,
		CachedAt:      now,
		LastCheckAt:   now,
		Available:     available,
		NextResetTime: nextResetTime,
	}

	logger.Info("成功刷新单个账号",
		logger.Int("index", index),
		logger.String("auth_type", cfg.AuthType),
		logger.Float64("available", available))

	// 获取更新后的状态
	status, _ := tm.getTokenStatusUnlocked(index)

	return &RefreshResult{
		Index:   index,
		Success: true,
		Status:  status,
	}, nil
}

// RefreshTokens 批量刷新账号状态（公开方法）
// POST /v1/admin/tokens/refresh
func (tm *TokenManager) RefreshTokens(indices []int) ([]RefreshResult, error) {
	tm.mutex.Lock()
	defer tm.mutex.Unlock()

	// 如果 indices 为空，刷新所有非禁用的账号
	if len(indices) == 0 {
		indices = make([]int, 0, len(tm.configs))
		for i, cfg := range tm.configs {
			if !cfg.Disabled {
				indices = append(indices, i)
			}
		}
	}

	results := make([]RefreshResult, 0, len(indices))

	for _, index := range indices {
		if index < 0 || index >= len(tm.configs) {
			results = append(results, RefreshResult{
				Index:   index,
				Success: false,
				Error:   fmt.Sprintf("索引超出范围: %d", index),
			})
			continue
		}

		cfg := tm.configs[index]
		if cfg.Disabled {
			results = append(results, RefreshResult{
				Index:   index,
				Success: false,
				Error:   "账号已禁用",
			})
			continue
		}

		// 刷新 token
		token, err := tm.refreshSingleToken(cfg, index)
		if err != nil {
			// 检查是否是 token 失效错误
			if types.IsTokenInvalidError(err) {
				logger.Warn("刷新时检测到token失效",
					logger.Int("index", index),
					logger.String("auth_type", cfg.AuthType),
					logger.Err(err))
				// 记录失效时间
				tm.invalidated[index] = time.Now()
			}

			results = append(results, RefreshResult{
				Index:   index,
				Success: false,
				Error:   err.Error(),
			})
			continue
		}

		// 如果刷新成功，清除失效标记
		delete(tm.invalidated, index)

		// 检查使用限制
		var usageInfo *types.UsageLimits
		var available float64
		var nextResetTime time.Time

		checker := tm.getUsageCheckerForToken(index)
		if usage, checkErr := checker.CheckUsageLimits(token); checkErr == nil {
			usageInfo = usage
			available = CalculateAvailableCount(usage)
			nextResetTime = GetNextResetTime(usage)
		} else {
			// 检查是否是 token 失效错误
			if types.IsTokenInvalidError(checkErr) {
				logger.Warn("使用限制检查检测到token失效",
					logger.Int("index", index),
					logger.String("auth_type", cfg.AuthType),
					logger.Err(checkErr))
				// 记录失效时间
				tm.invalidated[index] = time.Now()

				results = append(results, RefreshResult{
					Index:   index,
					Success: false,
					Error:   checkErr.Error(),
				})
				continue
			}
			logger.Warn("检查使用限制失败", logger.Err(checkErr))
		}

		// 更新缓存
		cacheKey := fmt.Sprintf(config.TokenCacheKeyFormat, index)
		now := time.Now()
		tm.cache.tokens[cacheKey] = &CachedToken{
			Token:         token,
			UsageInfo:     usageInfo,
			CachedAt:      now,
			LastCheckAt:   now,
			Available:     available,
			NextResetTime: nextResetTime,
		}

		logger.Info("成功刷新账号",
			logger.Int("index", index),
			logger.String("auth_type", cfg.AuthType),
			logger.Float64("available", available))

		// 获取更新后的状态
		status, _ := tm.getTokenStatusUnlocked(index)

		results = append(results, RefreshResult{
			Index:   index,
			Success: true,
			Status:  status,
		})
	}

	successCount := 0
	for _, r := range results {
		if r.Success {
			successCount++
		}
	}

	logger.Info("批量刷新完成",
		logger.Int("total", len(results)),
		logger.Int("success", successCount),
		logger.Int("failed", len(results)-successCount))

	return results, nil
}

// getTokenStatusUnlocked 获取单个 token 的状态（内部方法，调用者必须持有锁）
func (tm *TokenManager) getTokenStatusUnlocked(index int) (*TokenStatus, error) {
	if index < 0 || index >= len(tm.configs) {
		return nil, fmt.Errorf("索引超出范围: %d", index)
	}

	cfg := tm.configs[index]
	status := &TokenStatus{
		Index:        index,
		AuthType:     cfg.AuthType,
		RefreshToken: maskToken(cfg.RefreshToken),
		Disabled:     cfg.Disabled,
	}

	// 检查是否失效
	if invalidTime, exists := tm.invalidated[index]; exists {
		status.IsInvalid = true
		status.InvalidatedAt = &invalidTime
	}

	// 获取缓存信息
	cacheKey := fmt.Sprintf(config.TokenCacheKeyFormat, index)
	if cached, exists := tm.cache.tokens[cacheKey]; exists {
		// 已刷新过的账号
		if status.IsInvalid {
			status.RefreshStatus = "invalid"
		} else {
			status.RefreshStatus = "active"
		}
		status.Available = cached.Available
		status.UsageInfo = cached.UsageInfo
		if !cached.LastUsed.IsZero() {
			status.LastUsed = &cached.LastUsed
		}
		// 添加重置日期信息
		if !cached.NextResetTime.IsZero() {
			status.NextResetDate = &cached.NextResetTime
			// 计算距离重置的天数
			daysUntil := int(time.Until(cached.NextResetTime).Hours() / 24)
			if daysUntil < 0 {
				daysUntil = 0 // 已过期则显示 0
			}
			status.DaysUntilReset = daysUntil
		}
	} else {
		// 缓存中没有记录，说明从未刷新过
		status.RefreshStatus = "not_refreshed"
	}

	return status, nil
}

// startPeriodicRefresh 定时刷新活跃池中的 token
func (tm *TokenManager) startPeriodicRefresh() {
	for {
		select {
		case <-tm.refreshTicker.C:
			tm.refreshActivePoolTokens()
		case <-tm.refreshStop:
			logger.Info("停止活跃池定时刷新任务")
			return
		}
	}
}

// refreshActivePoolTokens 刷新活跃池中快要过期的 token
func (tm *TokenManager) refreshActivePoolTokens() {
	tm.mutex.Lock()
	defer tm.mutex.Unlock()

	if len(tm.activePool) == 0 {
		return
	}

	refreshCount := 0
	threshold := 10 * time.Minute // 提前10分钟刷新

	for _, configIndex := range tm.activePool {
		if configIndex >= len(tm.configs) {
			continue
		}

		currentKey := tm.configOrder[configIndex]
		cached, exists := tm.cache.tokens[currentKey]
		if !exists {
			continue
		}

		// 检查是否需要刷新（快要过期或已过期）
		timeUntilExpiry := time.Until(cached.Token.ExpiresAt)
		if timeUntilExpiry < threshold {
			cfg := tm.configs[configIndex]
			if cfg.Disabled {
				continue
			}

			logger.Debug("定时刷新即将过期的token",
				logger.Int("config_index", configIndex),
				logger.Duration("time_until_expiry", timeUntilExpiry))

			// 刷新 token
			if token, err := tm.refreshSingleToken(cfg, configIndex); err == nil {
				// 清除失效标记
				delete(tm.invalidated, configIndex)

				// 检查使用限制
				checker := tm.getUsageCheckerForToken(configIndex)
				if usage, checkErr := checker.CheckUsageLimits(token); checkErr == nil {
					available := CalculateAvailableCount(usage)
					nextResetTime := GetNextResetTime(usage)

					// 更新缓存
					now := time.Now()
					tm.cache.tokens[currentKey] = &CachedToken{
						Token:         token,
						UsageInfo:     usage,
						CachedAt:      now,
						LastCheckAt:   now,
						Available:     available,
						NextResetTime: nextResetTime,
					}

					refreshCount++
					logger.Info("定时刷新成功",
						logger.Int("config_index", configIndex),
						logger.Float64("available", available))
				} else {
					logger.Warn("定时刷新后检查使用限制失败",
						logger.Int("config_index", configIndex),
						logger.Err(checkErr))
				}
			} else {
				logger.Warn("定时刷新失败",
					logger.Int("config_index", configIndex),
					logger.Err(err))
			}
		}
	}

	if refreshCount > 0 {
		logger.Info("活跃池定时刷新完成",
			logger.Int("refreshed_count", refreshCount),
			logger.Int("pool_size", len(tm.activePool)))
	}
}

// Close 关闭 TokenManager，停止定时任务
func (tm *TokenManager) Close() {
	tm.mutex.Lock()
	defer tm.mutex.Unlock()

	// 停止定时刷新任务
	if tm.refreshTicker != nil {
		tm.refreshTicker.Stop()
		close(tm.refreshStop)
		tm.refreshTicker = nil
		logger.Info("TokenManager已关闭")
	}

	// 停止代理池健康检查
	if tm.proxyPool != nil {
		tm.proxyPool.Stop()
	}
}

// AddToken 添加新 token（自动保存）
func (tm *TokenManager) AddToken(config AuthConfig) error {
	tm.mutex.Lock()
	defer tm.mutex.Unlock()

	// 检查是否已存在相同的 refresh_token
	for _, existing := range tm.configs {
		if existing.RefreshToken == config.RefreshToken {
			return fmt.Errorf("该 refresh_token 已存在")
		}
	}

	// 添加到配置列表
	tm.configs = append(tm.configs, config)

	// 重新生成配置顺序
	tm.configOrder = generateConfigOrder(tm.configs)

	logger.Info("添加新 token",
		logger.String("auth_type", config.AuthType),
		logger.Int("total_count", len(tm.configs)))

	// 同步配置文件
	configPath := tm.configPath
	if configPath == "" {
		configPath = DefaultConfigPath
	}
	if err := SaveConfigToFile(tm.configs, configPath); err != nil {
		logger.Warn("同步配置文件失败", logger.Err(err))
		return fmt.Errorf("%w: %v", ErrConfigPersistence, err)
	}

	return nil
}

// AddTokenWithoutSave 添加新 token（不保存，用于批量导入）
// 返回是否为重复 token
func (tm *TokenManager) AddTokenWithoutSave(config AuthConfig) error {
	tm.mutex.Lock()
	defer tm.mutex.Unlock()

	// 检查是否已存在相同的 refresh_token
	for _, existing := range tm.configs {
		if existing.RefreshToken == config.RefreshToken {
			return fmt.Errorf("重复的 refresh_token")
		}
	}

	// 添加到配置列表
	tm.configs = append(tm.configs, config)

	// 重新生成配置顺序
	tm.configOrder = generateConfigOrder(tm.configs)

	return nil
}

// SaveConfig 保存配置到文件
func (tm *TokenManager) SaveConfig() error {
	tm.mutex.RLock()
	configs := make([]AuthConfig, len(tm.configs))
	copy(configs, tm.configs)
	configPath := tm.configPath
	tm.mutex.RUnlock()

	if configPath == "" {
		configPath = DefaultConfigPath
	}

	return SaveConfigToFile(configs, configPath)
}

// UpdateToken 更新 token
func (tm *TokenManager) UpdateToken(index int, authType, refreshToken, clientID, clientSecret string, disabled *bool) error {
	tm.mutex.Lock()
	defer tm.mutex.Unlock()

	if index < 0 || index >= len(tm.configs) {
		return fmt.Errorf("索引超出范围: %d", index)
	}

	// 更新字段（只更新非空值）
	if authType != "" {
		tm.configs[index].AuthType = authType
	}
	if refreshToken != "" {
		tm.configs[index].RefreshToken = refreshToken
	}
	if clientID != "" {
		tm.configs[index].ClientID = clientID
	}
	if clientSecret != "" {
		tm.configs[index].ClientSecret = clientSecret
	}
	if disabled != nil {
		tm.configs[index].Disabled = *disabled
	}

	logger.Info("更新 token",
		logger.Int("index", index),
		logger.String("auth_type", tm.configs[index].AuthType))

	// 同步配置文件
	configPath := tm.configPath
	if configPath == "" {
		configPath = DefaultConfigPath
	}
	if err := SaveConfigToFile(tm.configs, configPath); err != nil {
		logger.Warn("同步配置文件失败", logger.Err(err))
		return fmt.Errorf("%w: %v", ErrConfigPersistence, err)
	}

	return nil
}

// GetProxies 获取代理列表
func (tm *TokenManager) GetProxies() []map[string]interface{} {
	tm.mutex.RLock()
	defer tm.mutex.RUnlock()

	if tm.proxyPool == nil {
		return []map[string]interface{}{}
	}

	return tm.proxyPool.GetProxyList()
}

// AddProxy 添加代理
func (tm *TokenManager) AddProxy(proxyURL string) error {
	tm.mutex.Lock()
	defer tm.mutex.Unlock()

	// 如果代理池不存在，创建一个
	if tm.proxyPool == nil {
		var err error
		tm.proxyPool, err = NewProxyPoolManager([]string{proxyURL})
		if err != nil {
			return fmt.Errorf("创建代理池失败: %w", err)
		}
		logger.Info("创建代理池并添加代理", logger.String("proxy_url", proxyURL))
		return nil
	}

	// 添加到现有代理池
	return tm.proxyPool.AddProxy(proxyURL)
}

// RemoveProxy 删除代理
func (tm *TokenManager) RemoveProxy(index int) error {
	tm.mutex.Lock()
	defer tm.mutex.Unlock()

	if tm.proxyPool == nil {
		return fmt.Errorf("代理池未初始化")
	}

	return tm.proxyPool.RemoveProxy(index)
}
