/**
 * Token Dashboard - 前端控制器
 * 基于模块化设计，遵循单一职责原则
 */

class TokenDashboard {
    constructor() {
        this.autoRefreshInterval = null;
        this.isAutoRefreshEnabled = false;
        this.apiBaseUrl = '/api';
        this.adminApiBaseUrl = '/v1/admin';
        this.adminToken = null;
        this.isAdminMode = false;

        this.init();
    }

    /**
     * 初始化Dashboard
     */
    init() {
        this.loadAdminToken();
        this.bindEvents();
        this.refreshTokens();
    }

    /**
     * 绑定事件处理器 (DRY原则)
     */
    bindEvents() {
        // 手动刷新按钮
        const refreshBtn = document.querySelector('.refresh-btn');
        if (refreshBtn) {
            refreshBtn.addEventListener('click', () => this.refreshTokens());
        }

        // 自动刷新开关
        const switchEl = document.querySelector('.switch');
        if (switchEl) {
            switchEl.addEventListener('click', () => this.toggleAutoRefresh());
        }

        // 管理员认证按钮
        const authBtn = document.getElementById('authBtn');
        if (authBtn) {
            authBtn.addEventListener('click', () => this.enableAdminMode());
        }

        // 退出管理按钮
        const logoutBtn = document.getElementById('logoutBtn');
        if (logoutBtn) {
            logoutBtn.addEventListener('click', () => this.disableAdminMode());
        }

        // 导出全部配置按钮
        const exportAllBtn = document.getElementById('exportAllBtn');
        if (exportAllBtn) {
            exportAllBtn.addEventListener('click', () => this.exportAllTokens());
        }

        // 批量删除失效Token按钮
        const deleteInvalidBtn = document.getElementById('deleteInvalidBtn');
        if (deleteInvalidBtn) {
            deleteInvalidBtn.addEventListener('click', () => this.deleteInvalidTokens());
        }
    }

    /**
     * 获取Token数据 - 简单直接 (KISS原则)
     */
    async refreshTokens() {
        const tbody = document.getElementById('tokenTableBody');
        this.showLoading(tbody, '正在刷新Token数据...');
        
        try {
            const response = await fetch(`${this.apiBaseUrl}/tokens`);
            if (!response.ok) {
                throw new Error(`HTTP ${response.status}: ${response.statusText}`);
            }
            
            const data = await response.json();
            this.updateTokenTable(data);
            this.updateStatusBar(data);
            this.updateLastUpdateTime();
            
        } catch (error) {
            console.error('刷新Token数据失败:', error);
            this.showError(tbody, `加载失败: ${error.message}`);
        }
    }

    /**
     * 更新Token表格 (OCP原则 - 易于扩展新字段)
     */
    updateTokenTable(data) {
        const tbody = document.getElementById('tokenTableBody');
        
        if (!data.tokens || data.tokens.length === 0) {
            this.showError(tbody, '暂无Token数据');
            return;
        }
        
        const rows = data.tokens.map(token => this.createTokenRow(token)).join('');
        tbody.innerHTML = rows;
    }

    /**
     * 创建单个Token行 (SRP原则)
     */
    createTokenRow(token) {
        const statusClass = this.getStatusClass(token);
        const statusText = this.getStatusText(token);
        const isInvalid = token.status === 'error' || token.is_invalid;

        // 管理操作按钮
        let actionButtons = '';
        if (this.isAdminMode) {
            actionButtons = `
                <td class="admin-only action-cell">
                    <button class="action-btn-small export-btn-small" onclick="dashboard.exportSingleToken(${token.index})" title="导出配置">
                        📥
                    </button>
                    ${isInvalid ? `
                        <button class="action-btn-small delete-btn-small" onclick="dashboard.deleteSingleToken(${token.index})" title="删除失效Token">
                            🗑️
                        </button>
                    ` : ''}
                </td>
            `;
        }

        return `
            <tr class="${isInvalid ? 'invalid-row' : ''}">
                <td>${token.user_email || 'unknown'}</td>
                <td><span class="token-preview">${token.token_preview || 'N/A'}</span></td>
                <td>${token.auth_type || 'social'}</td>
                <td>${token.remaining_usage || 0}</td>
                <td>${this.formatDateTime(token.expires_at)}</td>
                <td>${this.formatDateTime(token.last_used)}</td>
                <td><span class="status-badge ${statusClass}">${statusText}</span></td>
                ${actionButtons}
            </tr>
        `;
    }

    /**
     * 更新状态栏 (SRP原则)
     */
    updateStatusBar(data) {
        this.updateElement('totalTokens', data.total_tokens || 0);
        this.updateElement('activeTokens', data.active_tokens || 0);
    }

    /**
     * 更新最后更新时间
     */
    updateLastUpdateTime() {
        const now = new Date();
        const timeStr = now.toLocaleTimeString('zh-CN', { hour12: false });
        this.updateElement('lastUpdate', timeStr);
    }

    /**
     * 切换自动刷新 (ISP原则 - 接口隔离)
     */
    toggleAutoRefresh() {
        const switchEl = document.querySelector('.switch');
        
        if (this.isAutoRefreshEnabled) {
            this.stopAutoRefresh();
            switchEl.classList.remove('active');
        } else {
            this.startAutoRefresh();
            switchEl.classList.add('active');
        }
    }

    /**
     * 启动自动刷新
     */
    startAutoRefresh() {
        this.autoRefreshInterval = setInterval(() => this.refreshTokens(), 30000);
        this.isAutoRefreshEnabled = true;
    }

    /**
     * 停止自动刷新
     */
    stopAutoRefresh() {
        if (this.autoRefreshInterval) {
            clearInterval(this.autoRefreshInterval);
            this.autoRefreshInterval = null;
        }
        this.isAutoRefreshEnabled = false;
    }

    /**
     * 工具方法 - 状态判断 (KISS原则)
     */
    getStatusClass(token) {
        if (new Date(token.expires_at) < new Date()) {
            return 'status-expired';
        }
        const remaining = token.remaining_usage || 0;
        if (remaining === 0) return 'status-exhausted';
        if (remaining <= 5) return 'status-low';
        return 'status-active';
    }

    getStatusText(token) {
        if (new Date(token.expires_at) < new Date()) {
            return '已过期';
        }
        const remaining = token.remaining_usage || 0;
        if (remaining === 0) return '已耗尽';
        if (remaining <= 5) return '即将耗尽';
        return '正常';
    }

    /**
     * 工具方法 - 日期格式化 (DRY原则)
     */
    formatDateTime(dateStr) {
        if (!dateStr) return '-';
        
        try {
            const date = new Date(dateStr);
            if (isNaN(date.getTime())) return '-';
            
            return date.toLocaleString('zh-CN', {
                year: 'numeric',
                month: '2-digit',
                day: '2-digit',
                hour: '2-digit',
                minute: '2-digit',
                hour12: false
            });
        } catch (e) {
            return '-';
        }
    }

    /**
     * UI工具方法 (KISS原则)
     */
    updateElement(id, content) {
        const element = document.getElementById(id);
        if (element) element.textContent = content;
    }

    showLoading(container, message) {
        container.innerHTML = `
            <tr>
                <td colspan="7" class="loading">
                    <div class="spinner"></div>
                    ${message}
                </td>
            </tr>
        `;
    }

    showError(container, message) {
        container.innerHTML = `
            <tr>
                <td colspan="${this.isAdminMode ? '8' : '7'}" class="error">
                    ${message}
                </td>
            </tr>
        `;
    }

    /**
     * 管理员功能 - 加载保存的Token
     */
    loadAdminToken() {
        const savedToken = localStorage.getItem('kiro_admin_token');
        if (savedToken) {
            this.adminToken = savedToken;
            this.isAdminMode = true;
            this.updateAdminUI();
        }
    }

    /**
     * 启用管理模式
     */
    async enableAdminMode() {
        const tokenInput = document.getElementById('adminToken');
        const token = tokenInput.value.trim();

        if (!token) {
            this.showAuthStatus('请输入管理员 Token', 'error');
            return;
        }

        // 验证Token
        try {
            const response = await fetch(`${this.adminApiBaseUrl}/tokens`, {
                headers: {
                    'Authorization': `Bearer ${token}`
                }
            });

            if (response.ok) {
                this.adminToken = token;
                this.isAdminMode = true;
                localStorage.setItem('kiro_admin_token', token);
                this.updateAdminUI();
                this.showAuthStatus('管理模式已启用', 'success');
                this.refreshTokens();
            } else {
                this.showAuthStatus('Token 验证失败，请检查是否正确', 'error');
            }
        } catch (error) {
            this.showAuthStatus('验证失败: ' + error.message, 'error');
        }
    }

    /**
     * 退出管理模式
     */
    disableAdminMode() {
        this.adminToken = null;
        this.isAdminMode = false;
        localStorage.removeItem('kiro_admin_token');
        document.getElementById('adminToken').value = '';
        this.updateAdminUI();
        this.showAuthStatus('已退出管理模式', 'info');
        this.refreshTokens();
    }

    /**
     * 更新管理UI显示
     */
    updateAdminUI() {
        const authBtn = document.getElementById('authBtn');
        const logoutBtn = document.getElementById('logoutBtn');
        const adminActions = document.getElementById('adminActions');
        const adminOnlyElements = document.querySelectorAll('.admin-only');

        if (this.isAdminMode) {
            authBtn.style.display = 'none';
            logoutBtn.style.display = 'inline-block';
            adminActions.style.display = 'flex';
            adminOnlyElements.forEach(el => el.style.display = '');
        } else {
            authBtn.style.display = 'inline-block';
            logoutBtn.style.display = 'none';
            adminActions.style.display = 'none';
            adminOnlyElements.forEach(el => el.style.display = 'none');
        }
    }

    /**
     * 显示认证状态消息
     */
    showAuthStatus(message, type) {
        const statusEl = document.getElementById('authStatus');
        statusEl.textContent = message;
        statusEl.className = `auth-status ${type}`;

        setTimeout(() => {
            statusEl.textContent = '';
            statusEl.className = 'auth-status';
        }, 3000);
    }

    /**
     * 导出全部Token配置
     */
    async exportAllTokens() {
        if (!this.adminToken) return;

        try {
            const response = await fetch(`${this.adminApiBaseUrl}/tokens/export`, {
                method: 'POST',
                headers: {
                    'Authorization': `Bearer ${this.adminToken}`,
                    'Content-Type': 'application/json'
                },
                body: JSON.stringify({})
            });

            if (!response.ok) {
                throw new Error(`HTTP ${response.status}`);
            }

            const data = await response.json();

            // 下载为JSON文件
            const blob = new Blob([JSON.stringify(data.data.configs, null, 2)], {
                type: 'application/json'
            });
            const url = URL.createObjectURL(blob);
            const a = document.createElement('a');
            a.href = url;
            a.download = `kiro_tokens_${new Date().toISOString().split('T')[0]}.json`;
            a.click();
            URL.revokeObjectURL(url);

            this.showAuthStatus(`已导出 ${data.data.count} 个配置`, 'success');
        } catch (error) {
            this.showAuthStatus('导出失败: ' + error.message, 'error');
        }
    }

    /**
     * 批量删除失效Token
     */
    async deleteInvalidTokens() {
        if (!this.adminToken) return;

        if (!confirm('确定要删除所有失效的 Token 吗？此操作不可撤销！')) {
            return;
        }

        try {
            const response = await fetch(`${this.adminApiBaseUrl}/tokens/invalid`, {
                method: 'DELETE',
                headers: {
                    'Authorization': `Bearer ${this.adminToken}`
                }
            });

            if (!response.ok) {
                throw new Error(`HTTP ${response.status}`);
            }

            const data = await response.json();
            this.showAuthStatus(`已删除 ${data.data.removed_count} 个失效 Token`, 'success');
            this.refreshTokens();
        } catch (error) {
            this.showAuthStatus('删除失败: ' + error.message, 'error');
        }
    }

    /**
     * 删除单个Token
     */
    async deleteSingleToken(index) {
        if (!this.adminToken) return;

        if (!confirm(`确定要删除索引为 ${index} 的失效 Token 吗？`)) {
            return;
        }

        try {
            const response = await fetch(`${this.adminApiBaseUrl}/tokens/${index}`, {
                method: 'DELETE',
                headers: {
                    'Authorization': `Bearer ${this.adminToken}`
                }
            });

            if (!response.ok) {
                const error = await response.json();
                throw new Error(error.error?.message || `HTTP ${response.status}`);
            }

            this.showAuthStatus('Token 已删除', 'success');
            this.refreshTokens();
        } catch (error) {
            this.showAuthStatus('删除失败: ' + error.message, 'error');
        }
    }

    /**
     * 导出单个Token配置
     */
    async exportSingleToken(index) {
        if (!this.adminToken) return;

        try {
            const response = await fetch(`${this.adminApiBaseUrl}/tokens/export`, {
                method: 'POST',
                headers: {
                    'Authorization': `Bearer ${this.adminToken}`,
                    'Content-Type': 'application/json'
                },
                body: JSON.stringify({ indices: [index] })
            });

            if (!response.ok) {
                throw new Error(`HTTP ${response.status}`);
            }

            const data = await response.json();

            // 下载为JSON文件
            const blob = new Blob([JSON.stringify(data.data.configs, null, 2)], {
                type: 'application/json'
            });
            const url = URL.createObjectURL(blob);
            const a = document.createElement('a');
            a.href = url;
            a.download = `kiro_token_${index}_${new Date().toISOString().split('T')[0]}.json`;
            a.click();
            URL.revokeObjectURL(url);

            this.showAuthStatus('配置已导出', 'success');
        } catch (error) {
            this.showAuthStatus('导出失败: ' + error.message, 'error');
        }
    }
}

// DOM加载完成后初始化 (依赖注入原则)
let dashboard;
document.addEventListener('DOMContentLoaded', () => {
    dashboard = new TokenDashboard();
});