/**
 * Token Dashboard - 前端控制器
 * 支持登录认证、Token 管理、代理管理
 */

class TokenDashboard {
    constructor() {
        this.autoRefreshInterval = null;
        this.isAutoRefreshEnabled = false;
        this.apiBaseUrl = '/api';
        this.adminApiBaseUrl = '/v1/admin';
        this.adminToken = null;
        this.isDefaultAdminToken = false;
        this.isDefaultClientToken = false;

        this.init();
    }

    init() {
        this.checkSavedLogin();
        this.bindEvents();
        this.currentAddMode = 'single';
        this.currentImportType = 'text';
        this.selectedFile = null;
    }

    bindEvents() {
        // 登录按钮
        document.getElementById('loginBtn')?.addEventListener('click', () => this.login());
        document.getElementById('loginToken')?.addEventListener('keypress', (e) => {
            if (e.key === 'Enter') this.login();
        });

        // 退出登录
        document.getElementById('headerLogoutBtn')?.addEventListener('click', () => this.logout());

        // 导航标签
        document.querySelectorAll('.nav-tab').forEach(tab => {
            tab.addEventListener('click', () => this.switchTab(tab.dataset.tab));
        });

        // Token 管理
        document.getElementById('refreshTokensBtn')?.addEventListener('click', () => this.refreshTokens());
        document.getElementById('addTokenBtn')?.addEventListener('click', () => this.openModal('addTokenModal'));
        document.getElementById('autoRefreshSwitch')?.addEventListener('click', () => this.toggleAutoRefresh());
        document.getElementById('exportAllBtn')?.addEventListener('click', () => this.exportAllTokens());
        document.getElementById('refreshAllBtn')?.addEventListener('click', () => this.refreshAllTokens());
        document.getElementById('deleteInvalidBtn')?.addEventListener('click', () => this.deleteInvalidTokens());

        // 代理管理
        document.getElementById('refreshProxiesBtn')?.addEventListener('click', () => this.refreshProxies());
        document.getElementById('addProxyBtn')?.addEventListener('click', () => this.openModal('addProxyModal'));

        // IP 监控
        document.getElementById('refreshIPStatsBtn')?.addEventListener('click', () => this.refreshIPStats());
        document.getElementById('addWhitelistBtn')?.addEventListener('click', () => this.openModal('addWhitelistModal'));
        document.getElementById('ipv6BlockToggle')?.addEventListener('change', (e) => this.toggleIPv6Block(e.target.checked));

        // 添加 Token 表单
        document.getElementById('newTokenAuthType')?.addEventListener('change', (e) => {
            document.getElementById('idcFields').style.display = e.target.value === 'IdC' ? 'block' : 'none';
        });

        // 点击弹窗外部关闭
        document.querySelectorAll('.modal').forEach(modal => {
            modal.addEventListener('click', (e) => {
                if (e.target === modal) this.closeModal(modal.id);
            });
        });
    }

    // ==================== 登录相关 ====================

    checkSavedLogin() {
        const savedToken = localStorage.getItem('kiro_admin_token');
        if (savedToken) {
            this.adminToken = savedToken;
            this.verifyToken();
        }
    }

    async login() {
        const tokenInput = document.getElementById('loginToken');
        const token = tokenInput.value.trim();
        const statusEl = document.getElementById('loginStatus');

        if (!token) {
            this.showLoginStatus('请输入管理员密码', 'error');
            return;
        }

        this.showLoginStatus('正在验证...', '');

        try {
            const response = await fetch(`${this.adminApiBaseUrl}/tokens`, {
                headers: { 'Authorization': `Bearer ${token}` }
            });

            if (response.ok) {
                this.adminToken = token;
                localStorage.setItem('kiro_admin_token', token);
                this.showLoginStatus('登录成功', 'success');
                setTimeout(() => this.showMainPage(), 500);
            } else {
                this.showLoginStatus('密码错误，请重试', 'error');
            }
        } catch (error) {
            this.showLoginStatus('连接失败: ' + error.message, 'error');
        }
    }

    async verifyToken() {
        try {
            const response = await fetch(`${this.adminApiBaseUrl}/tokens`, {
                headers: { 'Authorization': `Bearer ${this.adminToken}` }
            });

            if (response.ok) {
                this.showMainPage();
            } else {
                this.logout();
            }
        } catch (error) {
            this.logout();
        }
    }

    logout() {
        this.adminToken = null;
        localStorage.removeItem('kiro_admin_token');
        this.stopAutoRefresh();
        document.getElementById('loginPage').style.display = 'flex';
        document.getElementById('mainPage').style.display = 'none';
        document.getElementById('loginToken').value = '';
    }

    showLoginStatus(message, type) {
        const statusEl = document.getElementById('loginStatus');
        statusEl.textContent = message;
        statusEl.className = 'login-status ' + type;
    }

    async showMainPage() {
        document.getElementById('loginPage').style.display = 'none';
        document.getElementById('mainPage').style.display = 'block';

        // 检查系统状态
        await this.checkSystemStatus();

        // 加载数据
        this.refreshTokens();
    }

    async checkSystemStatus() {
        try {
            const response = await fetch(`${this.adminApiBaseUrl}/status`, {
                headers: { 'Authorization': `Bearer ${this.adminToken}` }
            });

            if (response.ok) {
                const data = await response.json();
                this.isDefaultAdminToken = data.data.is_default_admin_token;
                this.isDefaultClientToken = data.data.is_default_client_token;

                // 显示安全警告
                if (this.isDefaultAdminToken || this.isDefaultClientToken) {
                    const warningEl = document.getElementById('securityWarning');
                    const messageEl = document.getElementById('warningMessage');

                    let warnings = [];
                    if (this.isDefaultAdminToken) warnings.push('管理员密码');
                    if (this.isDefaultClientToken) warnings.push('客户端密码');

                    messageEl.textContent = `您正在使用默认的 ${warnings.join(' 和 ')}，请尽快在 .env 文件中修改以确保安全。`;
                    warningEl.style.display = 'flex';
                }

                // 更新设置页面状态
                document.getElementById('adminTokenStatus').className =
                    'status-badge ' + (this.isDefaultAdminToken ? 'status-default' : 'status-active');
                document.getElementById('adminTokenStatus').textContent =
                    this.isDefaultAdminToken ? '使用默认值' : '已配置';

                document.getElementById('clientTokenStatus').className =
                    'status-badge ' + (this.isDefaultClientToken ? 'status-default' : 'status-active');
                document.getElementById('clientTokenStatus').textContent =
                    this.isDefaultClientToken ? '使用默认值' : '已配置';
            }
        } catch (error) {
            console.error('检查系统状态失败:', error);
        }
    }

    // ==================== 标签切换 ====================

    switchTab(tabName) {
        // 更新标签按钮状态
        document.querySelectorAll('.nav-tab').forEach(tab => {
            tab.classList.toggle('active', tab.dataset.tab === tabName);
        });

        // 更新内容显示
        document.querySelectorAll('.tab-content').forEach(content => {
            content.classList.toggle('active', content.id === tabName + 'Tab');
        });

        // 加载对应数据
        if (tabName === 'tokens') {
            this.refreshTokens();
        } else if (tabName === 'proxies') {
            this.refreshProxies();
        } else if (tabName === 'ip') {
            this.refreshIPStats();
        }
    }

    // ==================== Token 管理 ====================

    async refreshTokens() {
        const tbody = document.getElementById('tokenTableBody');
        this.showLoading(tbody, '正在加载Token数据...', 8);

        try {
            const response = await fetch(`${this.adminApiBaseUrl}/tokens`, {
                headers: { 'Authorization': `Bearer ${this.adminToken}` }
            });

            if (!response.ok) throw new Error(`HTTP ${response.status}`);

            const data = await response.json();
            this.updateTokenTable(data.data.tokens || []);
            this.updateStatusBar(data.data.tokens || []);
            this.updateLastUpdateTime();
        } catch (error) {
            console.error('刷新Token数据失败:', error);
            this.showError(tbody, `加载失败: ${error.message}`, 8);
        }
    }

    updateTokenTable(tokens) {
        const tbody = document.getElementById('tokenTableBody');

        if (!tokens || tokens.length === 0) {
            tbody.innerHTML = `
                <tr>
                    <td colspan="8" class="empty-state">
                        <p>暂无 Token 数据</p>
                        <button class="btn btn-primary" onclick="dashboard.openModal('addTokenModal')">➕ 添加第一个 Token</button>
                    </td>
                </tr>
            `;
            return;
        }

        const rows = tokens.map(token => this.createTokenRow(token)).join('');
        tbody.innerHTML = rows;
    }

    createTokenRow(token) {
        const statusClass = this.getStatusClass(token);
        const statusText = this.getStatusText(token);
        const isInvalid = token.is_invalid;

        return `
            <tr class="${isInvalid ? 'invalid-row' : ''}">
                <td>${token.index}</td>
                <td><span class="token-preview">${token.refresh_token_preview || 'N/A'}</span></td>
                <td>${token.auth_type || 'Social'}</td>
                <td>${token.available?.toFixed(1) || 0}</td>
                <td>${this.formatDateTime(token.next_reset_date)}</td>
                <td>${this.formatDateTime(token.last_used)}</td>
                <td><span class="status-badge ${statusClass}">${statusText}</span></td>
                <td class="action-cell">
                    <button class="action-btn-small refresh-btn-small" onclick="dashboard.refreshSingleToken(${token.index})" title="刷新状态">🔄</button>
                    <button class="action-btn-small export-btn-small" onclick="dashboard.exportSingleToken(${token.index})" title="导出配置">📥</button>
                    ${isInvalid ? `<button class="action-btn-small delete-btn-small" onclick="dashboard.deleteSingleToken(${token.index})" title="删除">🗑️</button>` : ''}
                </td>
            </tr>
        `;
    }

    updateStatusBar(tokens) {
        const total = tokens.length;
        const active = tokens.filter(t => !t.is_invalid && !t.disabled && t.available > 0).length;

        document.getElementById('totalTokens').textContent = total;
        document.getElementById('activeTokens').textContent = active;
    }

    updateLastUpdateTime() {
        const now = new Date();
        document.getElementById('lastUpdate').textContent = now.toLocaleTimeString('zh-CN', { hour12: false });
    }

    getStatusClass(token) {
        if (token.refresh_status === 'not_refreshed') return 'status-not-refreshed';
        if (token.refresh_status === 'invalid' || token.is_invalid) return 'status-invalid';
        if (token.disabled) return 'status-exhausted';

        const available = token.available || 0;
        if (available === 0) return 'status-exhausted';
        if (available <= 5) return 'status-low';
        return 'status-active';
    }

    getStatusText(token) {
        if (token.refresh_status === 'not_refreshed') return '未刷新';
        if (token.refresh_status === 'invalid' || token.is_invalid) return '失效';
        if (token.disabled) return '已禁用';

        const available = token.available || 0;
        if (available === 0) return '已耗尽';
        if (available <= 5) return '即将耗尽';
        return '正常';
    }

    // ==================== 代理管理 ====================

    async refreshProxies() {
        const tbody = document.getElementById('proxyTableBody');
        this.showLoading(tbody, '正在加载代理数据...', 7);

        try {
            const response = await fetch(`${this.adminApiBaseUrl}/proxies`, {
                headers: { 'Authorization': `Bearer ${this.adminToken}` }
            });

            if (!response.ok) throw new Error(`HTTP ${response.status}`);

            const data = await response.json();
            this.updateProxyTable(data.data.proxies || []);
        } catch (error) {
            console.error('刷新代理数据失败:', error);
            this.showError(tbody, `加载失败: ${error.message}`, 7);
        }
    }

    updateProxyTable(proxies) {
        const tbody = document.getElementById('proxyTableBody');

        if (!proxies || proxies.length === 0) {
            tbody.innerHTML = `
                <tr>
                    <td colspan="7" class="empty-state">
                        <p>暂无代理配置</p>
                        <button class="btn btn-primary" onclick="dashboard.openModal('addProxyModal')">➕ 添加代理</button>
                    </td>
                </tr>
            `;
            return;
        }

        const rows = proxies.map(proxy => this.createProxyRow(proxy)).join('');
        tbody.innerHTML = rows;
    }

    createProxyRow(proxy) {
        return `
            <tr>
                <td>${proxy.index}</td>
                <td><span class="token-preview">${proxy.url}</span></td>
                <td><span class="status-badge ${proxy.healthy ? 'status-healthy' : 'status-unhealthy'}">${proxy.healthy ? '健康' : '不健康'}</span></td>
                <td>${proxy.failure_count}</td>
                <td>${proxy.assigned_count}</td>
                <td>${this.formatDateTime(proxy.last_check)}</td>
                <td class="action-cell">
                    <button class="action-btn-small delete-btn-small" onclick="dashboard.deleteProxy(${proxy.index})" title="删除">🗑️</button>
                </td>
            </tr>
        `;
    }

    // ==================== 操作方法 ====================

    async submitAddToken() {
        if (this.currentAddMode === 'import') {
            await this.submitImportTokens();
        } else {
            await this.submitSingleToken();
        }
    }

    async submitSingleToken() {
        const authType = document.getElementById('newTokenAuthType').value;
        const refreshToken = document.getElementById('newTokenRefreshToken').value.trim();
        const clientId = document.getElementById('newTokenClientId')?.value.trim() || '';
        const clientSecret = document.getElementById('newTokenClientSecret')?.value.trim() || '';

        if (!refreshToken) {
            this.showNotification('请输入 Refresh Token', 'error');
            return;
        }

        if (authType === 'IdC' && (!clientId || !clientSecret)) {
            this.showNotification('IdC 认证需要 Client ID 和 Client Secret', 'error');
            return;
        }

        try {
            const response = await fetch(`${this.adminApiBaseUrl}/tokens/add`, {
                method: 'POST',
                headers: {
                    'Authorization': `Bearer ${this.adminToken}`,
                    'Content-Type': 'application/json'
                },
                body: JSON.stringify({
                    auth: authType,
                    refreshToken: refreshToken,
                    clientId: clientId,
                    clientSecret: clientSecret
                })
            });

            if (!response.ok) {
                const error = await response.json();
                throw new Error(error.error?.message || `HTTP ${response.status}`);
            }

            this.showNotification('Token 添加成功', 'success');
            this.closeModal('addTokenModal');
            this.clearAddTokenForm();
            this.refreshTokens();
        } catch (error) {
            this.showNotification('添加失败: ' + error.message, 'error');
        }
    }

    async submitImportTokens() {
        let jsonContent = '';

        // 获取 JSON 内容
        if (this.currentImportType === 'text') {
            jsonContent = document.getElementById('importJsonText').value.trim();
            if (!jsonContent) {
                this.showNotification('请输入 JSON 内容', 'error');
                return;
            }
        } else {
            if (!this.selectedFile) {
                this.showNotification('请选择文件', 'error');
                return;
            }

            // 读取文件内容
            try {
                this.showImportProgress('正在读取文件...', 10);
                jsonContent = await this.readFileAsText(this.selectedFile);
            } catch (error) {
                this.showNotification('读取文件失败: ' + error.message, 'error');
                this.hideImportProgress();
                return;
            }
        }

        // 解析 JSON
        let tokens;
        try {
            this.showImportProgress('正在解析 JSON...', 20);
            tokens = JSON.parse(jsonContent);

            // 支持直接数组或 { tokens: [...] } 格式
            if (tokens.tokens && Array.isArray(tokens.tokens)) {
                tokens = tokens.tokens;
            }

            if (!Array.isArray(tokens)) {
                throw new Error('JSON 必须是数组格式');
            }

            if (tokens.length === 0) {
                throw new Error('数组不能为空');
            }
        } catch (error) {
            this.showNotification('JSON 解析失败: ' + error.message, 'error');
            this.hideImportProgress();
            return;
        }

        // 显示确认
        const confirmMsg = `确定要导入 ${tokens.length} 个 Token 吗？`;
        if (!confirm(confirmMsg)) {
            this.hideImportProgress();
            return;
        }

        // 大数据量分批导入
        const batchSize = 100;
        const totalBatches = Math.ceil(tokens.length / batchSize);
        let totalSuccess = 0;
        let totalSkipped = 0;
        let totalFailed = 0;
        let allErrors = [];

        this.showImportProgress(`正在导入 0/${tokens.length}...`, 30);

        for (let i = 0; i < totalBatches; i++) {
            const start = i * batchSize;
            const end = Math.min(start + batchSize, tokens.length);
            const batch = tokens.slice(start, end);

            try {
                const response = await fetch(`${this.adminApiBaseUrl}/tokens/import`, {
                    method: 'POST',
                    headers: {
                        'Authorization': `Bearer ${this.adminToken}`,
                        'Content-Type': 'application/json'
                    },
                    body: JSON.stringify({ tokens: batch })
                });

                if (!response.ok) {
                    const error = await response.json();
                    throw new Error(error.error?.message || `HTTP ${response.status}`);
                }

                const result = await response.json();
                totalSuccess += result.data.success || 0;
                totalSkipped += result.data.skipped || 0;
                totalFailed += result.data.failed || 0;
                if (result.data.errors) {
                    allErrors = allErrors.concat(result.data.errors);
                }

                // 更新进度
                const progress = 30 + Math.round((end / tokens.length) * 70);
                this.showImportProgress(`正在导入 ${end}/${tokens.length}...`, progress);

            } catch (error) {
                totalFailed += batch.length;
                allErrors.push(`批次 ${i + 1} 导入失败: ${error.message}`);
            }
        }

        // 显示结果
        this.hideImportProgress();
        this.showImportResult(totalSuccess, totalSkipped, totalFailed, allErrors);

        // 构建提示消息
        let msg = `导入完成：成功 ${totalSuccess} 个`;
        if (totalSkipped > 0) msg += `，跳过 ${totalSkipped} 个重复`;
        if (totalFailed > 0) msg += `，失败 ${totalFailed} 个`;

        if (totalSuccess > 0 || totalSkipped > 0) {
            this.showNotification(msg, totalFailed === 0 ? 'success' : 'info');
            if (totalSuccess > 0) this.refreshTokens();
        } else {
            this.showNotification('导入失败，请检查数据格式', 'error');
        }
    }

    readFileAsText(file) {
        return new Promise((resolve, reject) => {
            const reader = new FileReader();
            reader.onload = (e) => resolve(e.target.result);
            reader.onerror = (e) => reject(new Error('文件读取失败'));
            reader.readAsText(file);
        });
    }

    clearAddTokenForm() {
        document.getElementById('newTokenAuthType').value = 'Social';
        document.getElementById('newTokenRefreshToken').value = '';
        document.getElementById('newTokenClientId').value = '';
        document.getElementById('newTokenClientSecret').value = '';
        document.getElementById('idcFields').style.display = 'none';
        document.getElementById('importJsonText').value = '';
        this.clearSelectedFile();
        this.hideImportProgress();
        this.hideImportResult();
        this.switchAddMode('single');
    }

    // ==================== 添加模式切换 ====================

    switchAddMode(mode) {
        this.currentAddMode = mode;

        // 更新标签状态
        document.querySelectorAll('.add-mode-tab').forEach(tab => {
            tab.classList.toggle('active', tab.dataset.mode === mode);
        });

        // 更新内容显示
        document.getElementById('singleAddMode').classList.toggle('active', mode === 'single');
        document.getElementById('importMode').classList.toggle('active', mode === 'import');

        // 更新按钮文字
        const submitBtn = document.getElementById('addTokenSubmitBtn');
        submitBtn.textContent = mode === 'single' ? '添加' : '导入';
    }

    switchImportType(type) {
        this.currentImportType = type;
        document.getElementById('importTextArea').style.display = type === 'text' ? 'block' : 'none';
        document.getElementById('importFileArea').style.display = type === 'file' ? 'block' : 'none';

        // 初始化文件拖拽
        if (type === 'file') {
            this.initFileDropZone();
        }
    }

    initFileDropZone() {
        const dropZone = document.getElementById('fileDropZone');
        if (!dropZone || dropZone.dataset.initialized) return;

        dropZone.dataset.initialized = 'true';

        dropZone.addEventListener('click', () => {
            document.getElementById('importFile').click();
        });

        dropZone.addEventListener('dragover', (e) => {
            e.preventDefault();
            dropZone.classList.add('dragover');
        });

        dropZone.addEventListener('dragleave', () => {
            dropZone.classList.remove('dragover');
        });

        dropZone.addEventListener('drop', (e) => {
            e.preventDefault();
            dropZone.classList.remove('dragover');
            const files = e.dataTransfer.files;
            if (files.length > 0) {
                this.processSelectedFile(files[0]);
            }
        });
    }

    handleFileSelect(event) {
        const file = event.target.files[0];
        if (file) {
            this.processSelectedFile(file);
        }
    }

    processSelectedFile(file) {
        // 验证文件类型
        if (!file.name.endsWith('.json')) {
            this.showNotification('请选择 .json 文件', 'error');
            return;
        }

        // 验证文件大小（100MB）
        const maxSize = 100 * 1024 * 1024;
        if (file.size > maxSize) {
            this.showNotification('文件大小不能超过 100MB', 'error');
            return;
        }

        this.selectedFile = file;

        // 显示已选文件
        document.querySelector('.file-upload-content').style.display = 'none';
        document.getElementById('fileSelected').style.display = 'flex';
        document.getElementById('selectedFileName').textContent = file.name;
    }

    clearSelectedFile() {
        this.selectedFile = null;
        document.getElementById('importFile').value = '';
        const uploadContent = document.querySelector('.file-upload-content');
        if (uploadContent) uploadContent.style.display = 'block';
        const fileSelected = document.getElementById('fileSelected');
        if (fileSelected) fileSelected.style.display = 'none';
    }

    // ==================== 导入进度和结果 ====================

    showImportProgress(text, percent) {
        const progressEl = document.getElementById('importProgress');
        const fillEl = document.getElementById('progressFill');
        const textEl = document.getElementById('progressText');

        progressEl.style.display = 'block';
        fillEl.style.width = percent + '%';
        textEl.textContent = text;
    }

    hideImportProgress() {
        const progressEl = document.getElementById('importProgress');
        if (progressEl) progressEl.style.display = 'none';
    }

    showImportResult(success, skipped, failed, errors) {
        const resultEl = document.getElementById('importResult');
        const errorsEl = document.getElementById('resultErrors');
        const errorList = document.getElementById('errorList');

        document.getElementById('resultSuccess').textContent = success;
        document.getElementById('resultSkipped').textContent = skipped;
        document.getElementById('resultFailed').textContent = failed;

        if (errors && errors.length > 0) {
            errorList.innerHTML = errors.map(e => `<li>${e}</li>`).join('');
            errorsEl.style.display = 'block';
        } else {
            errorsEl.style.display = 'none';
        }

        resultEl.style.display = 'block';
    }

    hideImportResult() {
        const resultEl = document.getElementById('importResult');
        if (resultEl) resultEl.style.display = 'none';
    }

    async submitAddProxy() {
        const proxyUrl = document.getElementById('newProxyUrl').value.trim();

        if (!proxyUrl) {
            this.showNotification('请输入代理地址', 'error');
            return;
        }

        try {
            const response = await fetch(`${this.adminApiBaseUrl}/proxies/add`, {
                method: 'POST',
                headers: {
                    'Authorization': `Bearer ${this.adminToken}`,
                    'Content-Type': 'application/json'
                },
                body: JSON.stringify({ url: proxyUrl })
            });

            if (!response.ok) {
                const error = await response.json();
                throw new Error(error.error?.message || `HTTP ${response.status}`);
            }

            this.showNotification('代理添加成功', 'success');
            this.closeModal('addProxyModal');
            document.getElementById('newProxyUrl').value = '';
            this.refreshProxies();
        } catch (error) {
            this.showNotification('添加失败: ' + error.message, 'error');
        }
    }

    async refreshSingleToken(index) {
        this.showNotification(`正在刷新账号 ${index}...`, 'info');

        try {
            const response = await fetch(`${this.adminApiBaseUrl}/tokens/${index}/refresh`, {
                method: 'POST',
                headers: { 'Authorization': `Bearer ${this.adminToken}` }
            });

            if (!response.ok) throw new Error(`HTTP ${response.status}`);

            const data = await response.json();
            if (data.data.success) {
                this.showNotification(`账号 ${index} 刷新成功`, 'success');
            } else {
                this.showNotification(`账号 ${index} 刷新失败: ${data.data.error}`, 'error');
            }
            this.refreshTokens();
        } catch (error) {
            this.showNotification('刷新失败: ' + error.message, 'error');
        }
    }

    async exportSingleToken(index) {
        try {
            const response = await fetch(`${this.adminApiBaseUrl}/tokens/export`, {
                method: 'POST',
                headers: {
                    'Authorization': `Bearer ${this.adminToken}`,
                    'Content-Type': 'application/json'
                },
                body: JSON.stringify({ indices: [index] })
            });

            if (!response.ok) throw new Error(`HTTP ${response.status}`);

            const data = await response.json();
            this.downloadJSON(data.data.configs, `kiro_token_${index}.json`);
            this.showNotification('配置已导出', 'success');
        } catch (error) {
            this.showNotification('导出失败: ' + error.message, 'error');
        }
    }

    async deleteSingleToken(index) {
        if (!confirm(`确定要删除索引为 ${index} 的失效 Token 吗？`)) return;

        try {
            const response = await fetch(`${this.adminApiBaseUrl}/tokens/${index}`, {
                method: 'DELETE',
                headers: { 'Authorization': `Bearer ${this.adminToken}` }
            });

            if (!response.ok) {
                const error = await response.json();
                throw new Error(error.error?.message || `HTTP ${response.status}`);
            }

            this.showNotification('Token 已删除', 'success');
            this.refreshTokens();
        } catch (error) {
            this.showNotification('删除失败: ' + error.message, 'error');
        }
    }

    async exportAllTokens() {
        try {
            const response = await fetch(`${this.adminApiBaseUrl}/tokens/export`, {
                method: 'POST',
                headers: {
                    'Authorization': `Bearer ${this.adminToken}`,
                    'Content-Type': 'application/json'
                },
                body: JSON.stringify({})
            });

            if (!response.ok) throw new Error(`HTTP ${response.status}`);

            const data = await response.json();
            this.downloadJSON(data.data.configs, `kiro_tokens_${new Date().toISOString().split('T')[0]}.json`);
            this.showNotification(`已导出 ${data.data.count} 个配置`, 'success');
        } catch (error) {
            this.showNotification('导出失败: ' + error.message, 'error');
        }
    }

    async refreshAllTokens() {
        if (!confirm('确定要刷新所有账号的状态吗？这可能需要一些时间。')) return;

        this.showNotification('正在刷新账号状态...', 'info');

        try {
            const response = await fetch(`${this.adminApiBaseUrl}/tokens/refresh`, {
                method: 'POST',
                headers: {
                    'Authorization': `Bearer ${this.adminToken}`,
                    'Content-Type': 'application/json'
                },
                body: JSON.stringify({})
            });

            if (!response.ok) throw new Error(`HTTP ${response.status}`);

            const data = await response.json();
            this.showNotification(
                `刷新完成：成功 ${data.data.success} 个，失败 ${data.data.failed} 个`,
                data.data.failed === 0 ? 'success' : 'info'
            );
            this.refreshTokens();
        } catch (error) {
            this.showNotification('刷新失败: ' + error.message, 'error');
        }
    }

    async deleteInvalidTokens() {
        if (!confirm('确定要删除所有失效的 Token 吗？此操作不可撤销！')) return;

        try {
            const response = await fetch(`${this.adminApiBaseUrl}/tokens/invalid`, {
                method: 'DELETE',
                headers: { 'Authorization': `Bearer ${this.adminToken}` }
            });

            if (!response.ok) throw new Error(`HTTP ${response.status}`);

            const data = await response.json();
            this.showNotification(`已删除 ${data.data.removed_count} 个失效 Token`, 'success');
            this.refreshTokens();
        } catch (error) {
            this.showNotification('删除失败: ' + error.message, 'error');
        }
    }

    async deleteProxy(index) {
        if (!confirm(`确定要删除索引为 ${index} 的代理吗？`)) return;

        try {
            const response = await fetch(`${this.adminApiBaseUrl}/proxies/${index}`, {
                method: 'DELETE',
                headers: { 'Authorization': `Bearer ${this.adminToken}` }
            });

            if (!response.ok) {
                const error = await response.json();
                throw new Error(error.error?.message || `HTTP ${response.status}`);
            }

            this.showNotification('代理已删除', 'success');
            this.refreshProxies();
        } catch (error) {
            this.showNotification('删除失败: ' + error.message, 'error');
        }
    }

    // ==================== 密码修改 ====================

    async changeAdminPassword() {
        const newPassword = document.getElementById('newAdminPassword').value;
        const confirmPassword = document.getElementById('confirmAdminPassword').value;

        if (!newPassword) {
            this.showNotification('请输入新密码', 'error');
            return;
        }

        if (newPassword.length < 6) {
            this.showNotification('密码长度至少 6 位', 'error');
            return;
        }

        if (newPassword !== confirmPassword) {
            this.showNotification('两次输入的密码不一致', 'error');
            return;
        }

        if (!confirm('确定要修改管理员密码吗？修改后需要重新登录。')) {
            return;
        }

        try {
            const response = await fetch(`${this.adminApiBaseUrl}/password/admin`, {
                method: 'PUT',
                headers: {
                    'Authorization': `Bearer ${this.adminToken}`,
                    'Content-Type': 'application/json'
                },
                body: JSON.stringify({ newPassword })
            });

            if (!response.ok) {
                const error = await response.json();
                throw new Error(error.error?.message || `HTTP ${response.status}`);
            }

            this.showNotification('管理员密码已更新，请重新登录', 'success');

            // 清空输入框
            document.getElementById('newAdminPassword').value = '';
            document.getElementById('confirmAdminPassword').value = '';

            // 延迟后退出登录
            setTimeout(() => this.logout(), 2000);
        } catch (error) {
            this.showNotification('修改失败: ' + error.message, 'error');
        }
    }

    async changeClientPassword() {
        const newPassword = document.getElementById('newClientPassword').value;
        const confirmPassword = document.getElementById('confirmClientPassword').value;

        if (!newPassword) {
            this.showNotification('请输入新密码', 'error');
            return;
        }

        if (newPassword.length < 6) {
            this.showNotification('密码长度至少 6 位', 'error');
            return;
        }

        if (newPassword !== confirmPassword) {
            this.showNotification('两次输入的密码不一致', 'error');
            return;
        }

        if (!confirm('确定要修改客户端密码吗？')) {
            return;
        }

        try {
            const response = await fetch(`${this.adminApiBaseUrl}/password/client`, {
                method: 'PUT',
                headers: {
                    'Authorization': `Bearer ${this.adminToken}`,
                    'Content-Type': 'application/json'
                },
                body: JSON.stringify({ newPassword })
            });

            if (!response.ok) {
                const error = await response.json();
                throw new Error(error.error?.message || `HTTP ${response.status}`);
            }

            this.showNotification('客户端密码已更新', 'success');

            // 清空输入框
            document.getElementById('newClientPassword').value = '';
            document.getElementById('confirmClientPassword').value = '';

            // 刷新状态
            this.checkSystemStatus();
        } catch (error) {
            this.showNotification('修改失败: ' + error.message, 'error');
        }
    }

    // ==================== 自动刷新 ====================

    toggleAutoRefresh() {
        const switchEl = document.getElementById('autoRefreshSwitch');

        if (this.isAutoRefreshEnabled) {
            this.stopAutoRefresh();
            switchEl.classList.remove('active');
        } else {
            this.startAutoRefresh();
            switchEl.classList.add('active');
        }
    }

    startAutoRefresh() {
        this.autoRefreshInterval = setInterval(() => this.refreshTokens(), 30000);
        this.isAutoRefreshEnabled = true;
    }

    stopAutoRefresh() {
        if (this.autoRefreshInterval) {
            clearInterval(this.autoRefreshInterval);
            this.autoRefreshInterval = null;
        }
        this.isAutoRefreshEnabled = false;
    }

    // ==================== 工具方法 ====================

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

    showLoading(container, message, colspan) {
        container.innerHTML = `
            <tr>
                <td colspan="${colspan}" class="loading">
                    <div class="spinner"></div>
                    ${message}
                </td>
            </tr>
        `;
    }

    showError(container, message, colspan) {
        container.innerHTML = `
            <tr>
                <td colspan="${colspan}" class="error">${message}</td>
            </tr>
        `;
    }

    openModal(modalId) {
        document.getElementById(modalId).classList.add('active');
    }

    closeModal(modalId) {
        document.getElementById(modalId).classList.remove('active');
    }

    showNotification(message, type) {
        const notification = document.getElementById('notification');
        notification.textContent = message;
        notification.className = `notification ${type} show`;

        setTimeout(() => {
            notification.classList.remove('show');
        }, 3000);
    }

    downloadJSON(data, filename) {
        const blob = new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' });
        const url = URL.createObjectURL(blob);
        const a = document.createElement('a');
        a.href = url;
        a.download = filename;
        a.click();
        URL.revokeObjectURL(url);
    }

    // ==================== IP 监控 ====================

    async refreshIPStats() {
        const statsBody = document.getElementById('ipStatsTableBody');
        const whitelistBody = document.getElementById('whitelistTableBody');

        this.showLoading(statsBody, '正在加载 IP 统计...', 4);
        this.showLoading(whitelistBody, '正在加载白名单...', 4);

        try {
            // 获取 IP 统计
            const statsResponse = await fetch(`${this.adminApiBaseUrl}/ip/stats`, {
                headers: { 'Authorization': `Bearer ${this.adminToken}` }
            });

            if (!statsResponse.ok) throw new Error(`HTTP ${statsResponse.status}`);

            const statsData = await statsResponse.json();
            this.updateIPStatsTable(statsData.data);

            // 获取白名单
            const whitelistResponse = await fetch(`${this.adminApiBaseUrl}/ip/whitelist`, {
                headers: { 'Authorization': `Bearer ${this.adminToken}` }
            });

            if (!whitelistResponse.ok) throw new Error(`HTTP ${whitelistResponse.status}`);

            const whitelistData = await whitelistResponse.json();
            this.updateWhitelistTable(whitelistData.data.entries || []);

        } catch (error) {
            console.error('刷新 IP 数据失败:', error);
            this.showError(statsBody, `加载失败: ${error.message}`, 4);
            this.showError(whitelistBody, `加载失败: ${error.message}`, 4);
        }
    }

    updateIPStatsTable(data) {
        const tbody = document.getElementById('ipStatsTableBody');
        const ipStats = data.ip_stats || {};
        const ips = Object.keys(ipStats);

        // 更新统计信息
        document.getElementById('activeIPCount').textContent = ips.length;
        document.getElementById('whitelistCount').textContent = data.whitelist_count || 0;
        document.getElementById('maxConcurrent').textContent = data.max_concurrent || '-';
        document.getElementById('acquireTimeout').textContent = data.acquire_timeout || '-';

        // 更新 IPv6 禁止开关状态
        const ipv6Toggle = document.getElementById('ipv6BlockToggle');
        if (ipv6Toggle) {
            ipv6Toggle.checked = data.ipv6_block_enabled || false;
        }

        if (ips.length === 0) {
            tbody.innerHTML = '<tr><td colspan="4" class="empty">暂无活跃 IP</td></tr>';
            return;
        }

        tbody.innerHTML = ips.map(ip => {
            const stats = ipStats[ip];
            const active = stats.active || 0;
            const waiting = stats.waiting || 0;
            const status = waiting > 0 ? '排队中' : '正常';
            const statusClass = waiting > 0 ? 'status-warning' : 'status-active';

            return `
                <tr>
                    <td><code>${ip}</code></td>
                    <td>${active}</td>
                    <td>${waiting}</td>
                    <td><span class="status-badge ${statusClass}">${status}</span></td>
                </tr>
            `;
        }).join('');
    }

    updateWhitelistTable(entries) {
        const tbody = document.getElementById('whitelistTableBody');

        if (entries.length === 0) {
            tbody.innerHTML = '<tr><td colspan="4" class="empty">暂无白名单</td></tr>';
            return;
        }

        tbody.innerHTML = entries.map(entry => {
            const addedAt = new Date(entry.added_at).toLocaleString('zh-CN');
            return `
                <tr>
                    <td><code>${entry.ip}</code></td>
                    <td>${entry.description || '-'}</td>
                    <td>${addedAt}</td>
                    <td>
                        <button class="btn-icon delete-btn" onclick="dashboard.removeWhitelist('${entry.ip}')" title="移除">
                            🗑️
                        </button>
                    </td>
                </tr>
            `;
        }).join('');
    }

    async submitAddWhitelist() {
        const ip = document.getElementById('newWhitelistIP').value.trim();
        const description = document.getElementById('newWhitelistDesc').value.trim();

        if (!ip) {
            this.showNotification('请输入 IP 地址', 'error');
            return;
        }

        try {
            const response = await fetch(`${this.adminApiBaseUrl}/ip/whitelist`, {
                method: 'POST',
                headers: {
                    'Authorization': `Bearer ${this.adminToken}`,
                    'Content-Type': 'application/json'
                },
                body: JSON.stringify({ ip, description })
            });

            if (!response.ok) {
                const error = await response.json();
                throw new Error(error.error || `HTTP ${response.status}`);
            }

            this.showNotification('白名单已添加', 'success');
            this.closeModal('addWhitelistModal');
            document.getElementById('newWhitelistIP').value = '';
            document.getElementById('newWhitelistDesc').value = '';
            this.refreshIPStats();
        } catch (error) {
            this.showNotification('添加失败: ' + error.message, 'error');
        }
    }

    async removeWhitelist(ip) {
        if (!confirm(`确定要从白名单移除 ${ip} 吗？`)) {
            return;
        }

        try {
            const response = await fetch(`${this.adminApiBaseUrl}/ip/whitelist`, {
                method: 'DELETE',
                headers: {
                    'Authorization': `Bearer ${this.adminToken}`,
                    'Content-Type': 'application/json'
                },
                body: JSON.stringify({ ip })
            });

            if (!response.ok) {
                const error = await response.json();
                throw new Error(error.error || `HTTP ${response.status}`);
            }

            this.showNotification('已从白名单移除', 'success');
            this.refreshIPStats();
        } catch (error) {
            this.showNotification('移除失败: ' + error.message, 'error');
        }
    }

    async toggleIPv6Block(enabled) {
        try {
            const response = await fetch(`${this.adminApiBaseUrl}/ip/ipv6-block`, {
                method: 'POST',
                headers: {
                    'Authorization': `Bearer ${this.adminToken}`,
                    'Content-Type': 'application/json'
                },
                body: JSON.stringify({ enabled })
            });

            if (!response.ok) {
                const error = await response.json();
                throw new Error(error.error || `HTTP ${response.status}`);
            }

            this.showNotification(enabled ? 'IPv6 已禁止' : 'IPv6 已允许', 'success');
        } catch (error) {
            this.showNotification('设置失败: ' + error.message, 'error');
            // 恢复开关状态
            const toggle = document.getElementById('ipv6BlockToggle');
            if (toggle) {
                toggle.checked = !enabled;
            }
        }
    }
}

// 初始化
let dashboard;
document.addEventListener('DOMContentLoaded', () => {
    dashboard = new TokenDashboard();
});
