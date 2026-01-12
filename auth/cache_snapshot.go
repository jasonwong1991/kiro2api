package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"kiro2api/logger"
	"kiro2api/types"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const (
	// DefaultCacheSnapshotPath 默认缓存快照路径
	DefaultCacheSnapshotPath = "cache_snapshot.json"
	// CacheSnapshotInterval 缓存快照保存间隔
	CacheSnapshotInterval = 10 * time.Second
)

// CacheSnapshot 缓存快照结构
type CacheSnapshot struct {
	Version       int                       `json:"version"`
	ConfigHash    string                    `json:"config_hash"`
	SavedAt       time.Time                 `json:"saved_at"`
	Tokens        map[string]*CachedToken   `json:"tokens"`
	Invalidated   map[int]time.Time         `json:"invalidated"`
	ActivePool    []int                     `json:"active_pool"`
	PoolRoundRobin int                      `json:"pool_round_robin"`
}

// CachedTokenSnapshot 用于序列化的缓存 token 结构
type CachedTokenSnapshot struct {
	Token         types.TokenInfo    `json:"token"`
	UsageInfo     *types.UsageLimits `json:"usage_info,omitempty"`
	CachedAt      time.Time          `json:"cached_at"`
	LastCheckAt   time.Time          `json:"last_check_at"`
	LastUsed      time.Time          `json:"last_used,omitempty"`
	Available     float64            `json:"available"`
	NextResetTime time.Time          `json:"next_reset_time,omitempty"`
}

// generateConfigHash 生成配置哈希（用于检测配置变更）
func generateConfigHash(configs []AuthConfig) string {
	// 按 RefreshToken 排序以确保一致性
	sortedConfigs := make([]AuthConfig, len(configs))
	copy(sortedConfigs, configs)
	sort.Slice(sortedConfigs, func(i, j int) bool {
		return sortedConfigs[i].RefreshToken < sortedConfigs[j].RefreshToken
	})

	// 只使用 RefreshToken 计算哈希（忽略其他可变字段）
	var tokens []string
	for _, cfg := range sortedConfigs {
		if !cfg.Disabled {
			tokens = append(tokens, cfg.RefreshToken)
		}
	}

	data, _ := json.Marshal(tokens)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:8]) // 只取前8字节
}

// SaveCacheSnapshot 保存缓存快照
func (tm *TokenManager) SaveCacheSnapshot() error {
	tm.mutex.RLock()
	snapshot := CacheSnapshot{
		Version:        1,
		ConfigHash:     generateConfigHash(tm.configs),
		SavedAt:        time.Now(),
		Tokens:         make(map[string]*CachedToken),
		Invalidated:    make(map[int]time.Time),
		ActivePool:     make([]int, len(tm.activePool)),
		PoolRoundRobin: tm.poolRoundRobin,
	}

	// 复制 tokens
	for k, v := range tm.cache.tokens {
		snapshot.Tokens[k] = &CachedToken{
			Token:         v.Token,
			UsageInfo:     v.UsageInfo,
			CachedAt:      v.CachedAt,
			LastCheckAt:   v.LastCheckAt,
			LastUsed:      v.LastUsed,
			Available:     v.Available,
			NextResetTime: v.NextResetTime,
		}
	}

	// 复制 invalidated
	for k, v := range tm.invalidated {
		snapshot.Invalidated[k] = v
	}

	// 复制 activePool
	copy(snapshot.ActivePool, tm.activePool)
	tm.mutex.RUnlock()

	// 序列化
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}

	// 获取快照路径
	snapshotPath := getCacheSnapshotPath()

	// 确保目录存在
	if dir := filepath.Dir(snapshotPath); dir != "." {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
	}

	// 写入文件
	return os.WriteFile(snapshotPath, data, 0600)
}

// LoadCacheSnapshot 加载缓存快照
// 返回值：snapshot, configChanged, error
func (tm *TokenManager) LoadCacheSnapshot() (*CacheSnapshot, bool, error) {
	snapshotPath := getCacheSnapshotPath()

	data, err := os.ReadFile(snapshotPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil // 文件不存在，不是错误
		}
		return nil, false, err
	}

	var snapshot CacheSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, false, err
	}

	// 检查配置是否变更
	tm.mutex.RLock()
	currentHash := generateConfigHash(tm.configs)
	tm.mutex.RUnlock()

	configChanged := snapshot.ConfigHash != currentHash

	return &snapshot, configChanged, nil
}

// RestoreCacheSnapshot 恢复缓存快照
func (tm *TokenManager) RestoreCacheSnapshot(snapshot *CacheSnapshot) {
	tm.mutex.Lock()
	defer tm.mutex.Unlock()

	// 恢复 tokens
	for k, v := range snapshot.Tokens {
		tm.cache.tokens[k] = v
	}

	// 恢复 invalidated
	for k, v := range snapshot.Invalidated {
		tm.invalidated[k] = v
	}

	// 恢复 activePool
	tm.activePool = make([]int, len(snapshot.ActivePool))
	copy(tm.activePool, snapshot.ActivePool)
	tm.poolRoundRobin = snapshot.PoolRoundRobin

	logger.Info("缓存快照恢复完成",
		logger.Int("token_count", len(snapshot.Tokens)),
		logger.Int("invalidated_count", len(snapshot.Invalidated)),
		logger.Int("active_pool_size", len(snapshot.ActivePool)),
		logger.String("saved_at", snapshot.SavedAt.Format(time.RFC3339)))
}

// startCacheSnapshotSaver 启动缓存快照定时保存
func (tm *TokenManager) startCacheSnapshotSaver() {
	ticker := time.NewTicker(CacheSnapshotInterval)
	go func() {
		for {
			select {
			case <-ticker.C:
				if err := tm.SaveCacheSnapshot(); err != nil {
					logger.Warn("保存缓存快照失败", logger.Err(err))
				} else {
					logger.Debug("缓存快照已保存")
				}
			case <-tm.refreshStop:
				ticker.Stop()
				// 退出前保存一次
				if err := tm.SaveCacheSnapshot(); err != nil {
					logger.Warn("退出时保存缓存快照失败", logger.Err(err))
				}
				return
			}
		}
	}()
	logger.Info("启动缓存快照定时保存", logger.Duration("interval", CacheSnapshotInterval))
}

// getCacheSnapshotPath 获取缓存快照路径
func getCacheSnapshotPath() string {
	if path := os.Getenv("KIRO_CACHE_SNAPSHOT_PATH"); path != "" {
		return path
	}
	return DefaultCacheSnapshotPath
}

// TryRestoreCache 尝试恢复缓存（启动时调用）
// 如果配置变更则返回 false，需要重新刷新
func (tm *TokenManager) TryRestoreCache() bool {
	snapshot, configChanged, err := tm.LoadCacheSnapshot()
	if err != nil {
		logger.Warn("加载缓存快照失败", logger.Err(err))
		return false
	}

	if snapshot == nil {
		logger.Info("未找到缓存快照，将从头开始刷新")
		return false
	}

	if configChanged {
		logger.Info("检测到配置变更，将从头开始刷新",
			logger.String("snapshot_hash", snapshot.ConfigHash))
		return false
	}

	// 检查快照是否过期（超过1小时视为过期）
	if time.Since(snapshot.SavedAt) > time.Hour {
		logger.Info("缓存快照已过期，将从头开始刷新",
			logger.String("saved_at", snapshot.SavedAt.Format(time.RFC3339)),
			logger.Duration("age", time.Since(snapshot.SavedAt)))
		return false
	}

	tm.RestoreCacheSnapshot(snapshot)
	return true
}
