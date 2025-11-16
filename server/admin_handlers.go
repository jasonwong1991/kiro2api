package server

import (
	"net/http"
	"strconv"

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
		respondError(c, http.StatusBadRequest, "%v", err)
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

// RegisterAdminRoutes 注册管理 API 路由
func RegisterAdminRoutes(r *gin.Engine, authService *auth.AuthService, adminToken string) {
	handlers := NewAdminHandlers(authService)

	// 管理 API 路由组
	admin := r.Group("/v1/admin")

	// 管理员认证中间件
	admin.Use(AdminAuthMiddleware(adminToken))

	// Token 管理端点
	admin.GET("/tokens", handlers.HandleListTokens)
	admin.GET("/tokens/:index", handlers.HandleGetToken)
	admin.POST("/tokens/export", handlers.HandleExportTokens)
	admin.POST("/tokens/refresh", handlers.HandleRefreshTokens)
	admin.POST("/tokens/:index/refresh", handlers.HandleRefreshToken)
	admin.DELETE("/tokens/:index", handlers.HandleDeleteToken)
	admin.DELETE("/tokens/invalid", handlers.HandleDeleteInvalidTokens)
	admin.POST("/tokens/sync", handlers.HandleSyncConfig)

	logger.Info("管理 API 路由已注册")
	logger.Info("  GET    /v1/admin/tokens                - 列出所有 token 状态")
	logger.Info("  GET    /v1/admin/tokens/:index         - 获取单个 token 状态")
	logger.Info("  POST   /v1/admin/tokens/export         - 导出 token 配置")
	logger.Info("  POST   /v1/admin/tokens/refresh        - 批量刷新账号状态")
	logger.Info("  POST   /v1/admin/tokens/:index/refresh - 刷新单个账号状态")
	logger.Info("  DELETE /v1/admin/tokens/:index         - 删除单个失效 token")
	logger.Info("  DELETE /v1/admin/tokens/invalid        - 批量删除所有失效 token")
	logger.Info("  POST   /v1/admin/tokens/sync           - 手动同步配置文件")
}

// AdminAuthMiddleware 管理员认证中间件
func AdminAuthMiddleware(adminToken string) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 如果未设置管理员 token，拒绝访问
		if adminToken == "" {
			logger.Warn("管理 API 访问被拒绝：未配置管理员 token",
				logger.String("path", c.Request.URL.Path),
				logger.String("client_ip", c.ClientIP()))
			respondError(c, http.StatusForbidden, "管理 API 未启用")
			c.Abort()
			return
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
		if token != adminToken {
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
