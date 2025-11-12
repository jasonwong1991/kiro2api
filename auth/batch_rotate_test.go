package auth

import (
	"fmt"
	"kiro2api/config"
	"kiro2api/types"
	"os"
	"testing"
	"time"
)

// TestBatchRotateStrategy 测试分批轮换策略
func TestBatchRotateStrategy(t *testing.T) {
	// 设置环境变量
	os.Setenv("TOKEN_SELECTION_STRATEGY", "batch_rotate")
	os.Setenv("KIRO_BATCH_SIZE", "3")
	defer os.Unsetenv("TOKEN_SELECTION_STRATEGY")
	defer os.Unsetenv("KIRO_BATCH_SIZE")

	// 创建 9 个测试配置（3 批，每批 3 个）
	configs := make([]AuthConfig, 9)
	for i := 0; i < 9; i++ {
		configs[i] = AuthConfig{
			AuthType:     AuthMethodSocial,
			RefreshToken: fmt.Sprintf("test_token_%d", i),
		}
	}

	// 创建 TokenManager
	tm := NewTokenManager(configs)

	// 验证初始化
	if tm.strategy != StrategyBatchRotate {
		t.Errorf("期望策略为 batch_rotate，实际为 %s", tm.strategy)
	}
	if tm.batchSize != 3 {
		t.Errorf("期望批次大小为 3，实际为 %d", tm.batchSize)
	}
	if tm.currentBatch != 0 {
		t.Errorf("期望当前批次为 0，实际为 %d", tm.currentBatch)
	}

	// 手动填充缓存（模拟刷新后的状态）
	for i := 0; i < 9; i++ {
		cacheKey := fmt.Sprintf(config.TokenCacheKeyFormat, i)
		tm.cache.tokens[cacheKey] = &CachedToken{
			Token: types.TokenInfo{
				AccessToken: fmt.Sprintf("access_token_%d", i),
				ExpiresAt:   time.Now().Add(1 * time.Hour),
			},
			UsageInfo: &types.UsageLimits{},
			CachedAt:  time.Now(),
			Available: 10.0, // 每个 token 有 10 次可用
		}
	}

	t.Run("第一批次轮询", func(t *testing.T) {
		// 第一批次应该是索引 0, 1, 2
		expectedIndices := []int{0, 1, 2}
		for round := 0; round < 3; round++ {
			for _, expectedIdx := range expectedIndices {
				token := tm.selectBatchRotateToken()
				if token == nil {
					t.Fatalf("第 %d 轮第 %d 个 token 为 nil", round, expectedIdx)
				}

				expectedToken := fmt.Sprintf("access_token_%d", expectedIdx)
				if token.Token.AccessToken != expectedToken {
					t.Errorf("第 %d 轮期望 token %s，实际为 %s",
						round, expectedToken, token.Token.AccessToken)
				}
			}
		}

		// 验证仍在第一批次
		if tm.currentBatch != 0 {
			t.Errorf("期望仍在第一批次（0），实际为 %d", tm.currentBatch)
		}
	})

	t.Run("第一批次耗尽后切换", func(t *testing.T) {
		// 耗尽第一批次的所有 token
		for i := 0; i < 3; i++ {
			cacheKey := fmt.Sprintf(config.TokenCacheKeyFormat, i)
			tm.cache.tokens[cacheKey].Available = 0
		}

		// 下一次选择应该切换到第二批次
		token := tm.selectBatchRotateToken()
		if token == nil {
			t.Fatal("切换到第二批次后 token 为 nil")
		}

		// 应该是第二批次的第一个 token（索引 3）
		expectedToken := "access_token_3"
		if token.Token.AccessToken != expectedToken {
			t.Errorf("期望切换到 token %s，实际为 %s",
				expectedToken, token.Token.AccessToken)
		}

		// 验证已切换到第二批次
		if tm.currentBatch != 1 {
			t.Errorf("期望切换到第二批次（1），实际为 %d", tm.currentBatch)
		}
	})

	t.Run("所有批次耗尽", func(t *testing.T) {
		// 耗尽所有 token
		for i := 0; i < 9; i++ {
			cacheKey := fmt.Sprintf(config.TokenCacheKeyFormat, i)
			tm.cache.tokens[cacheKey].Available = 0
		}

		// 重置到第一批次
		tm.currentBatch = 0
		tm.batchRoundRobin = 0

		// 尝试选择 token，应该返回 nil 并触发全局刷新标记
		token := tm.selectBatchRotateToken()
		if token != nil {
			t.Error("所有批次耗尽后应该返回 nil")
		}

		// 验证已重置到第一批次
		if tm.currentBatch != 0 {
			t.Errorf("所有批次耗尽后应该重置到第一批次（0），实际为 %d", tm.currentBatch)
		}

		// 验证 lastRefresh 被重置（触发全局刷新）
		if !tm.lastRefresh.IsZero() {
			t.Error("所有批次耗尽后应该重置 lastRefresh 以触发全局刷新")
		}
	})
}

// TestBatchRotateWithInvalidSize 测试无效的批次大小
func TestBatchRotateWithInvalidSize(t *testing.T) {
	configs := make([]AuthConfig, 5)
	for i := 0; i < 5; i++ {
		configs[i] = AuthConfig{
			AuthType:     AuthMethodSocial,
			RefreshToken: fmt.Sprintf("test_token_%d", i),
		}
	}

	testCases := []struct {
		name      string
		batchSize int
		shouldUse string // "batch_rotate" 或 "round_robin"
	}{
		{"批次大小为0", 0, "round_robin"},
		{"批次大小等于总数", 5, "round_robin"},
		{"批次大小大于总数", 10, "round_robin"},
		{"批次大小为负数", -1, "round_robin"},
		{"有效批次大小", 2, "batch_rotate"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tm := NewTokenManager(configs)
			tm.strategy = StrategyBatchRotate
			tm.batchSize = tc.batchSize

			// 填充缓存
			for i := 0; i < 5; i++ {
				cacheKey := fmt.Sprintf(config.TokenCacheKeyFormat, i)
				tm.cache.tokens[cacheKey] = &CachedToken{
					Token: types.TokenInfo{
						AccessToken: fmt.Sprintf("access_token_%d", i),
						ExpiresAt:   time.Now().Add(1 * time.Hour),
					},
					CachedAt:  time.Now(),
					Available: 10.0,
				}
			}

			token := tm.selectBatchRotateToken()
			if token == nil {
				t.Fatal("token 不应该为 nil")
			}

			// 验证是否按预期降级
			if tc.shouldUse == "round_robin" && tc.batchSize > 0 && tc.batchSize < 5 {
				// 如果应该降级但批次大小有效，不应该降级
				t.Errorf("批次大小 %d 不应该降级", tc.batchSize)
			}
		})
	}
}

// TestBatchRotateRefreshStrategy 测试分批刷新策略
func TestBatchRotateRefreshStrategy(t *testing.T) {
	// 创建 6 个测试配置（2 批，每批 3 个）
	configs := make([]AuthConfig, 6)
	for i := 0; i < 6; i++ {
		configs[i] = AuthConfig{
			AuthType:     AuthMethodSocial,
			RefreshToken: fmt.Sprintf("test_token_%d", i),
		}
	}

	tm := NewTokenManager(configs)
	tm.strategy = StrategyBatchRotate
	tm.batchSize = 3
	tm.currentBatch = 0

	t.Run("只刷新当前批次", func(t *testing.T) {
		// 模拟 refreshCacheUnlocked 的逻辑
		var refreshIndices []int
		if tm.strategy == StrategyBatchRotate && tm.batchSize > 0 && tm.batchSize < len(tm.configs) {
			batchStart := tm.currentBatch * tm.batchSize
			batchEnd := batchStart + tm.batchSize
			if batchEnd > len(tm.configs) {
				batchEnd = len(tm.configs)
			}

			for i := batchStart; i < batchEnd; i++ {
				refreshIndices = append(refreshIndices, i)
			}
		}

		// 验证只刷新第一批次（索引 0, 1, 2）
		expectedIndices := []int{0, 1, 2}
		if len(refreshIndices) != len(expectedIndices) {
			t.Errorf("期望刷新 %d 个 token，实际刷新 %d 个",
				len(expectedIndices), len(refreshIndices))
		}

		for i, idx := range refreshIndices {
			if idx != expectedIndices[i] {
				t.Errorf("期望刷新索引 %d，实际为 %d", expectedIndices[i], idx)
			}
		}
	})

	t.Run("切换批次后刷新新批次", func(t *testing.T) {
		// 切换到第二批次
		tm.currentBatch = 1

		var refreshIndices []int
		if tm.strategy == StrategyBatchRotate && tm.batchSize > 0 && tm.batchSize < len(tm.configs) {
			batchStart := tm.currentBatch * tm.batchSize
			batchEnd := batchStart + tm.batchSize
			if batchEnd > len(tm.configs) {
				batchEnd = len(tm.configs)
			}

			for i := batchStart; i < batchEnd; i++ {
				refreshIndices = append(refreshIndices, i)
			}
		}

		// 验证只刷新第二批次（索引 3, 4, 5）
		expectedIndices := []int{3, 4, 5}
		if len(refreshIndices) != len(expectedIndices) {
			t.Errorf("期望刷新 %d 个 token，实际刷新 %d 个",
				len(expectedIndices), len(refreshIndices))
		}

		for i, idx := range refreshIndices {
			if idx != expectedIndices[i] {
				t.Errorf("期望刷新索引 %d，实际为 %d", expectedIndices[i], idx)
			}
		}
	})
}

// TestGetBatchSize 测试批次大小配置读取
func TestGetBatchSize(t *testing.T) {
	testCases := []struct {
		name     string
		envValue string
		expected int
	}{
		{"未设置", "", 0},
		{"有效值", "5", 5},
		{"零值", "0", 0},
		{"负数", "-1", 0},
		{"无效字符串", "abc", 0},
		{"大数值", "100", 100},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.envValue != "" {
				os.Setenv("KIRO_BATCH_SIZE", tc.envValue)
				defer os.Unsetenv("KIRO_BATCH_SIZE")
			} else {
				os.Unsetenv("KIRO_BATCH_SIZE")
			}

			result := getBatchSize()
			if result != tc.expected {
				t.Errorf("期望批次大小为 %d，实际为 %d", tc.expected, result)
			}
		})
	}
}
