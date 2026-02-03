package server

import (
	"fmt"
	"net/http"
	"os"
	"strings"

	"kiro2api/auth"
	"kiro2api/config"
	"kiro2api/converter"
	"kiro2api/logger"
	"kiro2api/types"
	"kiro2api/utils"

	"github.com/gin-gonic/gin"
)

// 移除全局httpClient，使用utils包中的共享客户端

// ServerConfig 服务器配置
type ServerConfig struct {
	Port                 string
	ClientToken          string
	AdminToken           string
	IsDefaultClientToken bool
	IsDefaultAdminToken  bool
}

// serverConfig 全局服务器配置（供 API 查询）
var serverConfig *ServerConfig

// GetServerConfig 获取服务器配置
func GetServerConfig() *ServerConfig {
	return serverConfig
}

// StartServer 启动HTTP代理服务器
func StartServer(port string, clientToken string, adminToken string, isDefaultClientToken bool, isDefaultAdminToken bool, authService *auth.AuthService) {
	// 保存服务器配置
	serverConfig = &ServerConfig{
		Port:                 port,
		ClientToken:          clientToken,
		AdminToken:           adminToken,
		IsDefaultClientToken: isDefaultClientToken,
		IsDefaultAdminToken:  isDefaultAdminToken,
	}

	// 初始化 IP 白名单管理器
	whitelistPath := os.Getenv("KIRO_IP_WHITELIST_PATH")
	if whitelistPath == "" {
		whitelistPath = DefaultWhitelistPath
	}
	if _, err := InitIPWhitelist(whitelistPath); err != nil {
		logger.Error("初始化 IP 白名单失败", logger.Err(err))
	}

	// 设置 gin 模式
	ginMode := os.Getenv("GIN_MODE")
	if ginMode == "" {
		ginMode = gin.ReleaseMode
	}
	gin.SetMode(ginMode)

	r := gin.New()

	// 配置可信代理（影响 gin.Context.ClientIP() 的取值）
	// - Gin 默认会信任所有代理并使用 X-Forwarded-For / X-Real-IP 解析真实客户端 IP（但存在 IP 伪造风险）
	// - 推荐在反向代理后部署时显式配置 KIRO_TRUSTED_PROXIES，只信任你的 LB/Nginx
	// - 如需完全禁用代理信任（强制只用 RemoteAddr），设置 KIRO_DISABLE_PROXY_TRUST=true
	disableProxyTrust := strings.TrimSpace(os.Getenv("KIRO_DISABLE_PROXY_TRUST"))
	trustedProxies := strings.TrimSpace(os.Getenv("KIRO_TRUSTED_PROXIES"))

	if disableProxyTrust == "1" || strings.EqualFold(disableProxyTrust, "true") {
		if err := r.SetTrustedProxies(nil); err != nil {
			logger.Error("禁用代理信任失败", logger.Err(err))
		}
		logger.Info("已禁用代理信任，使用 RemoteAddr 作为真实 IP")
	} else if trustedProxies != "" {
		// 支持逗号分隔的多个代理 IP/CIDR
		proxies := []string{}
		for _, proxy := range strings.Split(trustedProxies, ",") {
			proxy = strings.TrimSpace(proxy)
			if proxy != "" {
				proxies = append(proxies, proxy)
			}
		}
		if len(proxies) == 0 {
			logger.Warn("KIRO_TRUSTED_PROXIES 为空，沿用 Gin 默认（信任所有代理）")
		} else {
			if err := r.SetTrustedProxies(proxies); err != nil {
				logger.Error("设置可信代理失败", logger.Err(err))
			} else {
				logger.Info("已配置可信代理", logger.Int("count", len(proxies)))
			}
		}
	} else {
		logger.Warn("未配置 KIRO_TRUSTED_PROXIES，沿用 Gin 默认（信任所有代理）。建议显式配置可信代理以避免 IP 伪造。")
	}

	// 添加中间件
	r.Use(gin.Logger())
	// IP 并发限制必须在 Recovery 之前，确保 panic 时也能释放资源
	r.Use(IPConcurrencyMiddleware())
	r.Use(gin.Recovery())
	// 注入请求ID，便于日志追踪
	r.Use(RequestIDMiddleware())
	r.Use(corsMiddleware())
	// 注入AuthService到context
	r.Use(func(c *gin.Context) {
		c.Set("authService", authService)
		c.Next()
	})
	// 只对 /v1 开头的端点进行认证（排除管理 API）
	r.Use(PathBasedAuthMiddleware(clientToken, []string{"/v1/messages", "/v1/chat", "/v1/models"}))

	// 静态资源服务 - 前后端完全分离
	r.Static("/static", "./static")
	r.GET("/", func(c *gin.Context) {
		c.File("./static/index.html")
	})

	// API端点 - 纯数据服务
	r.GET("/api/tokens", handleTokenPoolAPI)

	// 注册管理 API 路由（始终启用，使用默认或配置的 adminToken）
	RegisterAdminRoutes(r, authService, adminToken, isDefaultAdminToken)
	logger.Info("管理 API 已启用", logger.String("admin_token", "***"))

	// GET /v1/models 端点
	r.GET("/v1/models", func(c *gin.Context) {
		// 构建模型列表
		models := []types.Model{}
		for anthropicModel := range config.ModelMap {
			model := types.Model{
				ID:          anthropicModel,
				Object:      "model",
				Created:     1234567890,
				OwnedBy:     "anthropic",
				DisplayName: anthropicModel,
				Type:        "text",
				MaxTokens:   200000,
			}
			models = append(models, model)
		}

		response := types.ModelsResponse{
			Object: "list",
			Data:   models,
		}

		c.JSON(http.StatusOK, response)
	})

	r.POST("/v1/messages", func(c *gin.Context) {
		// 读取请求体
		body, err := c.GetRawData()
		if err != nil {
			logger.Error("读取请求体失败", logger.Err(err))
			respondError(c, http.StatusBadRequest, "读取请求体失败: %v", err)
			return
		}

		// 记录请求日志
		logger.Debug("收到Anthropic请求",
			addReqFields(c,
				logger.String("direction", "client_request"),
				logger.String("body", string(body)),
				logger.Int("body_size", len(body)),
				logger.String("remote_addr", c.ClientIP()),
				logger.String("user_agent", c.GetHeader("User-Agent")),
			)...)

		// 先解析为通用map以便处理工具格式
		var rawReq map[string]any
		if err := utils.SafeUnmarshal(body, &rawReq); err != nil {
			logger.Error("解析请求体失败", logger.Err(err))
			respondError(c, http.StatusBadRequest, "解析请求体失败: %v", err)
			return
		}

		// 标准化工具格式处理
		if tools, exists := rawReq["tools"]; exists && tools != nil {
			if toolsArray, ok := tools.([]any); ok {
				normalizedTools := make([]map[string]any, 0, len(toolsArray))
				for _, tool := range toolsArray {
					if toolMap, ok := tool.(map[string]any); ok {
						// 检查是否是简化的工具格式（直接包含name, description, input_schema）
						if name, hasName := toolMap["name"]; hasName {
							if description, hasDesc := toolMap["description"]; hasDesc {
								if inputSchema, hasSchema := toolMap["input_schema"]; hasSchema {
									// 转换为标准Anthropic工具格式
									normalizedTool := map[string]any{
										"name":         name,
										"description":  description,
										"input_schema": inputSchema,
									}
									normalizedTools = append(normalizedTools, normalizedTool)
									continue
								}
							}
						}
						// 如果不是简化格式，保持原样
						normalizedTools = append(normalizedTools, toolMap)
					}
				}
				rawReq["tools"] = normalizedTools
			}
		}

		// 重新序列化并解析为AnthropicRequest
		normalizedBody, err := utils.SafeMarshal(rawReq)
		if err != nil {
			logger.Error("重新序列化请求失败", logger.Err(err))
			respondError(c, http.StatusBadRequest, "处理请求格式失败: %v", err)
			return
		}

		var anthropicReq types.AnthropicRequest
		if err := utils.SafeUnmarshal(normalizedBody, &anthropicReq); err != nil {
			logger.Error("解析标准化请求体失败", logger.Err(err))
			respondError(c, http.StatusBadRequest, "解析请求体失败: %v", err)
			return
		}

		// 检测 -thinking 后缀模型，自动启用 thinking 模式
		if strings.HasSuffix(anthropicReq.Model, "-thinking") && anthropicReq.Thinking == nil {
			anthropicReq.Thinking = map[string]any{
				"type":          "enabled",
				"budget_tokens": converter.DefaultMaxThinkingLength,
			}
		}

		// 验证请求的有效性
		if len(anthropicReq.Messages) == 0 {
			logger.Error("请求中没有消息")
			respondError(c, http.StatusBadRequest, "%s", "messages 数组不能为空")
			return
		}

		// 验证最后一条消息有有效内容
		lastMsg := anthropicReq.Messages[len(anthropicReq.Messages)-1]
		content, err := utils.GetMessageContent(lastMsg.Content)
		if err != nil {
			logger.Error("获取消息内容失败",
				logger.Err(err),
				logger.String("raw_content", fmt.Sprintf("%v", lastMsg.Content)))
			respondError(c, http.StatusBadRequest, "获取消息内容失败: %v", err)
			return
		}

		trimmedContent := strings.TrimSpace(content)
		if trimmedContent == "" || trimmedContent == "answer for user question" {
			logger.Error("消息内容为空或无效",
				logger.String("content", content),
				logger.String("trimmed_content", trimmedContent))
			respondError(c, http.StatusBadRequest, "%s", "消息内容不能为空")
			return
		}

		// 检测是否为纯 WebSearch 请求
		if IsWebSearchRequest(anthropicReq) {
			logger.Info("检测到 WebSearch 工具，路由到 WebSearch 处理")
			HandleWebSearchRequest(c, anthropicReq, authService)
			return
		}

		if anthropicReq.Stream {
			handleStreamRequest(c, anthropicReq, authService)
			return
		}

		handleNonStreamRequest(c, anthropicReq, authService)
	})

	// Token计数端点
	r.POST("/v1/messages/count_tokens", handleCountTokens)

	// 新增：OpenAI兼容的 /v1/chat/completions 端点
	r.POST("/v1/chat/completions", func(c *gin.Context) {
		// 使用RequestContext统一处理token获取和请求体读取
		reqCtx := &RequestContext{
			GinContext:  c,
			AuthService: authService,
			RequestType: "OpenAI",
		}

		tokenInfo, body, err := reqCtx.GetTokenAndBody()
		if err != nil {
			return // 错误已在GetTokenAndBody中处理
		}

		var openaiReq types.OpenAIRequest
		if err := utils.SafeUnmarshal(body, &openaiReq); err != nil {
			logger.Error("解析OpenAI请求体失败", logger.Err(err))
			respondError(c, http.StatusBadRequest, "解析请求体失败: %v", err)
			return
		}

		logger.Debug("OpenAI请求解析成功",
			logger.String("model", openaiReq.Model),
			logger.Bool("stream", openaiReq.Stream != nil && *openaiReq.Stream),
			logger.Int("max_tokens", func() int {
				if openaiReq.MaxTokens != nil {
					return *openaiReq.MaxTokens
				}
				return 16384
			}()))

		// 转换为Anthropic格式
		anthropicReq := converter.ConvertOpenAIToAnthropic(openaiReq)

		if anthropicReq.Stream {
			handleOpenAIStreamRequest(c, anthropicReq, tokenInfo)
			return
		}
		handleOpenAINonStreamRequest(c, anthropicReq, tokenInfo)
	})

	r.NoRoute(func(c *gin.Context) {
		logger.Warn("访问未知端点",
			logger.String("path", c.Request.URL.Path),
			logger.String("method", c.Request.Method))
		respondError(c, http.StatusNotFound, "%s", "404 未找到")
	})

	logger.Info("启动Anthropic API代理服务器",
		logger.String("port", port),
		logger.String("auth_token", "***"))
	logger.Info("AuthToken 验证已启用")
	logger.Info("可用端点:")
	logger.Info("  GET  /                          - 重定向到静态Dashboard")
	logger.Info("  GET  /static/*                  - 静态资源服务")
	logger.Info("  GET  /api/tokens                - Token池状态API")
	logger.Info("  GET  /v1/models                 - 模型列表")
	logger.Info("  POST /v1/messages               - Anthropic API代理")
	logger.Info("  POST /v1/messages/count_tokens  - Token计数接口")
	logger.Info("  POST /v1/chat/completions       - OpenAI API代理")
	logger.Info("按Ctrl+C停止服务器")

	// 创建自定义HTTP服务器以支持长时间请求
	server := &http.Server{
		Addr:    ":" + port,
		Handler: r,
	}

	logger.Info("启动HTTP服务器", logger.String("port", port))

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("启动服务器失败", logger.Err(err), logger.String("port", port))
		os.Exit(1)
	}
}

// corsMiddleware CORS中间件
func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization, x-api-key")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusOK)
			return
		}

		c.Next()
	}
}
