package server

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"

	"kiro2api/auth"
	"kiro2api/logger"

	"github.com/gin-gonic/gin"
)

// AdminHandlers 管理 API 处理器
type AdminHandlers struct {
	authService *auth.AuthService
}

// NewAdminHandlers 创建管理 API 处理器
func NewAdminHandlers(authService *auth.AuthService) *AdminHandlers {
	return &AdminHandlers{
		authService: authService,
	}
}

// HandleListTokens 列出所有 token 状态
// GET /v1/admin/tokens
func (h *AdminHandlers) HandleListTokens(c *gin.Context) {
	tm := h.authService.GetTokenManager()
	if tm == nil {
		respondError(c, http.StatusInternalServerError, "TokenManager 未初始化")
		return
	}

	statuses := tm.GetAllTokensStatus()

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"tokens": statuses,
			"total":  len(statuses),
		},
	})
}

// HandleGetToken 获取单个 token 状态
// GET /v1/admin/tokens/:index
func (h *AdminHandlers) HandleGetToken(c *gin.Context) {
	tm := h.authService.GetTokenManager()
	if tm == nil {
		respondError(c, http.StatusInternalServerError, "TokenManager 未初始化")
		return
	}

	indexStr := c.Param("index")
	index, err := strconv.Atoi(indexStr)
	if err != nil {
		respondError(c, http.StatusBadRequest, "无效的索引: %s", indexStr)
		return
	}

	status, err := tm.GetTokenStatus(index)
	if err != nil {
		respondError(c, http.StatusNotFound, "%v", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    status,
	})
}

// ExportRequest 导出请求
type ExportRequest struct {
	Indices []int `json:"indices"` // 空数组表示导出全部
}

// HandleExportTokens 导出 token 配置
// POST /v1/admin/tokens/export
func (h *AdminHandlers) HandleExportTokens(c *gin.Context) {
	tm := h.authService.GetTokenManager()
	if tm == nil {
		respondError(c, http.StatusInternalServerError, "TokenManager 未初始化")
		return
	}

	var req ExportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		// 如果没有请求体，导出全部
		req.Indices = []int{}
	}

	configs, err := tm.ExportTokens(req.Indices)
	if err != nil {
		respondError(c, http.StatusBadRequest, "%v", err)
		return
	}

	logger.Info("导出token配置",
		logger.Int("count", len(configs)),
		logger.Any("indices", req.Indices))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"configs": configs,
			"count":   len(configs),
		},
	})
}

// HandleDeleteToken 删除单个失效 token
// DELETE /v1/admin/tokens/:index
func (h *AdminHandlers) HandleDeleteToken(c *gin.Context) {
	tm := h.authService.GetTokenManager()
	if tm == nil {
		respondError(c, http.StatusInternalServerError, "TokenManager 未初始化")
		return
	}

	indexStr := c.Param("index")
	index, err := strconv.Atoi(indexStr)
	if err != nil {
		respondError(c, http.StatusBadRequest, "无效的索引: %s", indexStr)
		return
	}

	if err := tm.RemoveToken(index); err != nil {
		if errors.Is(err, auth.ErrConfigPersistence) {
			respondError(c, http.StatusInternalServerError, "%v", err)
		} else {
			respondError(c, http.StatusBadRequest, "%v", err)
		}
		return
	}

	logger.Info("删除失效token",
		logger.Int("index", index),
		logger.String("request_id", c.GetString("request_id")))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Token 已删除",
	})
}

// HandleDeleteInvalidTokens 批量删除所有失效 token
// DELETE /v1/admin/tokens/invalid
func (h *AdminHandlers) HandleDeleteInvalidTokens(c *gin.Context) {
	tm := h.authService.GetTokenManager()
	if tm == nil {
		respondError(c, http.StatusInternalServerError, "TokenManager 未初始化")
		return
	}

	removedCount, err := tm.RemoveInvalidTokens()
	if err != nil {
		respondError(c, http.StatusInternalServerError, "%v", err)
		return
	}

	logger.Info("批量删除失效token",
		logger.Int("removed_count", removedCount),
		logger.String("request_id", c.GetString("request_id")))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "已删除所有失效 token",
		"data": gin.H{
			"removed_count": removedCount,
		},
	})
}

// HandleSyncConfig 手动同步配置文件
// POST /v1/admin/tokens/sync
func (h *AdminHandlers) HandleSyncConfig(c *gin.Context) {
	tm := h.authService.GetTokenManager()
	if tm == nil {
		respondError(c, http.StatusInternalServerError, "TokenManager 未初始化")
		return
	}

	if err := tm.SyncConfigFile(); err != nil {
		respondError(c, http.StatusBadRequest, "%v", err)
		return
	}

	logger.Info("手动同步配置文件",
		logger.String("request_id", c.GetString("request_id")))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "配置文件已同步",
	})
}

// RefreshRequest 刷新请求
type RefreshRequest struct {
	Indices []int `json:"indices"` // 空数组表示刷新全部
}

// HandleRefreshToken 刷新单个账号状态
// POST /v1/admin/tokens/:index/refresh
func (h *AdminHandlers) HandleRefreshToken(c *gin.Context) {
	tm := h.authService.GetTokenManager()
	if tm == nil {
		respondError(c, http.StatusInternalServerError, "TokenManager 未初始化")
		return
	}

	indexStr := c.Param("index")
	index, err := strconv.Atoi(indexStr)
	if err != nil {
		respondError(c, http.StatusBadRequest, "无效的索引: %s", indexStr)
		return
	}

	result, err := tm.RefreshToken(index)
	if err != nil {
		respondError(c, http.StatusBadRequest, "%v", err)
		return
	}

	logger.Info("刷新单个账号",
		logger.Int("index", index),
		logger.Bool("success", result.Success),
		logger.String("request_id", c.GetString("request_id")))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    result,
	})
}

// HandleRefreshTokens 批量刷新账号状态
// POST /v1/admin/tokens/refresh
func (h *AdminHandlers) HandleRefreshTokens(c *gin.Context) {
	tm := h.authService.GetTokenManager()
	if tm == nil {
		respondError(c, http.StatusInternalServerError, "TokenManager 未初始化")
		return
	}

	var req RefreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		// 如果没有请求体或解析失败，刷新全部
		req.Indices = []int{}
	}

	results, err := tm.RefreshTokens(req.Indices)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "%v", err)
		return
	}

	successCount := 0
	for _, r := range results {
		if r.Success {
			successCount++
		}
	}

	logger.Info("批量刷新账号",
		logger.Int("total", len(results)),
		logger.Int("success", successCount),
		logger.Any("indices", req.Indices),
		logger.String("request_id", c.GetString("request_id")))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"results": results,
			"total":   len(results),
			"success": successCount,
			"failed":  len(results) - successCount,
		},
	})
}

// HandleGetRefreshStats 获取刷新管理器统计信息
// GET /v1/admin/tokens/refresh/stats
func (h *AdminHandlers) HandleGetRefreshStats(c *gin.Context) {
	tm := h.authService.GetTokenManager()
	if tm == nil {
		respondError(c, http.StatusInternalServerError, "TokenManager 未初始化")
		return
	}

	stats := tm.GetRefreshStats()

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    stats,
	})
}

// RegisterAdminRoutes 注册管理 API 路由
func RegisterAdminRoutes(r *gin.Engine, authService *auth.AuthService, adminToken string, isDefaultAdminToken bool) {
	handlers := NewAdminHandlers(authService)

	// 管理 API 路由组
	admin := r.Group("/v1/admin")

	// 管理员认证中间件
	admin.Use(AdminAuthMiddleware(adminToken))

	// 系统状态端点（用于检查是否使用默认密码）
	admin.GET("/status", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"success": true,
			"data": gin.H{
				"is_default_admin_token":  isDefaultAdminToken,
				"is_default_client_token": GetServerConfig().IsDefaultClientToken,
			},
		})
	})

	// 修改管理员密码
	admin.PUT("/password/admin", func(c *gin.Context) {
		var req struct {
			NewPassword string `json:"newPassword"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			respondError(c, http.StatusBadRequest, "无效的请求体")
			return
		}

		if len(req.NewPassword) < 6 {
			respondError(c, http.StatusBadRequest, "密码长度至少 6 位")
			return
		}

		// 更新 .env 文件
		if err := updateEnvFile("KIRO_ADMIN_TOKEN", req.NewPassword); err != nil {
			respondError(c, http.StatusInternalServerError, "更新配置文件失败: %v", err)
			return
		}

		// 更新内存中的配置
		serverConfig.AdminToken = req.NewPassword
		serverConfig.IsDefaultAdminToken = false

		logger.Info("管理员密码已更新", logger.String("request_id", c.GetString("request_id")))

		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": "管理员密码已更新，请使用新密码重新登录",
		})
	})

	// 修改客户端密码
	admin.PUT("/password/client", func(c *gin.Context) {
		var req struct {
			NewPassword string `json:"newPassword"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			respondError(c, http.StatusBadRequest, "无效的请求体")
			return
		}

		if len(req.NewPassword) < 6 {
			respondError(c, http.StatusBadRequest, "密码长度至少 6 位")
			return
		}

		// 更新 .env 文件
		if err := updateEnvFile("KIRO_CLIENT_TOKEN", req.NewPassword); err != nil {
			respondError(c, http.StatusInternalServerError, "更新配置文件失败: %v", err)
			return
		}

		// 更新内存中的配置
		serverConfig.ClientToken = req.NewPassword
		serverConfig.IsDefaultClientToken = false

		logger.Info("客户端密码已更新", logger.String("request_id", c.GetString("request_id")))

		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": "客户端密码已更新",
		})
	})

	// Token 管理端点
	admin.GET("/tokens", handlers.HandleListTokens)
	admin.GET("/tokens/:index", handlers.HandleGetToken)
	admin.POST("/tokens/export", handlers.HandleExportTokens)
	admin.POST("/tokens/refresh", handlers.HandleRefreshTokens)
	admin.GET("/tokens/refresh/stats", handlers.HandleGetRefreshStats)
	admin.POST("/tokens/:index/refresh", handlers.HandleRefreshToken)
	admin.DELETE("/tokens/:index", handlers.HandleDeleteToken)
	admin.DELETE("/tokens/invalid", handlers.HandleDeleteInvalidTokens)
	admin.POST("/tokens/sync", handlers.HandleSyncConfig)
	admin.POST("/tokens/add", handlers.HandleAddToken)
	admin.POST("/tokens/import", handlers.HandleImportTokens)
	admin.PUT("/tokens/:index", handlers.HandleUpdateToken)

	// 代理池管理端点
	admin.GET("/proxies", handlers.HandleListProxies)
	admin.POST("/proxies/add", handlers.HandleAddProxy)
	admin.DELETE("/proxies/:index", handlers.HandleDeleteProxy)

	// IP 监控和白名单管理端点
	admin.GET("/ip/stats", HandleGetIPStats)
	admin.GET("/ip/whitelist", HandleGetWhitelist)
	admin.POST("/ip/whitelist", HandleAddWhitelist)
	admin.DELETE("/ip/whitelist", HandleRemoveWhitelist)

	logger.Info("管理 API 路由已注册")
	logger.Info("  GET    /v1/admin/status                - 获取系统状态")
	logger.Info("  GET    /v1/admin/tokens                - 列出所有 token 状态")
	logger.Info("  GET    /v1/admin/tokens/:index         - 获取单个 token 状态")
	logger.Info("  POST   /v1/admin/tokens/add            - 添加新 token")
	logger.Info("  PUT    /v1/admin/tokens/:index         - 更新 token")
	logger.Info("  POST   /v1/admin/tokens/export         - 导出 token 配置")
	logger.Info("  POST   /v1/admin/tokens/refresh        - 批量刷新账号状态")
	logger.Info("  GET    /v1/admin/tokens/refresh/stats  - 获取刷新管理器统计信息")
	logger.Info("  POST   /v1/admin/tokens/:index/refresh - 刷新单个账号状态")
	logger.Info("  DELETE /v1/admin/tokens/:index         - 删除单个失效 token")
	logger.Info("  DELETE /v1/admin/tokens/invalid        - 批量删除所有失效 token")
	logger.Info("  POST   /v1/admin/tokens/sync           - 手动同步配置文件")
	logger.Info("  GET    /v1/admin/proxies               - 列出所有代理")
	logger.Info("  POST   /v1/admin/proxies/add           - 添加代理")
	logger.Info("  DELETE /v1/admin/proxies/:index        - 删除代理")
	logger.Info("  GET    /v1/admin/ip/stats              - 获取 IP 并发统计")
	logger.Info("  GET    /v1/admin/ip/whitelist          - 获取白名单列表")
	logger.Info("  POST   /v1/admin/ip/whitelist          - 添加 IP 到白名单")
	logger.Info("  DELETE /v1/admin/ip/whitelist          - 从白名单移除 IP (JSON Body)")
}

// AddTokenRequest 添加 token 请求
type AddTokenRequest struct {
	AuthType     string `json:"auth"`
	RefreshToken string `json:"refreshToken"`
	ClientID     string `json:"clientId,omitempty"`
	ClientSecret string `json:"clientSecret,omitempty"`
}

// ImportTokensRequest 批量导入 token 请求
type ImportTokensRequest struct {
	Tokens []auth.AuthConfig `json:"tokens"`
}

// ImportResult 导入结果
type ImportResult struct {
	Total   int      `json:"total"`
	Success int      `json:"success"`
	Skipped int      `json:"skipped"` // 重复跳过的数量
	Failed  int      `json:"failed"`
	Errors  []string `json:"errors,omitempty"`
}

// HandleImportTokens 批量导入 tokens
// POST /v1/admin/tokens/import
func (h *AdminHandlers) HandleImportTokens(c *gin.Context) {
	tm := h.authService.GetTokenManager()
	if tm == nil {
		respondError(c, http.StatusInternalServerError, "TokenManager 未初始化")
		return
	}

	var req ImportTokensRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "无效的请求体: %v", err)
		return
	}

	if len(req.Tokens) == 0 {
		respondError(c, http.StatusBadRequest, "tokens 数组不能为空")
		return
	}

	// 限制单次导入数量，防止内存溢出
	const maxImportSize = 1000
	if len(req.Tokens) > maxImportSize {
		respondError(c, http.StatusBadRequest, "单次导入不能超过 %d 个 token", maxImportSize)
		return
	}

	result := ImportResult{
		Total:  len(req.Tokens),
		Errors: []string{},
	}

	// 批量处理，每批 50 个
	const batchSize = 50
	for i := 0; i < len(req.Tokens); i += batchSize {
		end := i + batchSize
		if end > len(req.Tokens) {
			end = len(req.Tokens)
		}

		batch := req.Tokens[i:end]
		for j, token := range batch {
			idx := i + j

			// 验证必要字段
			if token.RefreshToken == "" {
				result.Failed++
				result.Errors = append(result.Errors, fmt.Sprintf("索引 %d: refreshToken 不能为空", idx))
				continue
			}

			// 设置默认认证类型
			if token.AuthType == "" {
				token.AuthType = auth.AuthMethodSocial
			}

			// 验证 IdC 认证的必要字段
			if token.AuthType == auth.AuthMethodIdC {
				if token.ClientID == "" || token.ClientSecret == "" {
					result.Failed++
					result.Errors = append(result.Errors, fmt.Sprintf("索引 %d: IdC 认证需要 clientId 和 clientSecret", idx))
					continue
				}
			}

			// 添加到 TokenManager（内部会处理持久化）
			if err := tm.AddTokenWithoutSave(token); err != nil {
				// 区分重复和其他错误
				if strings.Contains(err.Error(), "重复") {
					result.Skipped++
				} else {
					result.Failed++
					result.Errors = append(result.Errors, fmt.Sprintf("索引 %d: %v", idx, err))
				}
				continue
			}

			result.Success++
		}
	}

	// 批量添加完成后统一保存
	if result.Success > 0 {
		if err := tm.SaveConfig(); err != nil {
			logger.Warn("保存配置文件失败", logger.Err(err))
			c.JSON(http.StatusInternalServerError, gin.H{
				"success": false,
				"message": fmt.Sprintf("保存配置文件失败: %v", err),
				"data":    result,
			})
			return
		}
	}

	// 限制错误信息数量，避免响应过大
	const maxErrors = 20
	if len(result.Errors) > maxErrors {
		result.Errors = append(result.Errors[:maxErrors], fmt.Sprintf("... 还有 %d 个错误", len(result.Errors)-maxErrors))
	}

	logger.Info("批量导入 token",
		logger.Int("total", result.Total),
		logger.Int("success", result.Success),
		logger.Int("failed", result.Failed),
		logger.String("request_id", c.GetString("request_id")))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    result,
	})
}

// HandleAddToken 添加新 token
// POST /v1/admin/tokens/add
func (h *AdminHandlers) HandleAddToken(c *gin.Context) {
	tm := h.authService.GetTokenManager()
	if tm == nil {
		respondError(c, http.StatusInternalServerError, "TokenManager 未初始化")
		return
	}

	var req AddTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "无效的请求体: %v", err)
		return
	}

	// 验证必要字段
	if req.RefreshToken == "" {
		respondError(c, http.StatusBadRequest, "refreshToken 不能为空")
		return
	}

	// 设置默认认证类型
	if req.AuthType == "" {
		req.AuthType = auth.AuthMethodSocial
	}

	// 验证 IdC 认证的必要字段
	if req.AuthType == auth.AuthMethodIdC {
		if req.ClientID == "" || req.ClientSecret == "" {
			respondError(c, http.StatusBadRequest, "IdC 认证需要 clientId 和 clientSecret")
			return
		}
	}

	// 创建配置
	config := auth.AuthConfig{
		AuthType:     req.AuthType,
		RefreshToken: req.RefreshToken,
		ClientID:     req.ClientID,
		ClientSecret: req.ClientSecret,
	}

	// 添加到 TokenManager
	if err := tm.AddToken(config); err != nil {
		if errors.Is(err, auth.ErrConfigPersistence) {
			respondError(c, http.StatusInternalServerError, "添加 token 失败: %v", err)
		} else {
			respondError(c, http.StatusBadRequest, "添加 token 失败: %v", err)
		}
		return
	}

	logger.Info("添加新 token",
		logger.String("auth_type", req.AuthType),
		logger.String("request_id", c.GetString("request_id")))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Token 已添加",
	})
}

// UpdateTokenRequest 更新 token 请求
type UpdateTokenRequest struct {
	AuthType     string `json:"auth,omitempty"`
	RefreshToken string `json:"refreshToken,omitempty"`
	ClientID     string `json:"clientId,omitempty"`
	ClientSecret string `json:"clientSecret,omitempty"`
	Disabled     *bool  `json:"disabled,omitempty"`
}

// HandleUpdateToken 更新 token
// PUT /v1/admin/tokens/:index
func (h *AdminHandlers) HandleUpdateToken(c *gin.Context) {
	tm := h.authService.GetTokenManager()
	if tm == nil {
		respondError(c, http.StatusInternalServerError, "TokenManager 未初始化")
		return
	}

	indexStr := c.Param("index")
	index, err := strconv.Atoi(indexStr)
	if err != nil {
		respondError(c, http.StatusBadRequest, "无效的索引: %s", indexStr)
		return
	}

	var req UpdateTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "无效的请求体: %v", err)
		return
	}

	// 更新 token
	if err := tm.UpdateToken(index, req.AuthType, req.RefreshToken, req.ClientID, req.ClientSecret, req.Disabled); err != nil {
		if errors.Is(err, auth.ErrConfigPersistence) {
			respondError(c, http.StatusInternalServerError, "%v", err)
		} else {
			respondError(c, http.StatusBadRequest, "%v", err)
		}
		return
	}

	logger.Info("更新 token",
		logger.Int("index", index),
		logger.String("request_id", c.GetString("request_id")))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Token 已更新",
	})
}

// HandleListProxies 列出所有代理
// GET /v1/admin/proxies
func (h *AdminHandlers) HandleListProxies(c *gin.Context) {
	tm := h.authService.GetTokenManager()
	if tm == nil {
		respondError(c, http.StatusInternalServerError, "TokenManager 未初始化")
		return
	}

	proxies := tm.GetProxies()

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"proxies": proxies,
			"total":   len(proxies),
		},
	})
}

// AddProxyRequest 添加代理请求
type AddProxyRequest struct {
	URL string `json:"url"`
}

// HandleAddProxy 添加代理
// POST /v1/admin/proxies/add
func (h *AdminHandlers) HandleAddProxy(c *gin.Context) {
	tm := h.authService.GetTokenManager()
	if tm == nil {
		respondError(c, http.StatusInternalServerError, "TokenManager 未初始化")
		return
	}

	var req AddProxyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "无效的请求体: %v", err)
		return
	}

	if req.URL == "" {
		respondError(c, http.StatusBadRequest, "代理 URL 不能为空")
		return
	}

	if err := tm.AddProxy(req.URL); err != nil {
		respondError(c, http.StatusBadRequest, "添加代理失败: %v", err)
		return
	}

	logger.Info("添加代理",
		logger.String("proxy_url", req.URL),
		logger.String("request_id", c.GetString("request_id")))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "代理已添加",
	})
}

// HandleDeleteProxy 删除代理
// DELETE /v1/admin/proxies/:index
func (h *AdminHandlers) HandleDeleteProxy(c *gin.Context) {
	tm := h.authService.GetTokenManager()
	if tm == nil {
		respondError(c, http.StatusInternalServerError, "TokenManager 未初始化")
		return
	}

	indexStr := c.Param("index")
	index, err := strconv.Atoi(indexStr)
	if err != nil {
		respondError(c, http.StatusBadRequest, "无效的索引: %s", indexStr)
		return
	}

	if err := tm.RemoveProxy(index); err != nil {
		respondError(c, http.StatusBadRequest, "删除代理失败: %v", err)
		return
	}

	logger.Info("删除代理",
		logger.Int("index", index),
		logger.String("request_id", c.GetString("request_id")))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "代理已删除",
	})
}

// AdminAuthMiddleware 管理员认证中间件
func AdminAuthMiddleware(adminToken string) gin.HandlerFunc {
	return func(c *gin.Context) {
		// adminToken 始终有值（默认或配置的）
		// 使用当前内存中的 adminToken（可能已被更新）
		currentAdminToken := adminToken
		if serverConfig != nil && serverConfig.AdminToken != "" {
			currentAdminToken = serverConfig.AdminToken
		}

		// 从 Authorization header 获取 token
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			respondError(c, http.StatusUnauthorized, "缺少 Authorization header")
			c.Abort()
			return
		}

		// 支持 "Bearer <token>" 格式
		token := authHeader
		if len(authHeader) > 7 && authHeader[:7] == "Bearer " {
			token = authHeader[7:]
		}

		// 验证 token
		if token != currentAdminToken {
			logger.Warn("管理 API 认证失败",
				logger.String("path", c.Request.URL.Path),
				logger.String("client_ip", c.ClientIP()))
			respondError(c, http.StatusUnauthorized, "无效的管理员 token")
			c.Abort()
			return
		}

		c.Next()
	}
}

// updateEnvFile 更新 .env 文件中的配置项
func updateEnvFile(key, value string) error {
	envPath := ".env"

	// 读取现有内容
	content, err := os.ReadFile(envPath)
	if err != nil {
		if os.IsNotExist(err) {
			// 文件不存在，创建新文件
			content = []byte{}
		} else {
			return fmt.Errorf("读取 .env 文件失败: %w", err)
		}
	}

	lines := strings.Split(string(content), "\n")
	found := false
	newLines := make([]string, 0, len(lines)+1)

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// 跳过空行和注释
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			newLines = append(newLines, line)
			continue
		}

		// 检查是否是目标 key
		if strings.HasPrefix(trimmed, key+"=") {
			newLines = append(newLines, fmt.Sprintf("%s=%s", key, value))
			found = true
		} else {
			newLines = append(newLines, line)
		}
	}

	// 如果没找到，添加新行
	if !found {
		// 确保最后一行不是空行时添加换行
		if len(newLines) > 0 && newLines[len(newLines)-1] != "" {
			newLines = append(newLines, "")
		}
		newLines = append(newLines, fmt.Sprintf("%s=%s", key, value))
	}

	// 写回文件
	newContent := strings.Join(newLines, "\n")
	if err := os.WriteFile(envPath, []byte(newContent), 0644); err != nil {
		return fmt.Errorf("写入 .env 文件失败: %w", err)
	}

	logger.Info("更新 .env 配置",
		logger.String("key", key),
		logger.String("value", "***"))

	return nil
}
