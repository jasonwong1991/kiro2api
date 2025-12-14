package main

import (
	"os"

	"kiro2api/auth"
	"kiro2api/logger"
	"kiro2api/server"

	"github.com/joho/godotenv"
)

// DefaultAdminToken 默认管理员 Token（首次使用时应修改）
const DefaultAdminToken = "changeme"

// DefaultClientToken 默认客户端 Token（首次使用时应修改）
const DefaultClientToken = "changeme"

func main() {
	// 自动加载.env文件
	if err := godotenv.Load(); err != nil {
		logger.Info("未找到.env文件，使用环境变量")
	}

	// 重新初始化logger以使用.env文件中的配置
	logger.Reinitialize()

	// 显示当前日志级别设置（仅在DEBUG级别时显示详细信息）
	// 注意：移除重复的系统字段，这些信息已包含在日志结构中
	logger.Debug("日志系统初始化完成",
		logger.String("config_level", os.Getenv("LOG_LEVEL")),
		logger.String("config_file", os.Getenv("LOG_FILE")))

	// 🚀 创建AuthService实例（使用依赖注入）
	// 允许空配置启动，可通过 WebUI 添加 Token
	logger.Info("正在创建AuthService...")
	authService, err := auth.NewAuthService()
	if err != nil {
		logger.Error("AuthService创建失败", logger.Err(err))
		logger.Error("请检查token配置后重新启动服务器")
		os.Exit(1)
	}

	port := "8080" // 默认端口
	if len(os.Args) > 1 {
		port = os.Args[1]
	}
	// 从环境变量获取端口，覆盖命令行参数
	if envPort := os.Getenv("PORT"); envPort != "" {
		port = envPort
	}

	// 从环境变量获取客户端认证token（提供默认值，但会警告）
	clientToken := os.Getenv("KIRO_CLIENT_TOKEN")
	if clientToken == "" {
		clientToken = DefaultClientToken
		logger.Warn("⚠️  KIRO_CLIENT_TOKEN 未设置，使用默认值 'changeme'")
		logger.Warn("⚠️  请尽快在 .env 文件中设置强密码: KIRO_CLIENT_TOKEN=your-secure-random-password")
	}

	// 从环境变量获取管理员token（提供默认值，但会警告）
	adminToken := os.Getenv("KIRO_ADMIN_TOKEN")
	if adminToken == "" {
		adminToken = DefaultAdminToken
		logger.Warn("⚠️  KIRO_ADMIN_TOKEN 未设置，使用默认值 'changeme'")
		logger.Warn("⚠️  请尽快在 .env 文件中设置强密码: KIRO_ADMIN_TOKEN=your-secure-admin-password")
	}

	// 检查是否使用默认密码
	isDefaultClientToken := clientToken == DefaultClientToken
	isDefaultAdminToken := adminToken == DefaultAdminToken

	server.StartServer(port, clientToken, adminToken, isDefaultClientToken, isDefaultAdminToken, authService)
}
