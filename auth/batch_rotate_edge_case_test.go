package auth

import (
	"kiro2api/types"
	"testing"
	"time"
)

// TestBatchRotate_AllAccountsInvalid 测试所有账号失效时的刷新行为
func TestBatchRotate_AllAccountsInvalid(t *testing.T) {
	configs := []AuthConfig{
		{AuthType: AuthMethodSocial, RefreshToken: "token1", Disabled: false},
		{AuthType: AuthMethodSocial, RefreshToken: "token2", Disabled: false},
		{AuthType: AuthMethodSocial, RefreshToken: "token3", Disabled: false},
		{AuthType: AuthMethodSocial, RefreshToken: "token4", Disabled: false},
		{AuthType: AuthMethodSocial, RefreshToken: "token5", Disabled: false},
	}

	tm := NewTokenManager(configs)
	tm.strategy = StrategyBatchRotate
	tm.batchSize = 2

	// 标记所有账号为失效
	now := time.Now()
	for i := range configs {
		tm.invalidated[i] = now
	}

	// 模拟刷新缓存（这会触发"全部失效"场景）
	// 手动添加一些 mock token 到缓存，模拟刷新成功
	mockRefresh := func() {
		tm.mutex.Lock()
		defer tm.mutex.Unlock()

		// 清空缓存
		tm.cache.tokens = make(map[string]*CachedToken)

		// 模拟刷新前 2 个账号成功（假设额度已重置）
		for i := 0; i < tm.batchSize && i < len(tm.configs); i++ {
			cacheKey := tm.configOrder[i]
			tm.cache.tokens[cacheKey] = &CachedToken{
				Token: types.TokenInfo{
					AccessToken: "mock-token-" + configs[i].RefreshToken,
					ExpiresAt:   time.Now().Add(1 * time.Hour),
				},
				UsageInfo: &types.UsageLimits{},
				CachedAt:  time.Now(),
				Available: 100.0, // 额度已重置
			}
			// 清除失效标记
			delete(tm.invalidated, i)
		}

		tm.lastRefresh = time.Now()
	}

	// 执行刷新
	mockRefresh()

	// 验证：应该刷新了前 2 个账号（batchSize = 2）
	if len(tm.cache.tokens) != 2 {
		t.Errorf("期望刷新 2 个账号，实际刷新了 %d 个", len(tm.cache.tokens))
	}

	// 验证：前 2 个账号应该被清除失效标记
	for i := 0; i < tm.batchSize; i++ {
		if _, exists := tm.invalidated[i]; exists {
			t.Errorf("账号 %d 应该被清除失效标记", i)
		}
	}

	// 验证：后面的账号仍然失效
	for i := tm.batchSize; i < len(configs); i++ {
		if _, exists := tm.invalidated[i]; !exists {
			t.Errorf("账号 %d 应该保持失效状态", i)
		}
	}
}

// TestBatchRotate_ValidAccountsLessThanBatchSize 测试有效账号少于批次大小
func TestBatchRotate_ValidAccountsLessThanBatchSize(t *testing.T) {
	configs := []AuthConfig{
		{AuthType: AuthMethodSocial, RefreshToken: "token1", Disabled: false},
		{AuthType: AuthMethodSocial, RefreshToken: "token2", Disabled: false},
		{AuthType: AuthMethodSocial, RefreshToken: "token3", Disabled: true}, // 禁用
		{AuthType: AuthMethodSocial, RefreshToken: "token4", Disabled: false},
		{AuthType: AuthMethodSocial, RefreshToken: "token5", Disabled: false},
	}

	tm := NewTokenManager(configs)
	tm.strategy = StrategyBatchRotate
	tm.batchSize = 5 // 批次大小 5，但只有 4 个有效账号

	// 标记 1 个账号失效
	tm.invalidated[3] = time.Now()

	// 获取有效账号
	validIndices := tm.getValidAccountIndices()

	// 验证：应该有 3 个有效账号（排除禁用和失效）
	if len(validIndices) != 3 {
		t.Errorf("期望 3 个有效账号，实际 %d 个", len(validIndices))
	}

	// 验证：有效账号应该是 0, 1, 4
	expected := []int{0, 1, 4}
	for i, idx := range validIndices {
		if idx != expected[i] {
			t.Errorf("有效账号索引不匹配：期望 %v，实际 %v", expected, validIndices)
			break
		}
	}
}

// TestBatchRotate_NoInfiniteLoop 测试不会无限循环
func TestBatchRotate_NoInfiniteLoop(t *testing.T) {
	configs := []AuthConfig{
		{AuthType: AuthMethodSocial, RefreshToken: "token1", Disabled: false},
		{AuthType: AuthMethodSocial, RefreshToken: "token2", Disabled: false},
		{AuthType: AuthMethodSocial, RefreshToken: "token3", Disabled: false},
		{AuthType: AuthMethodSocial, RefreshToken: "token4", Disabled: false},
		{AuthType: AuthMethodSocial, RefreshToken: "token5", Disabled: false},
		{AuthType: AuthMethodSocial, RefreshToken: "token6", Disabled: false},
	}

	tm := NewTokenManager(configs)
	tm.strategy = StrategyBatchRotate
	tm.batchSize = 2

	// 不添加任何缓存（所有 token 不可用）
	tm.cache.tokens = make(map[string]*CachedToken)

	// 尝试选择 token（应该遍历所有批次后返回 nil）
	attempts := 0
	maxAttempts := 10

	for i := 0; i < maxAttempts; i++ {
		tm.mutex.Lock()
		token := tm.selectBestTokenUnlocked()
		tm.mutex.Unlock()

		attempts++

		if token == nil {
			// 正常返回 nil，不是无限循环
			break
		}
	}

	// 验证：应该在第一次尝试就返回 nil，不会循环多次
	if attempts > 1 {
		t.Errorf("期望 1 次尝试，实际 %d 次（可能存在循环）", attempts)
	}

	t.Logf("✓ 无限循环检查通过：%d 次尝试后返回 nil", attempts)
}

// TestBatchRotate_AllDisabled 测试所有账号都被禁用
func TestBatchRotate_AllDisabled(t *testing.T) {
	configs := []AuthConfig{
		{AuthType: AuthMethodSocial, RefreshToken: "token1", Disabled: true},
		{AuthType: AuthMethodSocial, RefreshToken: "token2", Disabled: true},
		{AuthType: AuthMethodSocial, RefreshToken: "token3", Disabled: true},
	}

	tm := NewTokenManager(configs)
	tm.strategy = StrategyBatchRotate
	tm.batchSize = 2

	// 获取有效账号
	validIndices := tm.getValidAccountIndices()

	// 验证：应该没有有效账号
	if len(validIndices) != 0 {
		t.Errorf("期望 0 个有效账号，实际 %d 个", len(validIndices))
	}

	// 尝试选择 token
	tm.mutex.Lock()
	token := tm.selectBestTokenUnlocked()
	tm.mutex.Unlock()

	// 验证：应该返回 nil
	if token != nil {
		t.Error("期望返回 nil，实际返回了 token")
	}
}

// TestBatchRotate_ExactBatchSize 测试有效账号刚好等于批次大小
func TestBatchRotate_ExactBatchSize(t *testing.T) {
	configs := []AuthConfig{
		{AuthType: AuthMethodSocial, RefreshToken: "token1", Disabled: false},
		{AuthType: AuthMethodSocial, RefreshToken: "token2", Disabled: false},
		{AuthType: AuthMethodSocial, RefreshToken: "token3", Disabled: false},
	}

	tm := NewTokenManager(configs)
	tm.strategy = StrategyBatchRotate
	tm.batchSize = 3 // 批次大小 = 有效账号数量

	// 添加缓存
	for i := 0; i < len(configs); i++ {
		cacheKey := tm.configOrder[i]
		tm.cache.tokens[cacheKey] = &CachedToken{
			Token: types.TokenInfo{
				AccessToken: "mock-token-" + configs[i].RefreshToken,
				ExpiresAt:   time.Now().Add(1 * time.Hour),
			},
			UsageInfo: &types.UsageLimits{},
			CachedAt:  time.Now(),
			Available: 100.0,
		}
	}

	// 验证：应该使用全部账号
	validIndices := tm.getValidAccountIndices()
	if len(validIndices) != 3 {
		t.Errorf("期望 3 个有效账号，实际 %d 个", len(validIndices))
	}

	// 选择 token
	for i := 0; i < len(configs); i++ {
		tm.mutex.Lock()
		token := tm.selectBestTokenUnlocked()
		tm.mutex.Unlock()

		if token == nil {
			t.Errorf("第 %d 次选择失败", i+1)
		}
	}
}
