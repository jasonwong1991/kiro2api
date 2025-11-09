#!/bin/bash

# 设备指纹演示脚本
# 用于验证不同账号生成不同的设备指纹

echo "=================================="
echo "设备指纹生成演示"
echo "=================================="
echo ""

# 检查 go 是否安装
if ! command -v go &> /dev/null; then
    echo "❌ Go 未安装，无法运行演示"
    echo "请先安装 Go: https://golang.org/dl/"
    exit 1
fi

# 创建临时测试文件
cat > /tmp/fingerprint_demo.go << 'EOF'
package main

import (
	"fmt"
	"kiro2api/utils"
)

func main() {
	// 模拟3个不同的账号
	tokens := []string{
		"refresh-token-account-1-abc123",
		"refresh-token-account-2-def456",
		"refresh-token-account-3-ghi789",
	}

	fmt.Println("🔐 演示：每个账号生成唯一的设备指纹\n")

	for i, token := range tokens {
		fmt.Printf("账号 %d (%s...)\n", i+1, token[:20])
		fmt.Println(strings.Repeat("-", 80))

		// 生成主请求指纹
		fp := utils.GenerateFingerprint(token)
		fmt.Printf("  User-Agent:      %s\n", truncate(fp.UserAgent, 70))
		fmt.Printf("  Device Hash:     %s\n", fp.DeviceHash)
		fmt.Printf("  OS Version:      darwin#%s\n", fp.OSVersion)
		fmt.Printf("  Node Version:    %s\n", fp.NodeVersion)
		fmt.Printf("  SDK Version:     %s\n", fp.SDKVersion)
		fmt.Printf("  Agent Mode:      %s\n", fp.KiroAgentMode)
		fmt.Printf("  IDE Version:     %s\n", fp.IDEVersion)

		// 验证稳定性
		fp2 := utils.GenerateFingerprint(token)
		if fp.DeviceHash == fp2.DeviceHash {
			fmt.Printf("  ✅ 指纹稳定性：同一账号多次生成结果相同\n")
		} else {
			fmt.Printf("  ❌ 指纹稳定性：同一账号多次生成结果不同（错误！）\n")
		}

		fmt.Println()
	}

	// 验证唯一性
	fmt.Println("🎯 验证：不同账号的指纹是否唯一\n")
	fp1 := utils.GenerateFingerprint(tokens[0])
	fp2 := utils.GenerateFingerprint(tokens[1])
	fp3 := utils.GenerateFingerprint(tokens[2])

	if fp1.DeviceHash != fp2.DeviceHash && fp2.DeviceHash != fp3.DeviceHash && fp1.DeviceHash != fp3.DeviceHash {
		fmt.Println("  ✅ 所有账号的设备Hash完全不同")
	} else {
		fmt.Println("  ❌ 存在设备Hash碰撞（错误！）")
	}

	if fp1.UserAgent != fp2.UserAgent && fp2.UserAgent != fp3.UserAgent {
		fmt.Println("  ✅ 所有账号的User-Agent不同")
	} else {
		fmt.Println("  ⚠️  部分账号的User-Agent相同（版本号范围有限，正常现象）")
	}

	fmt.Println("\n" + strings.Repeat("=", 80))
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
EOF

echo "正在编译演示程序..."
cd /root/2api/kiro2api

# 编译演示程序
if go run /tmp/fingerprint_demo.go 2>/dev/null; then
    echo ""
    echo "✅ 演示成功完成！"
else
    echo ""
    echo "⚠️  演示程序运行失败，可能需要先构建项目"
    echo "请执行："
    echo "  cd /root/2api/kiro2api"
    echo "  go build"
    echo "  然后直接查看代码：cat utils/device_fingerprint.go"
fi

# 清理
rm -f /tmp/fingerprint_demo.go

echo ""
echo "=================================="
echo "演示结束"
echo "=================================="
