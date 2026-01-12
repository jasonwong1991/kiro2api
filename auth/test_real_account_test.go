package auth

import (
	"encoding/json"
	"testing"
	"time"
)

// TestRealAccountUsageLimits 测试真实账号的使用限制 API 响应
// 注意：这个测试需要真实的网络连接和有效的账号凭据
func TestRealAccountUsageLimits(t *testing.T) {
	// 跳过此测试除非明确要求运行
	if testing.Short() {
		t.Skip("跳过真实账号测试（需要网络连接）")
	}

	// 真实 IdC 账号配置
	config := AuthConfig{
		AuthType:     AuthMethodIdC,
		RefreshToken: "aorAAAAAGls7GQtD4GD9t1E_mnw9_fTgjgk_bSLqpAOuQlwd3MFa8Vds3eSFn9-Qdw7wwVpJF5LeCZ8Ya9KXE2fAUBkc0:MGUCMQDDU7cyTFLDSoiX+p8j4wHit5Syk4lq9rcp8ZO2I7wRUv7bMZZIZCnhLQlC+H2VqCwCMCkXcb7RVj3LHRzNCtYjOkiAw/vATiQrerKf7Ix8ve2HaEdr0+MuqYJ/S0atcxZVmQ",
		ClientID:     "Z9b31nXNHMohhcLCB790W3VzLWVhc3QtMQ",
		ClientSecret: "eyJraWQiOiJrZXktMTU2NDAyODA5OSIsImFsZyI6IkhTMzg0In0.eyJzZXJpYWxpemVkIjoie1wiY2xpZW50SWRcIjp7XCJ2YWx1ZVwiOlwiWjliMzFuWE5ITW9oaGNMQ0I3OTBXM1Z6TFdWaGMzUXRNUVwifSxcImlkZW1wb3RlbnRLZXlcIjpudWxsLFwidGVuYW50SWRcIjpudWxsLFwiY2xpZW50TmFtZVwiOlwiQVdTIElERSBFeHRlbnNpb25zIGZvciBWU0NvZGVcIixcImJhY2tmaWxsVmVyc2lvblwiOm51bGwsXCJjbGllbnRUeXBlXCI6XCJQVUJMSUNcIixcInRlbXBsYXRlQXJuXCI6bnVsbCxcInRlbXBsYXRlQ29udGV4dFwiOm51bGwsXCJleHBpcmF0aW9uVGltZXN0YW1wXCI6MTc2ODc0NTk3Ny41NDgwNTk3MDAsXCJjcmVhdGVkVGltZXN0YW1wXCI6MTc2MDk2OTk3Ny41NDgwNTk3MDAsXCJ1cGRhdGVkVGltZXN0YW1wXCI6MTc2MDk2OTk3Ny41NDgwNTk3MDAsXCJjcmVhdGVkQnlcIjpudWxsLFwidXBkYXRlZEJ5XCI6bnVsbCxcInN0YXR1c1wiOm51bGwsXCJpbml0aWF0ZUxvZ2luVXJpXCI6XCJodHRwczpcL1wvdmlldy5hd3NhcHBzLmNvbVwvc3RhcnRcL1wiLFwiZW50aXRsZWRSZXNvdXJjZUlkXCI6bnVsbCxcImVudGl0bGVkUmVzb3VyY2VDb250YWluZXJJZFwiOm51bGwsXCJleHRlcm5hbElkXCI6bnVsbCxcInNvZnR3YXJlSWRcIjpudWxsLFwic2NvcGVzXCI6W3tcImZ1bGxTY29wZVwiOlwiY29kZXdoaXNwZXJlcjpjb21wbGV0aW9uc1wiLFwic3RhdHVzXCI6XCJJTklUSUFMXCIsXCJhcHBsaWNhdGlvbkFyblwiOm51bGwsXCJmcmllbmRseUlkXCI6XCJjb2Rld2hpc3BlcmVyXCIsXCJ1c2VDYXNlQWN0aW9uXCI6XCJjb21wbGV0aW9uc1wiLFwidHlwZVwiOlwiSW1tdXRhYmxlQWNjZXNzU2NvcGVcIixcInNjb3BlVHlwZVwiOlwiQUNDRVNTX1NDT1BFXCJ9LHtcImZ1bGxTY29wZVwiOlwiY29kZXdoaXNwZXJlcjphbmFseXNpc1wiLFwic3RhdHVzXCI6XCJJTklUSUFMXCIsXCJhcHBsaWNhdGlvbkFyblwiOm51bGwsXCJmcmllbmRseUlkXCI6XCJjb2Rld2hpc3BlcmVyXCIsXCJ1c2VDYXNlQWN0aW9uXCI6XCJhbmFseXNpc1wiLFwidHlwZVwiOlwiSW1tdXRhYmxlQWNjZXNzU2NvcGVcIixcInNjb3BlVHlwZVwiOlwiQUNDRVNTX1NDT1BFXCJ9LHtcImZ1bGxTY29wZVwiOlwiY29kZXdoaXNwZXJlcjpjb252ZXJzYXRpb25zXCIsXCJzdGF0dXNcIjpcIklOSVRJQUxcIixcImFwcGxpY2F0aW9uQXJuXCI6bnVsbCxcImZyaWVuZGx5SWRcIjpcImNvZGV3aGlzcGVyZXJcIixcInVzZUNhc2VBY3Rpb25cIjpcImNvbnZlcnNhdGlvbnNcIixcInR5cGVcIjpcIkltbXV0YWJsZUFjY2Vzc1Njb3BlXCIsXCJzY29wZVR5cGVcIjpcIkFDQ0VTU19TQ09QRVwifSx7XCJmdWxsU2NvcGVcIjpcImNvZGV3aGlzcGVyZXI6dHJhbnNmb3JtYXRpb25zXCIsXCJzdGF0dXNcIjpcIklOSVRJQUxcIixcImFwcGxpY2F0aW9uQXJuXCI6bnVsbCxcImZyaWVuZGx5SWRcIjpcImNvZGV3aGlzcGVyZXJcIixcInVzZUNhc2VBY3Rpb25cIjpcInRyYW5zZm9ybWF0aW9uc1wiLFwidHlwZVwiOlwiSW1tdXRhYmxlQWNjZXNzU2NvcGVcIixcInNjb3BlVHlwZVwiOlwiQUNDRVNTX1NDT1BFXCJ9LHtcImZ1bGxTY29wZVwiOlwiY29kZXdoaXNwZXJlcjp0YXNrYXNzaXN0XCIsXCJzdGF0dXNcIjpcIklOSVRJQUxcIixcImFwcGxpY2F0aW9uQXJuXCI6bnVsbCxcImZyaWVuZGx5SWRcIjpcImNvZGV3aGlzcGVyZXJcIixcInVzZUNhc2VBY3Rpb25cIjpcInRhc2thc3Npc3RcIixcInR5cGVcIjpcIkltbXV0YWJsZUFjY2Vzc1Njb3BlXCIsXCJzY29wZVR5cGVcIjpcIkFDQ0VTU19TQ09QRVwifV0sXCJhdXRoZW50aWNhdGlvbkNvbmZpZ3VyYXRpb25cIjpudWxsLFwic2hhZG93QXV0aGVudGljYXRpb25Db25maWd1cmF0aW9uXCI6bnVsbCxcImVuYWJsZWRHcmFudHNcIjp7XCJBVVRIX0NPREVcIjp7XCJ0eXBlXCI6XCJJbW11dGFibGVBdXRob3JpemF0aW9uQ29kZUdyYW50T3B0aW9uc1wiLFwicmVkaXJlY3RVcmlzXCI6W1wiaHR0cDpcL1wvMTI3LjAuMC4xXC9vYXV0aFwvY2FsbGJhY2tcIl19LFwiUkVGUkVTSF9UT0tFTlwiOntcInR5cGVcIjpcIkltbXV0YWJsZVJlZnJlc2hUb2tlbkdyYW50T3B0aW9uc1wifX0sXCJlbmZvcmNlQXV0aE5Db25maWd1cmF0aW9uXCI6bnVsbCxcIm93bmVyQWNjb3VudElkXCI6bnVsbCxcInNzb0luc3RhbmNlQWNjb3VudElkXCI6bnVsbCxcInVzZXJDb25zZW50XCI6bnVsbCxcIm5vbkludGVyYWN0aXZlU2Vzc2lvbnNFbmFibGVkXCI6bnVsbCxcImFzc29jaWF0ZWRJbnN0YW5jZUFyblwiOm51bGwsXCJpc0JhY2tmaWxsZWRcIjpmYWxzZSxcImhhc0luaXRpYWxTY29wZXNcIjp0cnVlLFwiYXJlQWxsU2NvcGVzQ29uc2VudGVkVG9cIjpmYWxzZSxcImlzRXhwaXJlZFwiOmZhbHNlLFwiZ3JvdXBTY29wZXNCeUZyaWVuZGx5SWRcIjp7XCJjb2Rld2hpc3BlcmVyXCI6W3tcImZ1bGxTY29wZVwiOlwiY29kZXdoaXNwZXJlcjpjb252ZXJzYXRpb25zXCIsXCJzdGF0dXNcIjpcIklOSVRJQUxcIixcImFwcGxpY2F0aW9uQXJuXCI6bnVsbCxcImZyaWVuZGx5SWRcIjpcImNvZGV3aGlzcGVyZXJcIixcInVzZUNhc2VBY3Rpb25cIjpcImNvbnZlcnNhdGlvbnNcIixcInR5cGVcIjpcIkltbXV0YWJsZUFjY2Vzc1Njb3BlXCIsXCJzY29wZVR5cGVcIjpcIkFDQ0VTU19TQ09QRVwifSx7XCJmdWxsU2NvcGVcIjpcImNvZGV3aGlzcGVyZXI6dGFza2Fzc2lzdFwiLFwic3RhdHVzXCI6XCJJTklUSUFMXCIsXCJhcHBsaWNhdGlvbkFyblwiOm51bGwsXCJmcmllbmRseUlkXCI6XCJjb2Rld2hpc3BlcmVyXCIsXCJ1c2VDYXNlQWN0aW9uXCI6XCJ0YXNrYXNzaXN0XCIsXCJ0eXBlXCI6XCJJbW11dGFibGVBY2Nlc3NTY29wZVwiLFwic2NvcGVUeXBlXCI6XCJBQ0NFU1NfU0NPUEVcIn0se1wiZnVsbFNjb3BlXCI6XCJjb2Rld2hpc3BlcmVyOmFuYWx5c2lzXCIsXCJzdGF0dXNcIjpcIklOSVRJQUxcIixcImFwcGxpY2F0aW9uQXJuXCI6bnVsbCxcImZyaWVuZGx5SWRcIjpcImNvZGV3aGlzcGVyZXJcIixcInVzZUNhc2VBY3Rpb25cIjpcImFuYWx5c2lzXCIsXCJ0eXBlXCI6XCJJbW11dGFibGVBY2Nlc3NTY29wZVwiLFwic2NvcGVUeXBlXCI6XCJBQ0NFU1NfU0NPUEVcIn0se1wiZnVsbFNjb3BlXCI6XCJjb2Rld2hpc3BlcmVyOmNvbXBsZXRpb25zXCIsXCJzdGF0dXNcIjpcIklOSVRJQUxcIixcImFwcGxpY2F0aW9uQXJuXCI6bnVsbCxcImZyaWVuZGx5SWRcIjpcImNvZGV3aGlzcGVyZXJcIixcInVzZUNhc2VBY3Rpb25cIjpcImNvbXBsZXRpb25zXCIsXCJ0eXBlXCI6XCJJbW11dGFibGVBY2Nlc3NTY29wZVwiLFwic2NvcGVUeXBlXCI6XCJBQ0NFU1NfU0NPUEVcIn0se1wiZnVsbFNjb3BlXCI6XCJjb2Rld2hpc3BlcmVyOnRyYW5zZm9ybWF0aW9uc1wiLFwic3RhdHVzXCI6XCJJTklUSUFMXCIsXCJhcHBsaWNhdGlvbkFyblwiOm51bGwsXCJmcmllbmRseUlkXCI6XCJjb2Rld2hpc3BlcmVyXCIsXCJ1c2VDYXNlQWN0aW9uXCI6XCJ0cmFuc2Zvcm1hdGlvbnNcIixcInR5cGVcIjpcIkltbXV0YWJsZUFjY2Vzc1Njb3BlXCIsXCJzY29wZVR5cGVcIjpcIkFDQ0VTU19TQ09QRVwifV19LFwic2hvdWxkR2V0VmFsdWVGcm9tVGVtcGxhdGVcIjpmYWxzZSxcImhhc1JlcXVlc3RlZFNjb3Blc1wiOmZhbHNlLFwiY29udGFpbnNPbmx5U3NvU2NvcGVzXCI6ZmFsc2UsXCJzc29TY29wZXNcIjpbXSxcImlzVjFCYWNrZmlsbGVkXCI6ZmFsc2UsXCJpc1YyQmFja2ZpbGxlZFwiOmZhbHNlLFwiaXNWM0JhY2tmaWxsZWRcIjpmYWxzZSxcImlzVjRCYWNrZmlsbGVkXCI6ZmFsc2V9In0.l1U2YrCN2UwShNr29_Y-Ui1cQ6o47KgYQ9uVhKJZvGVFdmmTPJwU4bSMAqvgZDN2",
	}

	// 第一步：刷新 accessToken
	t.Log("=== 第一步：刷新 accessToken ===")
	tokenInfo, err := RefreshIdCToken(config)
	if err != nil {
		t.Fatalf("刷新 IdC token 失败: %v", err)
	}

	t.Logf("✓ AccessToken 前 50 字符: %s...", tokenInfo.AccessToken[:50])
	t.Logf("✓ ExpiresAt: %s", tokenInfo.ExpiresAt.Format(time.RFC3339))
	t.Logf("✓ ExpiresIn: %d 秒", tokenInfo.ExpiresIn)

	// 第二步：获取使用限制
	t.Log("\n=== 第二步：获取使用限制 ===")
	checker := NewUsageLimitsChecker(0) // 测试用索引0
	usageLimits, err := checker.CheckUsageLimits(tokenInfo)
	if err != nil {
		t.Fatalf("获取使用限制失败: %v", err)
	}

	// 打印完整的 JSON 响应（便于分析）
	fullJSON, _ := json.MarshalIndent(usageLimits, "", "  ")
	t.Logf("完整使用限制响应:\n%s", string(fullJSON))

	// 第三步：分析重置日期
	t.Log("\n=== 第三步：分析重置日期信息 ===")

	// 顶层重置日期
	t.Logf("DaysUntilReset (顶层): %d 天", usageLimits.DaysUntilReset)

	if usageLimits.NextDateReset > 0 {
		// NextDateReset 是秒级时间戳，不需要除以 1000
		resetTime := time.Unix(int64(usageLimits.NextDateReset), 0)
		t.Logf("NextDateReset (顶层): %v (%s)", usageLimits.NextDateReset, resetTime.Format("2006-01-02 15:04:05"))
	}

	// 资源级别重置日期
	for _, breakdown := range usageLimits.UsageBreakdownList {
		t.Logf("\n资源类型: %s", breakdown.ResourceType)
		t.Logf("  - 当前使用: %.2f / %.2f", breakdown.CurrentUsageWithPrecision, breakdown.UsageLimitWithPrecision)
		t.Logf("  - 剩余额度: %.2f", breakdown.UsageLimitWithPrecision-breakdown.CurrentUsageWithPrecision)

		if breakdown.NextDateReset > 0 {
			// NextDateReset 是秒级时间戳，不需要除以 1000
			resetTime := time.Unix(int64(breakdown.NextDateReset), 0)
			t.Logf("  - 下次重置: %v (%s)", breakdown.NextDateReset, resetTime.Format("2006-01-02 15:04:05"))
			t.Logf("  - 距今天数: %.1f 天", time.Until(resetTime).Hours()/24)
		}

		// 免费试用信息
		if breakdown.FreeTrialInfo != nil {
			t.Logf("  - 免费试用状态: %s", breakdown.FreeTrialInfo.FreeTrialStatus)
			if breakdown.FreeTrialInfo.FreeTrialExpiry > 0 {
				// FreeTrialExpiry 也是秒级时间戳
				expiryTime := time.Unix(int64(breakdown.FreeTrialInfo.FreeTrialExpiry), 0)
				t.Logf("  - 试用到期: %s", expiryTime.Format("2006-01-02 15:04:05"))
			}
		}
	}

	// 第四步：用户信息
	t.Log("\n=== 第四步：用户信息 ===")
	t.Logf("用户邮箱: %s", usageLimits.UserInfo.Email)
	t.Logf("用户 ID: %s", usageLimits.UserInfo.UserID)
	t.Logf("订阅类型: %s", usageLimits.SubscriptionInfo.Type)
	t.Logf("订阅标题: %s", usageLimits.SubscriptionInfo.SubscriptionTitle)

	t.Log("\n✓ 测试完成！")
}

// TestParseResetDate 测试重置日期解析
func TestParseResetDate(t *testing.T) {
	// 示例：NextDateReset 是秒级时间戳
	// 例如 1734912000 = 2024-12-23 00:00:00 UTC

	testCases := []struct {
		name          string
		timestamp     float64
		expectedDate  string
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
			isAfterNow := resetTime.After(time.Now())

			t.Logf("时间戳: %.0f", tc.timestamp)
			t.Logf("解析为: %s", resetTime.Format("2006-01-02 15:04:05"))
			t.Logf("是否在未来: %v (期望: %v)", isAfterNow, tc.expectedAfter)

			if isAfterNow != tc.expectedAfter {
				t.Errorf("重置日期判断错误")
			}
		})
	}
}
