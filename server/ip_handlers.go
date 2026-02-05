package server

import (
	"net/http"

	"kiro2api/logger"

	"github.com/gin-gonic/gin"
)

// HandleGetIPStats 获取 IP 并发统计
// GET /v1/admin/ip/stats
func HandleGetIPStats(c *gin.Context) {
	if globalIPLimiter == nil {
		respondError(c, http.StatusInternalServerError, "IP 限制器未初始化")
		return
	}

	stats := globalIPLimiter.GetStats()
	whitelistManager := GetIPWhitelistManager()
	whitelistCount := 0
	if whitelistManager != nil {
		whitelistCount = whitelistManager.Count()
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"ip_stats":         stats,
			"whitelist_count":  whitelistCount,
			"max_concurrent":   globalIPLimiter.maxConcurrent,
			"acquire_timeout":  globalIPLimiter.acquireTimeout.String(),
			"ipv6_block_enabled": IsIPv6BlockEnabled(),
		},
	})
}

// HandleGetWhitelist 获取白名单列表
// GET /v1/admin/ip/whitelist
func HandleGetWhitelist(c *gin.Context) {
	whitelistManager := GetIPWhitelistManager()
	if whitelistManager == nil {
		respondError(c, http.StatusInternalServerError, "白名单管理器未初始化")
		return
	}

	entries := whitelistManager.GetAll()

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"entries": entries,
			"count":   len(entries),
		},
	})
}

// AddWhitelistRequest 添加白名单请求
type AddWhitelistRequest struct {
	IP          string `json:"ip" binding:"required"`
	Description string `json:"description"`
}

// HandleAddWhitelist 添加 IP 到白名单
// POST /v1/admin/ip/whitelist
func HandleAddWhitelist(c *gin.Context) {
	whitelistManager := GetIPWhitelistManager()
	if whitelistManager == nil {
		respondError(c, http.StatusInternalServerError, "白名单管理器未初始化")
		return
	}

	var req AddWhitelistRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "无效的请求体: %v", err)
		return
	}

	if err := whitelistManager.AddIP(req.IP, req.Description); err != nil {
		respondError(c, http.StatusBadRequest, "添加白名单失败: %v", err)
		return
	}

	logger.Info("添加 IP 到白名单",
		logger.String("ip", req.IP),
		logger.String("description", req.Description),
		logger.String("request_id", c.GetString("request_id")))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "IP 已添加到白名单",
	})
}

// RemoveWhitelistRequest 移除白名单请求
type RemoveWhitelistRequest struct {
	IP string `json:"ip" binding:"required"`
}

// HandleRemoveWhitelist 从白名单移除 IP
// DELETE /v1/admin/ip/whitelist
func HandleRemoveWhitelist(c *gin.Context) {
	whitelistManager := GetIPWhitelistManager()
	if whitelistManager == nil {
		respondError(c, http.StatusInternalServerError, "白名单管理器未初始化")
		return
	}

	var req RemoveWhitelistRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "无效的请求体: %v", err)
		return
	}

	if err := whitelistManager.RemoveIP(req.IP); err != nil {
		respondError(c, http.StatusBadRequest, "移除白名单失败: %v", err)
		return
	}

	logger.Info("从白名单移除 IP",
		logger.String("ip", req.IP),
		logger.String("request_id", c.GetString("request_id")))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "IP 已从白名单移除",
	})
}

// HandleGetIPv6BlockStatus 获取 IPv6 禁止状态
// GET /v1/admin/ip/ipv6-block
func HandleGetIPv6BlockStatus(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"enabled": IsIPv6BlockEnabled(),
		},
	})
}

// SetIPv6BlockRequest IPv6 禁止设置请求
type SetIPv6BlockRequest struct {
	Enabled bool `json:"enabled"`
}

// HandleSetIPv6Block 设置 IPv6 禁止状态
// POST /v1/admin/ip/ipv6-block
func HandleSetIPv6Block(c *gin.Context) {
	var req SetIPv6BlockRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "无效的请求体: %v", err)
		return
	}

	SetIPv6BlockEnabled(req.Enabled)

	logger.Info("IPv6 禁止状态已更新",
		logger.Bool("enabled", req.Enabled),
		logger.String("request_id", c.GetString("request_id")))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "IPv6 禁止状态已更新",
		"data": gin.H{
			"enabled": req.Enabled,
		},
	})
}
