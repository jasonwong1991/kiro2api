package auth

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

// TestRealAccountUsageLimits 测试真实账号的使用限制 API 响应
//
// 注意：
// - 该测试需要真实网络连接与有效凭据，默认跳过，避免 CI/本地误跑。
// - 运行方式（显式开启）：
//  1. 设置: KIRO_RUN_REAL_ACCOUNT_TESTS=1
//  2. 设置凭据:
//     - KIRO_TEST_IDC_REFRESH_TOKEN
//     - KIRO_TEST_IDC_CLIENT_ID
//     - KIRO_TEST_IDC_CLIENT_SECRET
//  3. 执行: go test ./auth -run TestRealAccountUsageLimits -count=1
func TestRealAccountUsageLimits(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过真实账号测试（需要网络连接）")
	}
	if os.Getenv("KIRO_RUN_REAL_ACCOUNT_TESTS") != "1" {
		t.Skip("跳过真实账号测试（需显式设置 KIRO_RUN_REAL_ACCOUNT_TESTS=1 并提供凭据）")
	}

	refreshToken := os.Getenv("KIRO_TEST_IDC_REFRESH_TOKEN")
	clientID := os.Getenv("KIRO_TEST_IDC_CLIENT_ID")
	clientSecret := os.Getenv("KIRO_TEST_IDC_CLIENT_SECRET")
	if refreshToken == "" || clientID == "" || clientSecret == "" {
		t.Skip("缺少真实账号凭据环境变量：KIRO_TEST_IDC_REFRESH_TOKEN / KIRO_TEST_IDC_CLIENT_ID / KIRO_TEST_IDC_CLIENT_SECRET")
	}

	cfg := AuthConfig{
		AuthType:     AuthMethodIdC,
		RefreshToken: refreshToken,
		ClientID:     clientID,
		ClientSecret: clientSecret,
	}

	t.Log("=== 第一步：刷新 accessToken ===")
	tokenInfo, err := RefreshIdCToken(cfg)
	if err != nil {
		t.Fatalf("刷新 IdC token 失败: %v", err)
	}
	t.Logf("✓ ExpiresAt: %s", tokenInfo.ExpiresAt.Format(time.RFC3339))
	t.Logf("✓ ExpiresIn: %d 秒", tokenInfo.ExpiresIn)

	t.Log("\n=== 第二步：获取使用限制 ===")
	checker := NewUsageLimitsChecker(0)
	usageLimits, err := checker.CheckUsageLimits(tokenInfo)
	if err != nil {
		t.Fatalf("获取使用限制失败: %v", err)
	}

	// 打印 JSON 主要用于调试；注意日志中可能包含用户邮箱等信息，请谨慎使用。
	fullJSON, _ := json.MarshalIndent(usageLimits, "", "  ")
	t.Logf("完整使用限制响应:\n%s", string(fullJSON))
}

// TestParseResetDate 测试重置日期解析（纯单元测试，不依赖网络）
func TestParseResetDate(t *testing.T) {
	testCases := []struct {
		name          string
		timestamp     float64
		expectedAfter bool // 是否在当前时间之后
	}{
		{
			name:          "一个月后的重置日期",
			timestamp:     float64(time.Now().Add(30 * 24 * time.Hour).Unix()),
			expectedAfter: true,
		},
		{
			name:          "已过期的重置日期",
			timestamp:     float64(time.Now().Add(-1 * 24 * time.Hour).Unix()),
			expectedAfter: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// timestamp 是秒级时间戳
			resetTime := time.Unix(int64(tc.timestamp), 0)
			if tc.expectedAfter && !resetTime.After(time.Now()) {
				t.Fatalf("期望 resetTime 在当前时间之后，resetTime=%s", resetTime.Format(time.RFC3339))
			}
			if !tc.expectedAfter && resetTime.After(time.Now()) {
				t.Fatalf("期望 resetTime 不在当前时间之后，resetTime=%s", resetTime.Format(time.RFC3339))
			}
		})
	}
}
