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
	StrategySequential   SelectionStrategy = "sequential"     // 顺序选择
	StrategyRandom       SelectionStrategy = "random"         // 随机选择
	StrategyRoundRobin   SelectionStrategy = "round_robin"   // 轮询选择
	StrategyBatchRotate  SelectionStrategy = "batch_rotate"  // 分批轮换（避免单IP频繁刷新）
)

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

	// 分批轮换策略相关字段
	batchSize         int       // 每批使用的账号数量
	currentBatch      int       // 当前批次索引
	batchStartIndex   int       // 当前批次起始索引
	batchRoundRobin   int       // 批次内轮询索引
	lastBatchRotation time.Time // 上次批次切换时间
}

// SimpleTokenCache 简化的token缓存（纯数据结构，无锁）
// 所有并发访问由 TokenManager.mutex 统一管理
type SimpleTokenCache struct {
	tokens map[string]*CachedToken
	ttl    time.Duration
}

// CachedToken 缓存的token信息
type CachedToken struct {
	Token     types.TokenInfo
	UsageInfo *types.UsageLimits
	CachedAt  time.Time
	LastUsed  time.Time
	Available float64
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

	logger.Info("TokenManager初始化",
		logger.Int("config_count", len(configs)),
		logger.Int("config_order_count", len(configOrder)),
		logger.String("selection_strategy", string(strategy)),
		logger.Int("batch_size", batchSize),
		logger.String("config_path", configPath))

	return &TokenManager{
		cache:             NewSimpleTokenCache(config.TokenCacheTTL),
		configs:           configs,
		configOrder:       configOrder,
		currentIndex:      0,
		exhausted:         make(map[string]bool),
		invalidated:       make(map[int]time.Time),
		strategy:          strategy,
		configPath:        configPath,
		batchSize:         batchSize,
		currentBatch:      0,
		batchStartIndex:   0,
		batchRoundRobin:   0,
		lastBatchRotation: time.Now(),
	}
}

// getConfigPath 获取配置文件路径
func getConfigPath() string {
	authToken := os.Getenv("KIRO_AUTH_TOKEN")
	if authToken == "" {
		return ""
	}

	// 如果以 [ 开头，说明是 JSON 字符串，不是文件路径
	if len(authToken) > 0 && authToken[0] == '[' {
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
	case StrategySequential, StrategyRandom, StrategyRoundRobin, StrategyBatchRotate:
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

// getBestToken 获取最优可用token
// 统一锁管理：所有操作在单一锁保护下完成，避免多次加锁/解锁
func (tm *TokenManager) getBestToken() (types.TokenInfo, error) {
	tm.mutex.Lock()
	defer tm.mutex.Unlock()

	// 检查是否需要刷新缓存（在锁内）
	if time.Since(tm.lastRefresh) > config.TokenCacheTTL {
		if err := tm.refreshCacheUnlocked(); err != nil {
			logger.Warn("刷新token缓存失败", logger.Err(err))
		}
	}

	// 选择最优token（内部方法，不加锁）
	bestToken := tm.selectBestTokenUnlocked()
	if bestToken == nil {
		return types.TokenInfo{}, fmt.Errorf("没有可用的token")
	}

	// 更新最后使用时间（在锁内，安全）
	bestToken.LastUsed = time.Now()
	if bestToken.Available > 0 {
		bestToken.Available--
	}

	return bestToken.Token, nil
}

// GetBestTokenWithUsage 获取最优可用token（包含使用信息）
// 统一锁管理：所有操作在单一锁保护下完成
func (tm *TokenManager) GetBestTokenWithUsage() (*types.TokenWithUsage, error) {
	tm.mutex.Lock()
	defer tm.mutex.Unlock()

	// 检查是否需要刷新缓存（在锁内）
	if time.Since(tm.lastRefresh) > config.TokenCacheTTL {
		if err := tm.refreshCacheUnlocked(); err != nil {
			logger.Warn("刷新token缓存失败", logger.Err(err))
		}
	}

	// 选择最优token（内部方法，不加锁）
	bestToken := tm.selectBestTokenUnlocked()
	if bestToken == nil {
		return nil, fmt.Errorf("没有可用的token")
	}

	// 更新最后使用时间（在锁内，安全）
	bestToken.LastUsed = time.Now()
	available := bestToken.Available
	if bestToken.Available > 0 {
		bestToken.Available--
	}

	// 构造 TokenWithUsage
	tokenWithUsage := &types.TokenWithUsage{
		TokenInfo:       bestToken.Token,
		UsageLimits:     bestToken.UsageInfo,
		AvailableCount:  available, // 使用精确计算的可用次数
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
	case StrategyBatchRotate:
		return tm.selectBatchRotateToken()
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
			if time.Since(cached.CachedAt) <= tm.cache.ttl && cached.IsUsable() {
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
			// 检查token是否过期
			if time.Since(cached.CachedAt) > tm.cache.ttl {
				tm.exhausted[currentKey] = true
				tm.currentIndex = (tm.currentIndex + 1) % len(tm.configOrder)
				continue
			}

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
func (tm *TokenManager) selectRoundRobinToken() *CachedToken {
	if len(tm.configOrder) == 0 {
		return nil
	}

	startIndex := tm.currentIndex
	for attempts := 0; attempts < len(tm.configOrder); attempts++ {
		currentKey := tm.configOrder[tm.currentIndex]

		if cached, exists := tm.cache.tokens[currentKey]; exists {
			if time.Since(cached.CachedAt) <= tm.cache.ttl && cached.IsUsable() {
				logger.Debug("轮询策略选择token",
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

	logger.Warn("所有token都不可用（轮询策略）",
		logger.Int("total_count", len(tm.configOrder)))

	return nil
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
			if time.Since(cached.CachedAt) <= tm.cache.ttl && cached.IsUsable() {
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

// selectBatchRotateToken 分批轮换策略
// 核心逻辑：
// 1. 将所有 token 分成多个批次，每批 batchSize 个
// 2. 当前批次内使用 round_robin 策略轮流使用
// 3. 当前批次所有 token 都不可用时，自动切换到下一批次
// 4. 只刷新当前批次的 token，其他批次不刷新（避免单IP频繁刷新）
// 5. 所有批次都耗尽后，重置到第一批次并触发全局刷新
func (tm *TokenManager) selectBatchRotateToken() *CachedToken {
	if len(tm.configOrder) == 0 {
		return nil
	}

	// 如果 batchSize 为 0 或大于等于总数，降级为普通轮询策略
	if tm.batchSize <= 0 || tm.batchSize >= len(tm.configOrder) {
		logger.Debug("批次大小无效，降级为轮询策略",
			logger.Int("batch_size", tm.batchSize),
			logger.Int("total_count", len(tm.configOrder)))
		return tm.selectRoundRobinToken()
	}

	// 计算总批次数
	totalBatches := (len(tm.configOrder) + tm.batchSize - 1) / tm.batchSize

	// 尝试从当前批次选择可用 token
	for batchAttempts := 0; batchAttempts < totalBatches; batchAttempts++ {
		// 计算当前批次的起始和结束索引
		batchStart := tm.currentBatch * tm.batchSize
		batchEnd := batchStart + tm.batchSize
		if batchEnd > len(tm.configOrder) {
			batchEnd = len(tm.configOrder)
		}

		logger.Debug("尝试从当前批次选择token",
			logger.Int("current_batch", tm.currentBatch),
			logger.Int("batch_start", batchStart),
			logger.Int("batch_end", batchEnd),
			logger.Int("batch_round_robin", tm.batchRoundRobin))

		// 在当前批次内轮询查找可用 token
		batchSize := batchEnd - batchStart
		for tokenAttempts := 0; tokenAttempts < batchSize; tokenAttempts++ {
			// 计算当前要检查的 token 在批次内的相对索引
			relativeIndex := tm.batchRoundRobin % batchSize
			// 计算在 configOrder 中的绝对索引
			absoluteIndex := batchStart + relativeIndex
			currentKey := tm.configOrder[absoluteIndex]

			// 移动到下一个位置（为下次调用准备）
			tm.batchRoundRobin = (tm.batchRoundRobin + 1) % batchSize

			// 检查 token 是否存在且可用
			if cached, exists := tm.cache.tokens[currentKey]; exists {
				if time.Since(cached.CachedAt) <= tm.cache.ttl && cached.IsUsable() {
					logger.Debug("分批轮换策略选择token",
						logger.String("selected_key", currentKey),
						logger.Int("batch", tm.currentBatch),
						logger.Int("absolute_index", absoluteIndex),
						logger.Float64("available_count", cached.Available))
					return cached
				}
			}

			logger.Debug("token不可用，尝试批次内下一个",
				logger.String("skipped_key", currentKey),
				logger.Int("absolute_index", absoluteIndex))
		}

		// 当前批次所有 token 都不可用，切换到下一批次
		tm.currentBatch = (tm.currentBatch + 1) % totalBatches
		tm.batchRoundRobin = 0 // 重置批次内轮询索引
		tm.lastBatchRotation = time.Now()

		logger.Info("当前批次耗尽，切换到下一批次",
			logger.Int("new_batch", tm.currentBatch),
			logger.Int("total_batches", totalBatches),
			logger.String("rotation_time", tm.lastBatchRotation.Format(time.RFC3339)))

		// 如果回到第一批次，说明所有批次都耗尽了
		if tm.currentBatch == 0 {
			logger.Warn("所有批次都已耗尽，需要全局刷新",
				logger.Int("total_batches", totalBatches),
				logger.Int("total_tokens", len(tm.configOrder)))
			// 触发全局刷新（在下次 getBestToken 时会自动刷新）
			tm.lastRefresh = time.Time{} // 重置刷新时间，强制下次刷新
			return nil
		}
	}

	// 所有批次都尝试过了，仍然没有可用 token
	logger.Warn("所有批次都不可用（分批轮换策略）",
		logger.Int("total_batches", totalBatches),
		logger.Int("total_tokens", len(tm.configOrder)))

	return nil
}

// refreshCacheUnlocked 刷新token缓存
// 内部方法：调用者必须持有 tm.mutex
// 分批轮换策略优化：只刷新当前批次的 token，避免单 IP 频繁刷新
func (tm *TokenManager) refreshCacheUnlocked() error {
	logger.Debug("开始刷新token缓存")

	// 确定需要刷新的索引范围
	var refreshIndices []int
	if tm.strategy == StrategyBatchRotate && tm.batchSize > 0 && tm.batchSize < len(tm.configs) {
		// 分批轮换策略：只刷新当前批次
		batchStart := tm.currentBatch * tm.batchSize
		batchEnd := batchStart + tm.batchSize
		if batchEnd > len(tm.configs) {
			batchEnd = len(tm.configs)
		}

		for i := batchStart; i < batchEnd; i++ {
			refreshIndices = append(refreshIndices, i)
		}

		logger.Info("分批刷新策略：只刷新当前批次",
			logger.Int("current_batch", tm.currentBatch),
			logger.Int("batch_start", batchStart),
			logger.Int("batch_end", batchEnd),
			logger.Int("refresh_count", len(refreshIndices)))
	} else {
		// 其他策略：刷新所有 token
		for i := range tm.configs {
			refreshIndices = append(refreshIndices, i)
		}

		logger.Debug("全局刷新策略：刷新所有token",
			logger.Int("refresh_count", len(refreshIndices)))
	}

	// 刷新指定索引的 token
	for _, i := range refreshIndices {
		cfg := tm.configs[i]
		if cfg.Disabled {
			continue
		}

		// 刷新token
		token, err := tm.refreshSingleToken(cfg)
		if err != nil {
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

		checker := NewUsageLimitsChecker()
		if usage, checkErr := checker.CheckUsageLimits(token); checkErr == nil {
			usageInfo = usage
			available = CalculateAvailableCount(usage)
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
		cacheKey := fmt.Sprintf(config.TokenCacheKeyFormat, i)
		tm.cache.tokens[cacheKey] = &CachedToken{
			Token:     token,
			UsageInfo: usageInfo,
			CachedAt:  time.Now(),
			Available: available,
		}

		logger.Debug("token缓存更新",
			logger.String("cache_key", cacheKey),
			logger.Float64("available", available))
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

	// 检查可用次数
	return ct.Available > 0
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
	Index         int                `json:"index"`
	AuthType      string             `json:"auth_type"`
	RefreshToken  string             `json:"refresh_token_preview"` // 只显示前后各4位
	Disabled      bool               `json:"disabled"`
	IsInvalid     bool               `json:"is_invalid"`
	InvalidatedAt *time.Time         `json:"invalidated_at,omitempty"`
	Available     float64            `json:"available"`
	UsageInfo     *types.UsageLimits `json:"usage_info,omitempty"`
	LastUsed      *time.Time         `json:"last_used,omitempty"`
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
			status.Available = cached.Available
			status.UsageInfo = cached.UsageInfo
			if !cached.LastUsed.IsZero() {
				status.LastUsed = &cached.LastUsed
			}
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
		status.Available = cached.Available
		status.UsageInfo = cached.UsageInfo
		if !cached.LastUsed.IsZero() {
			status.LastUsed = &cached.LastUsed
		}
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

	// 同步配置文件（如果使用文件配置）
	if tm.configPath != "" {
		if err := SaveConfigToFile(tm.configs, tm.configPath); err != nil {
			logger.Warn("同步配置文件失败", logger.Err(err))
			// 不返回错误，因为内存中的删除已经成功
		}
	}

	return nil
}

// RemoveInvalidTokens 批量删除所有失效的 token
func (tm *TokenManager) RemoveInvalidTokens() (int, error) {
	tm.mutex.Lock()
	defer tm.mutex.Unlock()

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

	logger.Info("批量删除失效token",
		logger.Int("removed_count", removedCount),
		logger.Int("remaining_count", len(tm.configs)))

	// 同步配置文件（如果使用文件配置）
	if tm.configPath != "" {
		if err := SaveConfigToFile(tm.configs, tm.configPath); err != nil {
			logger.Warn("同步配置文件失败", logger.Err(err))
			// 不返回错误，因为内存中的删除已经成功
		}
	}

	return removedCount, nil
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
