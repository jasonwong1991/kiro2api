package auth

import (
	"context"
	"fmt"
	"kiro2api/config"
	"kiro2api/logger"
	"kiro2api/types"
	"kiro2api/utils"
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
// 余额 < 1 视为耗尽（即只有 >= 1 才可用）
const MinAvailableThreshold = 1.0

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
	batchSize         int   // 活跃池大小（0=使用全部账号）
	activePool        []int // 活跃账号池（存储配置索引）
	poolRoundRobin    int   // 活跃池内轮询索引
	poolWarmupRunning bool  // 活跃池后台预热任务是否运行中（仅在 mutex 保护下访问）

	// 自动管理相关字段
	autoRemoveInvalid bool // 是否自动删除失效的账号

	// 代理池管理器
	proxyPool *ProxyPoolManager // 代理池（可选）

	// 定时刷新相关字段
	refreshTicker *time.Ticker // 定时器
	refreshStop   chan bool    // 停止信号

	// 刷新管理器（防止重复刷新）
	refreshManager *utils.TokenRefreshManager

	// 低余额验证冷却时间（防止频繁刷新已用完的账号）
	lowBalanceVerified map[int]time.Time // 索引 -> 最后验证时间

	// 生命周期控制（优雅关闭）
	ctx    context.Context
	cancel context.CancelFunc
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
	Index         int // 配置索引
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

	// 创建生命周期context
	ctx, cancel := context.WithCancel(context.Background())

	tm := &TokenManager{
		cache:              NewSimpleTokenCache(config.TokenCacheTTL),
		configs:            configs,
		configOrder:        configOrder,
		currentIndex:       0,
		exhausted:          make(map[string]bool),
		invalidated:        make(map[int]time.Time),
		lowBalanceVerified: make(map[int]time.Time), // 初始化低余额验证记录
		strategy:           strategy,
		configPath:         configPath,
		batchSize:          batchSize,
		activePool:         []int{}, // 初始为空，首次使用时构建
		poolRoundRobin:     0,
		autoRemoveInvalid:  autoRemove,
		proxyPool:          proxyPool,
		refreshStop:        make(chan bool, 1),
		refreshManager:     utils.NewTokenRefreshManager(), // 初始化刷新管理器
		ctx:                ctx,
		cancel:             cancel,
	}

	// 如果使用活跃池策略，启动定时刷新任务
	if batchSize > 0 && strategy == StrategyRoundRobin {
		// 每5分钟刷新一次活跃池中快要过期的 token
		tm.refreshTicker = time.NewTicker(5 * time.Minute)
		go tm.startPeriodicRefresh()
		logger.Info("启动活跃池定时刷新任务", logger.Duration("interval", 5*time.Minute))
	}

	// 启动缓存快照定时保存
	tm.startCacheSnapshotSaver()

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

// IsAllTokensUnavailable 检查是否所有token都不可用
// 用于快速失败，避免无意义的重试
func (tm *TokenManager) IsAllTokensUnavailable() bool {
	tm.mutex.RLock()
	defer tm.mutex.RUnlock()

	// 检查是否有任何可用的token
	for i, key := range tm.configOrder {
		// *** 新增：跳过已失效的token ***
		if _, isInvalid := tm.invalidated[i]; isInvalid {
			continue
		}

		if cached, exists := tm.cache.tokens[key]; exists {
			if cached.IsUsable() {
				return false
			}
		}
	}
	return true
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

	// *** 优化：仅在首次降到低余额时触发验证 ***
	// 如果余额降到接近0（< 1.0），异步触发刷新来验证真实余额
	// 但如果已经验证过且确认用完（在 lowBalanceVerified 中有记录），则不再触发
	// 这样可以避免反复刷新已用完的账号
	if bestToken.Available < 1.0 && oldAvailable >= 1.0 {
		// 注意：当前已持有 tm.mutex，禁止重复加锁（否则会死锁）
		_, alreadyVerified := tm.lowBalanceVerified[bestToken.Index]

		// 只有未验证过的账号才触发验证
		if !alreadyVerified && bestToken.Index >= 0 {
			go tm.verifyLowBalanceToken(bestToken.Index)
		}
	}

	// 复制 token 信息
	token := bestToken.Token
	token.ConfigIndex = bestToken.Index

	// 注入代理客户端（如果启用）
	if tm.proxyPool != nil && bestToken.Index >= 0 {
		tokenIndexStr := fmt.Sprintf("%d", bestToken.Index)
		if _, client, err := tm.proxyPool.GetProxyForToken(tokenIndexStr); err == nil && client != nil {
			token.HTTPClient = client
		}
	}

	return token, nil
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

	// *** 优化：仅在首次降到低余额时触发验证 ***
	// 如果余额降到接近0（< 1.0），异步触发刷新来验证真实余额
	// 但如果已经验证过且确认用完（在 lowBalanceVerified 中有记录），则不再触发
	if bestToken.Available < 1.0 && oldAvailable >= 1.0 {
		// 检查是否已经验证过且确认用完
		_, alreadyVerified := tm.lowBalanceVerified[bestToken.Index]

		// 只有未验证过的账号才触发验证
		if !alreadyVerified && bestToken.Index >= 0 {
			go tm.verifyLowBalanceToken(bestToken.Index)
		}
	}

	// 复制 token 信息
	token := bestToken.Token
	token.ConfigIndex = bestToken.Index

	// 注入代理客户端（如果启用）
	if tm.proxyPool != nil && bestToken.Index >= 0 {
		tokenIndexStr := fmt.Sprintf("%d", bestToken.Index)
		if _, client, err := tm.proxyPool.GetProxyForToken(tokenIndexStr); err == nil && client != nil {
			token.HTTPClient = client
		}
	}

	// 构造 TokenWithUsage
	tokenWithUsage := &types.TokenWithUsage{
		TokenInfo:       token,
		UsageLimits:     bestToken.UsageInfo,
		AvailableCount:  available, // 使用扣减后的余额
		LastUsageCheck:  bestToken.LastUsed,
		IsUsageExceeded: available < MinAvailableThreshold,
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
			// *** 新增：跳过已失效的token ***
			if _, isInvalid := tm.invalidated[cached.Index]; isInvalid {
				continue
			}

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

		// *** 新增：跳过已失效的token ***
		if _, isInvalid := tm.invalidated[tm.currentIndex]; isInvalid {
			tm.currentIndex = (tm.currentIndex + 1) % len(tm.configOrder)
			logger.Debug("token已失效，跳过",
				logger.String("invalid_key", currentKey),
				logger.Int("next_index", tm.currentIndex))
			continue
		}

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

	for i, key := range tm.configOrder {
		// *** 新增：跳过已失效的token ***
		if _, isInvalid := tm.invalidated[i]; isInvalid {
			continue
		}

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

// shouldSkipRefreshDueToQuota 判断是否应跳过刷新
// 条件：账号额度已耗尽，且下次重置时间尚未到达
func shouldSkipRefreshDueToQuota(cached *CachedToken, now time.Time) bool {
	if cached == nil {
		return false
	}
	if cached.Available >= MinAvailableThreshold {
		return false
	}
	if cached.NextResetTime.IsZero() {
		return false
	}
	return now.Before(cached.NextResetTime)
}

// collectUncachedCandidatesUnlocked 收集可用于后台预热的未缓存账号索引
// 调用者必须持有 tm.mutex
func (tm *TokenManager) collectUncachedCandidatesUnlocked() []int {
	candidates := make([]int, 0, len(tm.configs))
	for i, cfg := range tm.configs {
		if cfg.Disabled {
			continue
		}
		if _, isInvalid := tm.invalidated[i]; isInvalid {
			continue
		}
		cacheKey := fmt.Sprintf(config.TokenCacheKeyFormat, i)
		if _, exists := tm.cache.tokens[cacheKey]; exists {
			continue
		}
		candidates = append(candidates, i)
	}
	return candidates
}

// startAsyncPoolWarmupUnlocked 启动活跃池后台预热（非阻塞）
// 调用者必须持有 tm.mutex
func (tm *TokenManager) startAsyncPoolWarmupUnlocked(reason string) {
	if tm.batchSize <= 0 {
		return
	}
	if tm.poolWarmupRunning {
		return
	}

	candidates := tm.collectUncachedCandidatesUnlocked()
	if len(candidates) == 0 {
		return
	}

	tm.poolWarmupRunning = true
	logger.Info("启动活跃池后台预热",
		logger.String("reason", reason),
		logger.Int("candidate_count", len(candidates)),
		logger.Int("batch_size", tm.batchSize))

	go tm.asyncWarmupPoolTokens(candidates, reason)
}

// asyncWarmupPoolTokens 后台预热未缓存账号，避免在请求路径阻塞
func (tm *TokenManager) asyncWarmupPoolTokens(candidates []int, reason string) {
	logger.Debug("开始活跃池后台预热",
		logger.String("reason", reason),
		logger.Int("candidate_count", len(candidates)))

	healthyAdded := 0
	invalidDetected := false

	for _, index := range candidates {
		select {
		case <-tm.ctx.Done():
			logger.Debug("TokenManager已关闭，终止活跃池后台预热")
			tm.mutex.Lock()
			tm.poolWarmupRunning = false
			tm.mutex.Unlock()
			return
		default:
		}

		tm.mutex.RLock()
		if index < 0 || index >= len(tm.configs) {
			tm.mutex.RUnlock()
			continue
		}
		cfg := tm.configs[index]
		if cfg.Disabled {
			tm.mutex.RUnlock()
			continue
		}
		if _, isInvalid := tm.invalidated[index]; isInvalid {
			tm.mutex.RUnlock()
			continue
		}
		cacheKey := fmt.Sprintf(config.TokenCacheKeyFormat, index)
		if _, exists := tm.cache.tokens[cacheKey]; exists {
			tm.mutex.RUnlock()
			continue
		}
		tm.mutex.RUnlock()

		token, err := tm.refreshSingleToken(cfg, index)
		if err != nil {
			if types.IsTokenInvalidError(err) {
				tm.mutex.Lock()
				tm.markTokenInvalidUnlocked(index, "pool_warmup_refresh", err)
				tm.mutex.Unlock()
				invalidDetected = true
			}
			continue
		}

		usage, checkErr := tm.checkUsageLimitsWithRetry(index, token)
		if checkErr != nil {
			if types.IsTokenInvalidError(checkErr) {
				tm.mutex.Lock()
				tm.markTokenInvalidUnlocked(index, "pool_warmup_usage", checkErr)
				tm.mutex.Unlock()
				invalidDetected = true
			}
			continue
		}

		available := CalculateAvailableCount(usage)
		nextResetTime := GetNextResetTime(usage)

		tm.mutex.Lock()
		if index >= 0 && index < len(tm.configs) {
			cacheKey = fmt.Sprintf(config.TokenCacheKeyFormat, index)
			now := time.Now()
			tm.cache.tokens[cacheKey] = &CachedToken{
				Token:         token,
				Index:         index,
				UsageInfo:     usage,
				CachedAt:      now,
				LastCheckAt:   now,
				Available:     available,
				NextResetTime: nextResetTime,
			}
			delete(tm.invalidated, index)
			if available >= MinAvailableThreshold {
				healthyAdded++
			}
		}
		tm.mutex.Unlock()
	}

	tm.mutex.Lock()
	defer tm.mutex.Unlock()

	if invalidDetected {
		tm.removeInvalidTokensImmediatelyUnlocked("pool_warmup")
	}

	// 预热完成后重建活跃池（仅基于缓存，不做网络请求）
	tm.buildActivePool()
	tm.poolWarmupRunning = false

	logger.Info("活跃池后台预热完成",
		logger.String("reason", reason),
		logger.Int("candidate_count", len(candidates)),
		logger.Int("healthy_added", healthyAdded),
		logger.Int("active_pool_size", len(tm.activePool)))
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
		// 只检查余额是否足够（>= 阈值才可用，< 阈值视为耗尽）
		return cached.Available >= MinAvailableThreshold
	}

	// 如果没有缓存且需要强制刷新
	if forceRefresh {
		logger.Debug("账号无缓存，尝试刷新获取状态", logger.Int("index", index))

		// 记录刷新开始时间，用于防止竞态条件
		refreshStartTime := time.Now()

		// 刷新账号获取最新状态（注意：这里需要避免死锁，使用内部方法）
		cfg := tm.configs[index]
		token, err := tm.refreshSingleToken(cfg, index)
		if err != nil {
			logger.Debug("刷新账号失败", logger.Int("index", index), logger.Err(err))
			// 如果是 token 失效错误，标记为失效
			if types.IsTokenInvalidError(err) {
				tm.markTokenInvalidUnlocked(index, "health_check_refresh", err)
			}
			return false
		}

		// 检查使用限制（带代理切换重试）
		if usage, checkErr := tm.checkUsageLimitsWithRetry(index, token); checkErr == nil {
			available := CalculateAvailableCount(usage)
			nextResetTime := GetNextResetTime(usage)

			// 更新缓存
			now := time.Now()
			tm.cache.tokens[cacheKey] = &CachedToken{
				Token:         token,
				Index:         index,
				UsageInfo:     usage,
				CachedAt:      now,
				LastCheckAt:   now,
				Available:     available,
				NextResetTime: nextResetTime,
			}

			// 使用限制检查成功，清除失效标记（带时间戳保护）
			// 只有当失效时间早于刷新开始时间时才清除，防止清除更新的失效标记
			if invalidTime, exists := tm.invalidated[index]; !exists || invalidTime.Before(refreshStartTime) {
				delete(tm.invalidated, index)
			} else {
				logger.Debug("检测到更新的失效标记，保留失效状态",
					logger.Int("index", index),
					logger.String("invalid_time", invalidTime.Format(time.RFC3339)),
					logger.String("refresh_start", refreshStartTime.Format(time.RFC3339)))
			}

			logger.Debug("成功刷新账号状态",
				logger.Int("index", index),
				logger.Float64("available", available))

			// 返回健康状态（>= 阈值才可用）
			return available >= MinAvailableThreshold
		} else {
			logger.Debug("检查使用限制失败", logger.Int("index", index), logger.Err(checkErr))
			// 如果是 token 失效错误，标记为失效
			if types.IsTokenInvalidError(checkErr) {
				tm.markTokenInvalidUnlocked(index, "health_check_usage", checkErr)
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

	uncachedCandidates := len(tm.collectUncachedCandidatesUnlocked())
	if len(healthyIndices) < tm.batchSize && uncachedCandidates > 0 {
		// 关键修复：不要在请求路径下同步刷新未缓存账号，避免持锁网络请求拖死全局并发。
		tm.startAsyncPoolWarmupUnlocked("build_active_pool")
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
		logger.Int("healthy_total", len(healthyIndices)),
		logger.Int("uncached_candidates", uncachedCandidates),
		logger.Bool("warmup_running", tm.poolWarmupRunning))
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
		// 关键修复：仅使用已缓存的健康账号，避免在请求路径做阻塞刷新。
		if tm.isAccountHealthy(i) {
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
	tm.startAsyncPoolWarmupUnlocked("replace_unhealthy_account")

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
	// 记录本轮是否已经尝试过替换，避免频繁替换
	hasTriedReplacement := false

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
					// 小于阈值视为已耗尽
					if cached.Available < MinAvailableThreshold {
						reason = fmt.Sprintf("余额已耗尽 (当前: %.2f, 阈值: %.2f)", cached.Available, MinAvailableThreshold)
					}
				} else {
					reason = "缓存不存在"
				}
				logger.Info("活跃池中发现不健康账号",
					logger.Int("config_index", configIndex),
					logger.Int("pool_index", relativeIndex),
					logger.String("reason", reason))

				// *** 新增：立即尝试替换不健康账号 ***
				// 每轮只替换一次，避免频繁刷新
				if !hasTriedReplacement {
					hasTriedReplacement = true
					logger.Info("立即尝试替换不健康账号",
						logger.Int("config_index", configIndex))

					replaced := tm.replaceUnhealthyAccount(configIndex)
					if replaced {
						// 替换成功，重新计算池大小并从头开始轮询
						poolSize = len(tm.activePool)
						if poolSize == 0 {
							logger.Warn("替换后活跃池为空")
							break
						}
						logger.Info("替换成功，从头重新开始轮询",
							logger.Int("new_pool_size", poolSize))
						// 重置attempt和startIndex，从头开始
						attempt = -1 // 下次循环会+1变成0
						startIndex = 0
						unhealthyAccounts = make(map[int]bool)
						hasTriedReplacement = false // 重置标志，允许在新一轮中再次替换
						continue
					} else {
						// 替换失败（可能已移除不健康账号），更新池大小
						poolSize = len(tm.activePool)
						if poolSize == 0 {
							logger.Warn("移除不健康账号后活跃池为空")
							break
						}
						logger.Warn("替换失败，继续轮询剩余账号",
							logger.Int("remaining_pool_size", poolSize))
						// 池已改变，从头重新开始轮询
						attempt = -1
						startIndex = 0
						hasTriedReplacement = false // 重置标志，允许在新一轮中再次替换
						continue
					}
				}
			}
			// 继续下一次尝试
			continue
		}

		// 获取 token
		if cached, exists := tm.cache.tokens[currentKey]; exists {
			// 如果 token 过期，触发异步刷新（不阻塞）
			if time.Now().After(cached.Token.ExpiresAt) {
				logger.Debug("活跃池中token过期，触发异步刷新",
					logger.Int("config_index", configIndex),
					logger.String("expired_at", cached.Token.ExpiresAt.Format(time.RFC3339)))

				// 异步刷新，不阻塞当前请求
				go tm.asyncRefreshToken(configIndex)
				// 继续尝试下一个token
				continue
			}

			// 检查 token 是否可用（余额 >= 阈值才可用）
			if cached.Available >= MinAvailableThreshold {
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

	// 一轮结束后，兜底处理剩余的不健康账号（理论上不应该执行到这里）
	// 因为我们已经在检测到不健康账号时立即替换了
	// 这里作为兜底机制，处理可能遗漏的情况
	if len(unhealthyAccounts) > 0 {
		logger.Warn("轮询结束后仍有未处理的不健康账号（兜底处理）",
			logger.Int("unhealthy_count", len(unhealthyAccounts)))
		// 只替换第一个不健康账号，避免过度刷新
		for unhealthyIndex := range unhealthyAccounts {
			logger.Info("兜底：尝试替换不健康账号",
				logger.Int("config_index", unhealthyIndex))

			replaced := tm.replaceUnhealthyAccount(unhealthyIndex)
			if !replaced {
				// 如果找不到替换账号，只移除这一个不健康账号
				logger.Warn("兜底：无可用替换账号，仅移除不健康账号",
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

		// *** 新增：跳过已失效的token ***
		if _, isInvalid := tm.invalidated[configIndex]; !isInvalid {
			if cached, exists := tm.cache.tokens[currentKey]; exists {
				if cached.IsUsable() {
					tm.poolRoundRobin = 1 % len(tm.activePool)
					return cached
				}
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

		// 检查缓存是否存在且 token 是否即将过期
		cached, exists := tm.cache.tokens[currentKey]
		needRefresh := false

		if !exists {
			// 缓存不存在，需要刷新
			needRefresh = true
			logger.Debug("账号缓存不存在，需要刷新",
				logger.String("key", currentKey),
				logger.Int("index", configIndex))
		} else {
			// *** 修复：基于 token 实际过期时间判断，而不是缓存时间 ***
			// 只有在 token 还剩 5 分钟就要过期时才刷新
			timeUntilExpiry := time.Until(cached.Token.ExpiresAt)
			if timeUntilExpiry < 5*time.Minute {
				needRefresh = true
				logger.Debug("token 即将过期，需要刷新",
					logger.String("key", currentKey),
					logger.Int("index", configIndex),
					logger.Duration("time_until_expiry", timeUntilExpiry),
					logger.String("expires_at", cached.Token.ExpiresAt.Format(time.RFC3339)))
			}
		}

		// 如果需要刷新且账号未被禁用/失效
		if needRefresh && configIndex < len(tm.configs) {
			cfg := tm.configs[configIndex]
			if !cfg.Disabled {
				if _, isInvalid := tm.invalidated[configIndex]; !isInvalid {
					// 余额耗尽且尚未到重置时间时，跳过无意义刷新
					if exists && shouldSkipRefreshDueToQuota(cached, time.Now()) {
						logger.Debug("账号额度耗尽且未到重置时间，跳过刷新",
							logger.Int("index", configIndex),
							logger.Float64("available", cached.Available),
							logger.String("next_reset", cached.NextResetTime.Format(time.RFC3339)))
					} else {
						// 触发异步刷新（不阻塞）
						logger.Debug("触发异步刷新单个账号",
							logger.Int("index", configIndex),
							logger.String("auth_type", cfg.AuthType))

						go tm.asyncRefreshToken(configIndex)
					}
				}
			}
		}

		// 再次检查缓存（刷新后的结果）
		if cached, exists := tm.cache.tokens[currentKey]; exists {
			// *** 新增：跳过已失效的token ***
			if _, isInvalid := tm.invalidated[configIndex]; !isInvalid {
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

	// 自动清理低余额验证记录（每月1日）
	now := time.Now()
	if now.Day() == 1 {
		// 检查是否需要清理（避免同一天多次清理）
		needClear := true
		for _, verifiedTime := range tm.lowBalanceVerified {
			// 如果有任何记录是今天的，说明已经清理过了
			if verifiedTime.Year() == now.Year() && verifiedTime.Month() == now.Month() && verifiedTime.Day() == now.Day() {
				needClear = false
				break
			}
		}
		if needClear && len(tm.lowBalanceVerified) > 0 {
			count := len(tm.lowBalanceVerified)
			tm.lowBalanceVerified = make(map[int]time.Time)
			logger.Info("自动清除低余额验证记录（月度重置）",
				logger.Int("cleared_count", count))
		}
	}

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

	invalidDetected := false

	// 刷新指定索引的 token
	for _, i := range refreshIndices {
		cfg := tm.configs[i]
		if cfg.Disabled {
			continue
		}

		// 优化：检查是否额度耗尽且未到重置日期
		cacheKey := fmt.Sprintf(config.TokenCacheKeyFormat, i)
		if cached, exists := tm.cache.tokens[cacheKey]; exists {
			// 如果额度耗尽（available < 阈值）且重置日期未到（在未来）
			if cached.Available < MinAvailableThreshold && !cached.NextResetTime.IsZero() && time.Now().Before(cached.NextResetTime) {
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
				tm.markTokenInvalidUnlocked(i, "refresh_cache_refresh", err)
				invalidDetected = true
			} else {
				logger.Warn("刷新单个token失败",
					logger.Int("config_index", i),
					logger.String("auth_type", cfg.AuthType),
					logger.Err(err))
			}
			continue
		}

		// 检查使用限制（带代理切换重试）
		var usageInfo *types.UsageLimits
		var available float64
		var nextResetTime time.Time

		if usage, checkErr := tm.checkUsageLimitsWithRetry(i, token); checkErr == nil {
			usageInfo = usage
			available = CalculateAvailableCount(usage)
			nextResetTime = GetNextResetTime(usage)

			// 使用限制检查成功，清除失效标记
			delete(tm.invalidated, i)
		} else {
			// 检查是否是 token 失效错误
			if types.IsTokenInvalidError(checkErr) {
				logger.Warn("使用限制检查检测到token失效",
					logger.Int("config_index", i),
					logger.String("auth_type", cfg.AuthType),
					logger.Err(checkErr))
				tm.markTokenInvalidUnlocked(i, "refresh_cache_usage", checkErr)
				invalidDetected = true
			} else {
				logger.Warn("检查使用限制失败", logger.Err(checkErr))
			}
		}

		// 更新缓存（直接访问，已在tm.mutex保护下）
		cacheKey = fmt.Sprintf(config.TokenCacheKeyFormat, i)
		now := time.Now()
		tm.cache.tokens[cacheKey] = &CachedToken{
			Token:         token,
			Index:         i,
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

	// 刷新中检测到失效账号时，立即删除，避免占用轮询池
	if invalidDetected {
		tm.removeInvalidTokensImmediatelyUnlocked("refresh_cache")
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

	// 检查可用次数是否达到阈值（>= 阈值才可用，< 阈值视为耗尽）
	return ct.Available >= MinAvailableThreshold
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

// parseResetTimestamp 解析 API 返回的 nextDateReset 时间戳
// 支持秒/毫秒/微秒/纳秒（带小数秒）并统一转换为 UTC 时间。
func parseResetTimestamp(ts float64) (time.Time, bool) {
	if ts <= 0 {
		return time.Time{}, false
	}

	seconds := ts
	switch {
	case ts > 1e18: // 纳秒
		seconds = ts / 1e9
	case ts > 1e15: // 微秒
		seconds = ts / 1e6
	case ts > 1e12: // 毫秒
		seconds = ts / 1e3
	}

	sec := int64(seconds)
	nsec := int64((seconds - float64(sec)) * float64(time.Second))
	parsed := time.Unix(sec, nsec).UTC()
	if parsed.Year() < 2000 || parsed.Year() > 2200 {
		return time.Time{}, false
	}
	return parsed, true
}

// getBillingResetLocation 获取账单重置时区
// 默认使用美国西海岸时区，可通过 KIRO_BILLING_TIMEZONE 覆盖。
func getBillingResetLocation() *time.Location {
	tz := os.Getenv("KIRO_BILLING_TIMEZONE")
	if tz == "" {
		tz = "America/Los_Angeles"
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.Local
	}
	return loc
}

// GetNextResetTime 获取下次重置时间
// 优先使用 API 返回的 nextDateReset；若无效则回退到账单时区的「每月1日 00:00」。
func GetNextResetTime(usage *types.UsageLimits) time.Time {
	now := time.Now()

	// 1) 优先使用 API 返回的重置时间，避免本地时区推算误差
	if usage != nil {
		candidates := make([]time.Time, 0, 4)
		if t, ok := parseResetTimestamp(usage.NextDateReset); ok {
			candidates = append(candidates, t)
		}
		for _, breakdown := range usage.UsageBreakdownList {
			if t, ok := parseResetTimestamp(breakdown.NextDateReset); ok {
				candidates = append(candidates, t)
			}
		}
		var selected time.Time
		for _, candidate := range candidates {
			// 允许少量时钟抖动，过滤明显过期的重置时间
			if candidate.After(now.Add(-2 * time.Hour)) {
				if selected.IsZero() || candidate.Before(selected) {
					selected = candidate
				}
			}
		}
		if !selected.IsZero() {
			return selected
		}
	}

	// 2) 回退：按账单时区计算下个月1日 00:00
	loc := getBillingResetLocation()
	nowInLoc := now.In(loc)
	year := nowInLoc.Year()
	month := nowInLoc.Month() + 1
	if month > 12 {
		month = 1
		year++
	}
	return time.Date(year, month, 1, 0, 0, 0, 0, loc)
}

// getUsageCheckerForToken 获取指定token索引的usage checker（带代理支持）
// 内部方法：调用者必须确保索引有效
// 返回 checker 和使用的代理 URL（如果有）
func (tm *TokenManager) getUsageCheckerForToken(tokenIndex int) (*UsageLimitsChecker, string) {
	// 如果没有代理池，使用默认客户端
	if tm.proxyPool == nil {
		return NewUsageLimitsChecker(tokenIndex), ""
	}

	// 获取代理客户端
	tokenIndexStr := fmt.Sprintf("%d", tokenIndex)
	proxyURL, client, err := tm.proxyPool.GetProxyForToken(tokenIndexStr)
	if err != nil {
		logger.Warn("获取代理失败，usage checker使用默认客户端",
			logger.String("token_index", tokenIndexStr),
			logger.Err(err))
		return NewUsageLimitsChecker(tokenIndex), ""
	}

	return NewUsageLimitsChecker(tokenIndex, client), proxyURL
}

// checkUsageLimitsWithRetry 带代理切换重试的使用限制检查
func (tm *TokenManager) checkUsageLimitsWithRetry(index int, token types.TokenInfo) (*types.UsageLimits, error) {
	maxRetries := 1
	if tm.proxyPool != nil {
		maxRetries = 3
	}

	tokenIndexStr := fmt.Sprintf("%d", index)
	var lastErr error

	for attempt := 0; attempt < maxRetries; attempt++ {
		checker, proxyURL := tm.getUsageCheckerForToken(index)
		usage, err := checker.CheckUsageLimits(token)
		if err == nil {
			return usage, nil
		}

		lastErr = err

		// 如果是 Token 失效错误，不重试
		if types.IsTokenInvalidError(err) {
			return nil, err
		}

		// 如果配置了代理池，报告失败并重置绑定以便下次获取新代理
		if tm.proxyPool != nil {
			if proxyURL != "" {
				tm.proxyPool.ReportProxyFailure(proxyURL)
			}
			tm.proxyPool.ResetTokenProxy(tokenIndexStr)
			logger.Warn("使用限制检查失败，切换代理重试",
				logger.String("token_index", tokenIndexStr),
				logger.Int("attempt", attempt+1),
				logger.Err(err))
		}
	}

	return nil, lastErr
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

// rebuildCacheAfterDeletion 删除账号后重建缓存
// 因为删除账号会导致所有后续账号的索引前移，需要重建整个缓存
// 调用者必须持有 tm.mutex 锁
func (tm *TokenManager) rebuildCacheAfterDeletion() {
	// 保存旧缓存
	oldCache := tm.cache.tokens

	// 创建新缓存
	newCache := make(map[string]*CachedToken)

	// 遍历旧缓存，根据 refresh_token 匹配新索引
	for _, oldCached := range oldCache {
		// 在新配置中查找对应的账号
		for newIndex, cfg := range tm.configs {
			if cfg.RefreshToken == oldCached.Token.RefreshToken {
				// 找到匹配的账号，使用新索引
				newKey := fmt.Sprintf(config.TokenCacheKeyFormat, newIndex)
				newCached := &CachedToken{
					Token:         oldCached.Token,
					Index:         newIndex, // 使用新索引
					UsageInfo:     oldCached.UsageInfo,
					CachedAt:      oldCached.CachedAt,
					LastCheckAt:   oldCached.LastCheckAt,
					LastUsed:      oldCached.LastUsed,
					Available:     oldCached.Available,
					NextResetTime: oldCached.NextResetTime,
				}
				newCache[newKey] = newCached
				logger.Debug("重建缓存：账号索引更新",
					logger.Int("old_index", oldCached.Index),
					logger.Int("new_index", newIndex),
					logger.String("new_key", newKey))
				break
			}
		}
	}

	// 替换缓存
	tm.cache.tokens = newCache

	logger.Info("删除账号后重建缓存完成",
		logger.Int("old_cache_size", len(oldCache)),
		logger.Int("new_cache_size", len(newCache)))
}

// removeFromActivePoolUnlocked 从活跃池移除指定账号（内部方法，调用者必须持有锁）
func (tm *TokenManager) removeFromActivePoolUnlocked(index int) bool {
	if len(tm.activePool) == 0 {
		return false
	}

	for i, poolIndex := range tm.activePool {
		if poolIndex != index {
			continue
		}

		tm.activePool = append(tm.activePool[:i], tm.activePool[i+1:]...)
		if len(tm.activePool) == 0 {
			tm.poolRoundRobin = 0
		} else if tm.poolRoundRobin >= len(tm.activePool) {
			tm.poolRoundRobin = tm.poolRoundRobin % len(tm.activePool)
		}

		logger.Info("从活跃池中移除失效token",
			logger.Int("index", index),
			logger.Int("remaining_pool_size", len(tm.activePool)))
		return true
	}

	return false
}

// markTokenInvalidUnlocked 标记 token 失效并移出活跃池（内部方法，调用者必须持有锁）
func (tm *TokenManager) markTokenInvalidUnlocked(index int, source string, err error) bool {
	if index < 0 || index >= len(tm.configs) {
		logger.Warn("尝试标记无效索引的token为失效",
			logger.Int("index", index),
			logger.Int("configs_len", len(tm.configs)),
			logger.String("source", source))
		return false
	}

	if _, exists := tm.invalidated[index]; exists {
		// 即使已标记失效，也确保不占用活跃池
		tm.removeFromActivePoolUnlocked(index)
		return false
	}

	tm.invalidated[index] = time.Now()
	tm.removeFromActivePoolUnlocked(index)

	if err != nil {
		logger.Warn("标记token为失效",
			logger.Int("index", index),
			logger.String("auth_type", tm.configs[index].AuthType),
			logger.String("source", source),
			logger.Err(err))
	} else {
		logger.Warn("标记token为失效",
			logger.Int("index", index),
			logger.String("auth_type", tm.configs[index].AuthType),
			logger.String("source", source))
	}

	return true
}

// removeInvalidTokensImmediatelyUnlocked 立即删除所有失效 token（内部方法，调用者必须持有锁）
func (tm *TokenManager) removeInvalidTokensImmediatelyUnlocked(trigger string) {
	if len(tm.invalidated) == 0 {
		return
	}

	removed, err := tm.removeInvalidTokensUnlocked()
	if err != nil {
		logger.Warn("立即删除失效账号失败",
			logger.String("trigger", trigger),
			logger.Err(err))
		return
	}

	if removed > 0 {
		logger.Info("立即删除失效账号完成",
			logger.String("trigger", trigger),
			logger.Int("removed_count", removed),
			logger.Int("remaining_count", len(tm.configs)))
	}
}

// MarkTokenInvalid 标记指定索引的token为失效
// 用于在请求时检测到token失效（如401/403错误）时立即标记
// 这样可以避免后续请求继续使用已失效的token
func (tm *TokenManager) MarkTokenInvalid(index int) {
	tm.mutex.Lock()
	defer tm.mutex.Unlock()

	before := len(tm.configs)
	tm.markTokenInvalidUnlocked(index, "request_invalid", nil)
	tm.removeInvalidTokensImmediatelyUnlocked("request_invalid")
	after := len(tm.configs)

	logger.Info("请求阶段失效token处理完成",
		logger.Int("index", index),
		logger.Int("before_count", before),
		logger.Int("after_count", after),
		logger.Int("removed_count", before-after))
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

	// 更新低余额验证记录（索引需要调整）
	newLowBalanceVerified := make(map[int]time.Time)
	for i, t := range tm.lowBalanceVerified {
		if i < index {
			newLowBalanceVerified[i] = t
		} else if i > index {
			newLowBalanceVerified[i-1] = t
		}
	}
	tm.lowBalanceVerified = newLowBalanceVerified

	// 重新生成配置顺序
	tm.configOrder = generateConfigOrder(tm.configs)

	// 重建整个缓存（因为所有索引都变了）
	tm.rebuildCacheAfterDeletion()

	// 清空并重建活跃池（如果使用活跃池策略）
	if tm.batchSize > 0 {
		tm.activePool = []int{}
		tm.poolRoundRobin = 0
		logger.Info("删除账号后清空活跃池，下次使用时将重建")
	}

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
		// 只收集有效范围内的索引
		if i >= 0 && i < len(tm.configs) {
			indices = append(indices, i)
		} else {
			logger.Warn("检测到超出范围的失效索引，跳过",
				logger.Int("index", i),
				logger.Int("configs_len", len(tm.configs)))
		}
	}

	if len(indices) == 0 {
		// 所有索引都超出范围，清空失效记录
		tm.invalidated = make(map[int]time.Time)
		return 0, nil
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
		tm.configs = append(tm.configs[:index], tm.configs[index+1:]...)
		removedCount++
	}

	// 清空失效记录和低余额验证记录
	tm.invalidated = make(map[int]time.Time)
	tm.lowBalanceVerified = make(map[int]time.Time)

	// 重新生成配置顺序
	tm.configOrder = generateConfigOrder(tm.configs)

	// 重建整个缓存（因为所有索引都变了）
	tm.rebuildCacheAfterDeletion()

	// 清空并重建活跃池（如果使用活跃池策略）
	if tm.batchSize > 0 {
		tm.activePool = []int{}
		tm.poolRoundRobin = 0
		logger.Info("批量删除账号后清空活跃池，下次使用时将重建")
	}

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
// 优先尝试从缓存快照恢复，如果配置变更则从头刷新
// 使用并发模式加速初始化，并发度 = batchSize
func (tm *TokenManager) InitializeBatchTokens() error {
	// 尝试从缓存快照恢复（不持锁，TryRestoreCache 内部会加锁）
	if tm.TryRestoreCache() {
		// 恢复成功，检查活跃池是否满足要求
		tm.mutex.Lock()
		activePoolSize := len(tm.activePool)
		requiredSize := tm.batchSize
		if requiredSize == 0 {
			requiredSize = 1
		}
		tm.mutex.Unlock()

		if activePoolSize >= requiredSize {
			logger.Info("从缓存快照恢复成功，跳过初始化刷新",
				logger.Int("active_pool_size", activePoolSize),
				logger.Int("required_size", requiredSize))
			return nil
		}
		logger.Info("缓存快照恢复但活跃池不足，继续刷新补充",
			logger.Int("active_pool_size", activePoolSize),
			logger.Int("required_size", requiredSize))
	}

	// 确定需要初始化的账号数量和并发度
	tm.mutex.Lock()
	var initCount int
	var concurrency int
	if tm.batchSize > 0 {
		initCount = tm.batchSize
		concurrency = tm.batchSize
		if initCount > len(tm.configs) {
			initCount = len(tm.configs)
		}
		logger.Info("使用活跃池模式（并发初始化）",
			logger.Int("batch_size", tm.batchSize),
			logger.Int("init_count", initCount),
			logger.Int("concurrency", concurrency),
			logger.Int("total_configs", len(tm.configs)))
	} else {
		initCount = 1
		concurrency = 1
		logger.Info("使用常规模式，只刷新第一个账号")
	}

	// 准备任务列表（所有非禁用账号）
	type initTask struct {
		index int
		cfg   AuthConfig
	}
	tasks := make([]initTask, 0, len(tm.configs))
	for i, cfg := range tm.configs {
		if !cfg.Disabled {
			tasks = append(tasks, initTask{index: i, cfg: cfg})
		}
	}
	tm.mutex.Unlock()

	if len(tasks) == 0 {
		return fmt.Errorf("没有可用的账号配置")
	}

	logger.Info("开始并发初始化token",
		logger.Int("task_count", len(tasks)),
		logger.Int("concurrency", concurrency),
		logger.Int("target_count", initCount))

	// 并发刷新结果
	type initResult struct {
		index         int
		token         types.TokenInfo
		usageInfo     *types.UsageLimits
		available     float64
		nextResetTime time.Time
		err           error
		isInvalid     bool
		isHealthy     bool // available >= MinAvailableThreshold
	}

	// 分批并发刷新，直到找到足够的健康账号
	successCount := 0
	healthyTokenSet := make(map[string]bool, initCount)
	invalidDetected := false
	taskOffset := 0

	for successCount < initCount && taskOffset < len(tasks) {
		// 计算本批次任务数量
		batchEnd := taskOffset + concurrency
		if batchEnd > len(tasks) {
			batchEnd = len(tasks)
		}
		batchTasks := tasks[taskOffset:batchEnd]
		taskOffset = batchEnd

		// 并发刷新本批次
		taskChan := make(chan initTask, len(batchTasks))
		resultChan := make(chan initResult, len(batchTasks))

		var wg sync.WaitGroup
		for i := 0; i < concurrency && i < len(batchTasks); i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for task := range taskChan {
					result := initResult{index: task.index}

					// 刷新 token
					token, err := tm.refreshSingleToken(task.cfg, task.index)
					if err != nil {
						result.err = err
						result.isInvalid = types.IsTokenInvalidError(err)
						resultChan <- result
						continue
					}

					result.token = token

					// 检查使用限制（带代理切换重试）
					if usage, checkErr := tm.checkUsageLimitsWithRetry(task.index, token); checkErr == nil {
						result.usageInfo = usage
						result.available = CalculateAvailableCount(usage)
						result.nextResetTime = GetNextResetTime(usage)
						result.isHealthy = result.available >= MinAvailableThreshold
					} else if types.IsTokenInvalidError(checkErr) {
						result.err = checkErr
						result.isInvalid = true
					}

					resultChan <- result
				}
			}()
		}

		// 发送任务
		for _, task := range batchTasks {
			taskChan <- task
		}
		close(taskChan)

		// 等待完成
		wg.Wait()
		close(resultChan)

		// 收集结果并更新缓存
		tm.mutex.Lock()
		for result := range resultChan {
			if result.err != nil {
				if result.isInvalid {
					logger.Warn("初始化时检测到token失效",
						logger.Int("index", result.index),
						logger.Err(result.err))
					tm.markTokenInvalidUnlocked(result.index, "initialize_batch", result.err)
					invalidDetected = true
				} else {
					logger.Warn("初始化刷新token失败",
						logger.Int("index", result.index),
						logger.Err(result.err))
				}
				continue
			}

			// 更新缓存
			cacheKey := fmt.Sprintf(config.TokenCacheKeyFormat, result.index)
			now := time.Now()
			tm.cache.tokens[cacheKey] = &CachedToken{
				Token:         result.token,
				Index:         result.index,
				UsageInfo:     result.usageInfo,
				CachedAt:      now,
				LastCheckAt:   now,
				Available:     result.available,
				NextResetTime: result.nextResetTime,
			}

			if result.isHealthy {
				successCount++
				healthyTokenSet[result.token.RefreshToken] = true
				logger.Info("成功初始化健康token",
					logger.Int("index", result.index),
					logger.Float64("available", result.available))
			} else {
				logger.Warn("初始化token但余额不足",
					logger.Int("index", result.index),
					logger.Float64("available", result.available))
			}
		}
		tm.mutex.Unlock()

		logger.Debug("批次初始化完成",
			logger.Int("batch_offset", taskOffset),
			logger.Int("success_count", successCount),
			logger.Int("target_count", initCount))
	}

	// 更新活跃池
	tm.mutex.Lock()
	if invalidDetected {
		tm.removeInvalidTokensImmediatelyUnlocked("initialize_batch")
	}

	healthyIndices := make([]int, 0, initCount)
	if tm.batchSize > 0 {
		for i, cfg := range tm.configs {
			if healthyTokenSet[cfg.RefreshToken] {
				healthyIndices = append(healthyIndices, i)
				if len(healthyIndices) >= tm.batchSize {
					break
				}
			}
		}
	}

	tm.activePool = healthyIndices
	tm.lastRefresh = time.Now()
	tm.mutex.Unlock()

	logger.Info("首批token初始化完成",
		logger.Int("success_count", successCount),
		logger.Int("desired_count", initCount),
		logger.Int("active_pool_size", len(healthyIndices)),
		logger.Int("total_configs", len(tm.configs)))

	if successCount == 0 {
		return fmt.Errorf("没有成功初始化任何健康token（余额>=1）")
	}

	return nil
}

// verifyLowBalanceToken 验证低余额账号的真实余额
// 当本地计算的余额降到接近0时，异步调用此方法刷新获取真实余额
// 注意：此方法只会被调用一次（通过 lowBalanceVerified 标记防止重复）
func (tm *TokenManager) verifyLowBalanceToken(index int) {
	logger.Info("触发低余额账号验证",
		logger.Int("index", index))

	// 记录刷新开始时间，用于防止竞态条件
	refreshStartTime := time.Now()

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
			tm.markTokenInvalidUnlocked(index, "low_balance_refresh", err)
			tm.removeInvalidTokensImmediatelyUnlocked("low_balance_refresh")
			tm.mutex.Unlock()
		}
		return
	}

	// 检查使用限制获取真实余额（带代理切换重试）
	usage, checkErr := tm.checkUsageLimitsWithRetry(index, token)
	if checkErr != nil {
		logger.Warn("低余额验证：检查使用限制失败",
			logger.Int("index", index),
			logger.Err(checkErr))
		if types.IsTokenInvalidError(checkErr) {
			tm.mutex.Lock()
			tm.markTokenInvalidUnlocked(index, "low_balance_usage", checkErr)
			tm.removeInvalidTokensImmediatelyUnlocked("low_balance_usage")
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
		Index:         index,
		UsageInfo:     usage,
		CachedAt:      now,
		LastCheckAt:   now,
		Available:     available,
		NextResetTime: nextResetTime,
	}

	// 使用限制检查成功，清除失效标记（带时间戳保护）
	// 只有当失效时间早于刷新开始时间时才清除，防止清除更新的失效标记
	if invalidTime, exists := tm.invalidated[index]; !exists || invalidTime.Before(refreshStartTime) {
		delete(tm.invalidated, index)
	} else {
		logger.Debug("检测到更新的失效标记，保留失效状态",
			logger.Int("index", index),
			logger.String("invalid_time", invalidTime.Format(time.RFC3339)),
			logger.String("refresh_start", refreshStartTime.Format(time.RFC3339)))
	}

	// *** 关键：只有确认用完时才记录，避免后续重复验证 ***
	if available < MinAvailableThreshold {
		// 记录此账号已验证且确认用完
		tm.lowBalanceVerified[index] = time.Now()
		logger.Warn("低余额验证：账号余额已耗尽，标记为已验证",
			logger.Int("index", index),
			logger.Float64("available", available),
			logger.Float64("threshold", MinAvailableThreshold))
	} else {
		// 账号仍有余额，不记录（允许下次再验证）
		logger.Info("低余额验证：账号仍有余额",
			logger.Int("index", index),
			logger.Float64("available", available))
	}

	tm.mutex.Unlock()
}

// asyncRefreshToken 异步刷新单个token（使用TokenRefreshManager防重复）
// 此方法不阻塞调用者，适用于在token选择过程中触发刷新
func (tm *TokenManager) asyncRefreshToken(index int) {
	// 额度耗尽且未到重置时间时，不做无意义刷新
	tm.mutex.RLock()
	cacheKey := fmt.Sprintf(config.TokenCacheKeyFormat, index)
	if cached, exists := tm.cache.tokens[cacheKey]; exists && shouldSkipRefreshDueToQuota(cached, time.Now()) {
		tm.mutex.RUnlock()
		logger.Debug("异步刷新跳过：额度耗尽且未到重置时间",
			logger.Int("index", index),
			logger.Float64("available", cached.Available),
			logger.String("next_reset", cached.NextResetTime.Format(time.RFC3339)))
		return
	}
	tm.mutex.RUnlock()

	// 使用TokenRefreshManager防止重复刷新
	_, isNew := tm.refreshManager.StartRefresh(index)
	if !isNew {
		// 已经有其他goroutine在刷新，直接返回
		logger.Debug("Token已在刷新中，跳过重复刷新",
			logger.Int("index", index))
		return
	}

	// 在新goroutine中执行刷新
	go func() {
		// 记录刷新开始时间，用于防止竞态条件
		refreshStartTime := time.Now()

		// 检查是否已关闭
		select {
		case <-tm.ctx.Done():
			logger.Debug("TokenManager已关闭，取消异步刷新",
				logger.Int("index", index))
			tm.refreshManager.CompleteRefresh(index, nil, fmt.Errorf("TokenManager已关闭"))
			return
		default:
		}

		// 短暂持锁获取配置
		tm.mutex.RLock()
		if index < 0 || index >= len(tm.configs) {
			tm.mutex.RUnlock()
			tm.refreshManager.CompleteRefresh(index, nil, fmt.Errorf("索引超出范围"))
			return
		}
		cfg := tm.configs[index]
		if cfg.Disabled {
			tm.mutex.RUnlock()
			tm.refreshManager.CompleteRefresh(index, nil, fmt.Errorf("账号已禁用"))
			return
		}
		tm.mutex.RUnlock()

		logger.Debug("开始异步刷新token",
			logger.Int("index", index))

		// 刷新token（不持锁）
		token, err := tm.refreshSingleToken(cfg, index)
		if err != nil {
			logger.Warn("异步刷新token失败",
				logger.Int("index", index),
				logger.Err(err))
			if types.IsTokenInvalidError(err) {
				tm.mutex.Lock()
				tm.markTokenInvalidUnlocked(index, "async_refresh", err)
				tm.removeInvalidTokensImmediatelyUnlocked("async_refresh")
				tm.mutex.Unlock()
			}
			tm.refreshManager.CompleteRefresh(index, nil, err)
			return
		}

		// 检查使用限制（带代理切换重试，不持锁）
		usage, checkErr := tm.checkUsageLimitsWithRetry(index, token)
		if checkErr != nil {
			logger.Warn("异步刷新：检查使用限制失败",
				logger.Int("index", index),
				logger.Err(checkErr))
			if types.IsTokenInvalidError(checkErr) {
				tm.mutex.Lock()
				tm.markTokenInvalidUnlocked(index, "async_refresh_usage", checkErr)
				tm.removeInvalidTokensImmediatelyUnlocked("async_refresh_usage")
				tm.mutex.Unlock()
			}
			tm.refreshManager.CompleteRefresh(index, nil, checkErr)
			return
		}

		available := CalculateAvailableCount(usage)
		nextResetTime := GetNextResetTime(usage)

		// 更新缓存（短暂持锁）
		tm.mutex.Lock()
		cacheKey := fmt.Sprintf(config.TokenCacheKeyFormat, index)
		now := time.Now()
		tm.cache.tokens[cacheKey] = &CachedToken{
			Token:         token,
			Index:         index,
			UsageInfo:     usage,
			CachedAt:      now,
			LastCheckAt:   now,
			Available:     available,
			NextResetTime: nextResetTime,
		}
		// 使用限制检查成功，清除失效标记（带时间戳保护）
		// 只有当失效时间早于刷新开始时间时才清除，防止清除更新的失效标记
		if invalidTime, exists := tm.invalidated[index]; !exists || invalidTime.Before(refreshStartTime) {
			delete(tm.invalidated, index)
		} else {
			logger.Debug("检测到更新的失效标记，保留失效状态",
				logger.Int("index", index),
				logger.String("invalid_time", invalidTime.Format(time.RFC3339)),
				logger.String("refresh_start", refreshStartTime.Format(time.RFC3339)))
		}
		tm.mutex.Unlock()

		logger.Info("异步刷新token完成",
			logger.Int("index", index),
			logger.Float64("available", available))

		tm.refreshManager.CompleteRefresh(index, &token, nil)
	}()
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
			tm.markTokenInvalidUnlocked(index, "refresh_token", err)
			tm.removeInvalidTokensImmediatelyUnlocked("refresh_token")
		}

		return &RefreshResult{
			Index:   index,
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	// 检查使用限制
	var usageInfo *types.UsageLimits
	var available float64
	var nextResetTime time.Time

	if usage, checkErr := tm.checkUsageLimitsWithRetry(index, token); checkErr == nil {
		usageInfo = usage
		available = CalculateAvailableCount(usage)
		nextResetTime = GetNextResetTime(usage)

		// 使用限制检查成功，清除失效标记
		delete(tm.invalidated, index)
	} else {
		// 检查是否是 token 失效错误
		if types.IsTokenInvalidError(checkErr) {
			logger.Warn("使用限制检查检测到token失效",
				logger.Int("index", index),
				logger.String("auth_type", cfg.AuthType),
				logger.Err(checkErr))
			tm.markTokenInvalidUnlocked(index, "refresh_token_usage", checkErr)
			tm.removeInvalidTokensImmediatelyUnlocked("refresh_token_usage")

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
		Index:         index,
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
// 如果配置了代理池，将根据健康代理数量并发刷新
func (tm *TokenManager) RefreshTokens(indices []int) ([]RefreshResult, error) {
	// 1. 获取并发度
	concurrency := 1
	if tm.proxyPool != nil {
		if healthyCount := tm.proxyPool.GetHealthyProxyCount(); healthyCount > 0 {
			concurrency = healthyCount
		}
	}

	// 2. 短暂持锁获取任务列表
	type refreshTask struct {
		index int
		cfg   AuthConfig
	}

	tm.mutex.Lock()
	if len(indices) == 0 {
		indices = make([]int, 0, len(tm.configs))
		for i, cfg := range tm.configs {
			if !cfg.Disabled {
				indices = append(indices, i)
			}
		}
	}

	// 预处理：过滤无效索引和禁用账号
	tasks := make([]refreshTask, 0, len(indices))
	invalidResults := make([]RefreshResult, 0)

	for _, index := range indices {
		if index < 0 || index >= len(tm.configs) {
			invalidResults = append(invalidResults, RefreshResult{
				Index:   index,
				Success: false,
				Error:   fmt.Sprintf("索引超出范围: %d", index),
			})
			continue
		}

		cfg := tm.configs[index]
		if cfg.Disabled {
			invalidResults = append(invalidResults, RefreshResult{
				Index:   index,
				Success: false,
				Error:   "账号已禁用",
			})
			continue
		}

		tasks = append(tasks, refreshTask{index: index, cfg: cfg})
	}
	tm.mutex.Unlock()

	if len(tasks) == 0 {
		return invalidResults, nil
	}

	logger.Info("开始并发刷新token",
		logger.Int("task_count", len(tasks)),
		logger.Int("concurrency", concurrency))

	// 3. 并发刷新（不持锁）
	type taskResult struct {
		index         int
		token         types.TokenInfo
		usageInfo     *types.UsageLimits
		available     float64
		nextResetTime time.Time
		err           error
		isInvalid     bool // token 是否失效
	}

	taskChan := make(chan refreshTask, len(tasks))
	resultChan := make(chan taskResult, len(tasks))

	// 启动 workers
	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for task := range taskChan {
				result := taskResult{index: task.index}

				// 刷新 token
				token, err := tm.refreshSingleToken(task.cfg, task.index)
				if err != nil {
					result.err = err
					result.isInvalid = types.IsTokenInvalidError(err)
					resultChan <- result
					continue
				}

				result.token = token

				// 检查使用限制（带代理切换重试）
				if usage, checkErr := tm.checkUsageLimitsWithRetry(task.index, token); checkErr == nil {
					result.usageInfo = usage
					result.available = CalculateAvailableCount(usage)
					result.nextResetTime = GetNextResetTime(usage)
				} else if types.IsTokenInvalidError(checkErr) {
					result.err = checkErr
					result.isInvalid = true
					resultChan <- result
					continue
				}

				resultChan <- result
			}
		}()
	}

	// 发送任务
	for _, task := range tasks {
		taskChan <- task
	}
	close(taskChan)

	// 等待完成
	wg.Wait()
	close(resultChan)

	// 4. 收集结果并持锁更新缓存
	taskResults := make([]taskResult, 0, len(tasks))
	for result := range resultChan {
		taskResults = append(taskResults, result)
	}

	results := make([]RefreshResult, 0, len(invalidResults)+len(taskResults))
	results = append(results, invalidResults...)
	invalidDetected := false

	tm.mutex.Lock()
	for _, tr := range taskResults {
		if tr.err != nil {
			if tr.isInvalid {
				logger.Warn("刷新时检测到token失效",
					logger.Int("index", tr.index),
					logger.Err(tr.err))
				tm.markTokenInvalidUnlocked(tr.index, "refresh_tokens", tr.err)
				invalidDetected = true
			}

			results = append(results, RefreshResult{
				Index:   tr.index,
				Success: false,
				Error:   tr.err.Error(),
			})
			continue
		}

		// 清除失效标记
		delete(tm.invalidated, tr.index)

		// 更新缓存
		cacheKey := fmt.Sprintf(config.TokenCacheKeyFormat, tr.index)
		now := time.Now()
		tm.cache.tokens[cacheKey] = &CachedToken{
			Token:         tr.token,
			Index:         tr.index,
			UsageInfo:     tr.usageInfo,
			CachedAt:      now,
			LastCheckAt:   now,
			Available:     tr.available,
			NextResetTime: tr.nextResetTime,
		}

		logger.Info("成功刷新账号",
			logger.Int("index", tr.index),
			logger.Float64("available", tr.available))

		// 获取更新后的状态
		status, _ := tm.getTokenStatusUnlocked(tr.index)

		results = append(results, RefreshResult{
			Index:   tr.index,
			Success: true,
			Status:  status,
		})
	}
	tm.mutex.Unlock()

	if invalidDetected {
		tm.mutex.Lock()
		tm.removeInvalidTokensImmediatelyUnlocked("refresh_tokens")
		tm.mutex.Unlock()
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
		logger.Int("failed", len(results)-successCount),
		logger.Int("concurrency", concurrency))

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

// refreshActivePoolTokens 刷新活跃池中快要过期的 token（并发模式）
func (tm *TokenManager) refreshActivePoolTokens() {
	// 短暂持锁获取需要刷新的token列表
	tm.mutex.Lock()
	if len(tm.activePool) == 0 {
		tm.mutex.Unlock()
		return
	}

	threshold := 10 * time.Minute // 提前10分钟刷新
	type refreshTask struct {
		index int
		cfg   AuthConfig
	}
	tasks := make([]refreshTask, 0)

	for _, configIndex := range tm.activePool {
		if configIndex >= len(tm.configs) {
			continue
		}

		currentKey := tm.configOrder[configIndex]
		cached, exists := tm.cache.tokens[currentKey]
		if !exists {
			continue
		}

		if shouldSkipRefreshDueToQuota(cached, time.Now()) {
			logger.Debug("定时刷新跳过：额度耗尽且未到重置时间",
				logger.Int("config_index", configIndex),
				logger.Float64("available", cached.Available),
				logger.String("next_reset", cached.NextResetTime.Format(time.RFC3339)))
			continue
		}

		// 检查是否需要刷新（快要过期或已过期）
		timeUntilExpiry := time.Until(cached.Token.ExpiresAt)
		if timeUntilExpiry < threshold {
			cfg := tm.configs[configIndex]
			if !cfg.Disabled {
				tasks = append(tasks, refreshTask{index: configIndex, cfg: cfg})
				logger.Debug("定时刷新即将过期的token",
					logger.Int("config_index", configIndex),
					logger.Duration("time_until_expiry", timeUntilExpiry))
			}
		}
	}
	tm.mutex.Unlock()

	if len(tasks) == 0 {
		return
	}

	logger.Info("开始并发定时刷新活跃池token",
		logger.Int("task_count", len(tasks)))

	// 并发刷新（不持锁）
	type taskResult struct {
		index         int
		token         types.TokenInfo
		usageInfo     *types.UsageLimits
		available     float64
		nextResetTime time.Time
		err           error
	}

	// 动态调整并发度
	concurrency := 10 // 默认并发度
	if tm.proxyPool != nil {
		// 如果配置了代理池，根据健康代理数量调整并发度
		if healthyCount := tm.proxyPool.GetHealthyProxyCount(); healthyCount > 0 {
			concurrency = min(healthyCount, 20) // 最多20个并发
			logger.Debug("根据代理池健康数量调整并发度",
				logger.Int("healthy_proxies", healthyCount),
				logger.Int("concurrency", concurrency))
		}
	}
	concurrency = min(concurrency, len(tasks)) // 不超过任务数量

	taskChan := make(chan refreshTask, len(tasks))
	resultChan := make(chan taskResult, len(tasks))

	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for task := range taskChan {
				result := taskResult{index: task.index}

				// 刷新token
				token, err := tm.refreshSingleToken(task.cfg, task.index)
				if err != nil {
					result.err = err
					resultChan <- result
					continue
				}

				result.token = token

				// 检查使用限制（带代理切换重试）
				if usage, checkErr := tm.checkUsageLimitsWithRetry(task.index, token); checkErr == nil {
					result.usageInfo = usage
					result.available = CalculateAvailableCount(usage)
					result.nextResetTime = GetNextResetTime(usage)
				} else {
					result.err = checkErr
				}

				resultChan <- result
			}
		}()
	}

	// 发送任务
	for _, task := range tasks {
		taskChan <- task
	}
	close(taskChan)

	// 等待完成
	wg.Wait()
	close(resultChan)

	// 收集结果并持锁更新缓存
	refreshCount := 0
	invalidDetected := false
	tm.mutex.Lock()
	for result := range resultChan {
		if result.err != nil {
			logger.Warn("定时刷新失败",
				logger.Int("config_index", result.index),
				logger.Err(result.err))
			if types.IsTokenInvalidError(result.err) {
				tm.markTokenInvalidUnlocked(result.index, "periodic_refresh", result.err)
				invalidDetected = true
			}
			continue
		}

		// 清除失效标记
		delete(tm.invalidated, result.index)

		// 更新缓存
		cacheKey := tm.configOrder[result.index]
		now := time.Now()
		tm.cache.tokens[cacheKey] = &CachedToken{
			Token:         result.token,
			Index:         result.index,
			UsageInfo:     result.usageInfo,
			CachedAt:      now,
			LastCheckAt:   now,
			Available:     result.available,
			NextResetTime: result.nextResetTime,
		}

		refreshCount++
		logger.Info("定时刷新成功",
			logger.Int("config_index", result.index),
			logger.Float64("available", result.available))
	}
	tm.mutex.Unlock()

	if invalidDetected {
		tm.mutex.Lock()
		tm.removeInvalidTokensImmediatelyUnlocked("periodic_refresh")
		tm.mutex.Unlock()
	}

	if refreshCount > 0 {
		logger.Info("活跃池定时刷新完成",
			logger.Int("refreshed_count", refreshCount),
			logger.Int("total_tasks", len(tasks)),
			logger.Int("concurrency", concurrency))
	}
}

// Close 关闭 TokenManager，停止定时任务
func (tm *TokenManager) Close() {
	// 先取消context，通知所有异步goroutine退出
	tm.cancel()
	logger.Info("TokenManager开始关闭，已通知所有异步任务退出")

	tm.mutex.Lock()
	defer tm.mutex.Unlock()

	// 停止定时刷新任务
	if tm.refreshTicker != nil {
		tm.refreshTicker.Stop()
		tm.refreshTicker = nil
	}

	// refreshStop 被多个后台任务复用（定时刷新、缓存快照保存等），必须广播式停止。
	// 使用 close(channel) 作为广播信号；这里用非阻塞接收判断是否已关闭，避免重复 close panic。
	if tm.refreshStop != nil {
		select {
		case <-tm.refreshStop:
			// already closed
		default:
			close(tm.refreshStop)
		}
	}
	logger.Info("TokenManager定时任务已停止")

	// 停止代理池健康检查
	if tm.proxyPool != nil {
		tm.proxyPool.Stop()
	}

	logger.Info("TokenManager已完全关闭")
}

// GetRefreshStats 获取刷新管理器统计信息
func (tm *TokenManager) GetRefreshStats() map[string]any {
	return tm.refreshManager.GetStats()
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
	newIndex := len(tm.configs) - 1

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

	// 异步预热新账号，避免首次请求命中未缓存状态
	if tm.batchSize > 0 {
		tm.startAsyncPoolWarmupUnlocked("add_token")
	} else {
		go tm.asyncRefreshToken(newIndex)
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

	// 配置变更后清理旧缓存和低余额标记，避免沿用旧状态
	cacheKey := fmt.Sprintf(config.TokenCacheKeyFormat, index)
	delete(tm.cache.tokens, cacheKey)
	delete(tm.lowBalanceVerified, index)
	delete(tm.invalidated, index)

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

	// 更新后异步预热，避免请求路径同步刷新
	if !tm.configs[index].Disabled {
		if tm.batchSize > 0 {
			tm.startAsyncPoolWarmupUnlocked("update_token")
		} else {
			go tm.asyncRefreshToken(index)
		}
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

// ClearLowBalanceVerified 清除低余额验证记录
// 应该在每月1日调用，允许已用完的账号重新被验证
func (tm *TokenManager) ClearLowBalanceVerified() {
	tm.mutex.Lock()
	defer tm.mutex.Unlock()

	count := len(tm.lowBalanceVerified)
	tm.lowBalanceVerified = make(map[int]time.Time)

	logger.Info("清除低余额验证记录（月度重置）",
		logger.Int("cleared_count", count))
}
