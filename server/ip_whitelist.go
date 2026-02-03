package server

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"kiro2api/logger"
	"kiro2api/utils"
)

const (
	DefaultWhitelistPath = "ip_whitelist.json"
)

// IPWhitelistEntry 白名单条目
type IPWhitelistEntry struct {
	IP          string `json:"ip"`
	Description string `json:"description"`
	AddedAt     string `json:"added_at"`
}

// IPWhitelistManager IP 白名单管理器
type IPWhitelistManager struct {
	mutex     sync.RWMutex
	entries   []IPWhitelistEntry
	ipSet     map[string]bool // 快速查找
	filePath  string
}

// globalWhitelistManager 全局白名单管理器实例
var globalWhitelistManager *IPWhitelistManager

// InitIPWhitelist 初始化 IP 白名单管理器
func InitIPWhitelist(filePath string) (*IPWhitelistManager, error) {
	if filePath == "" {
		filePath = DefaultWhitelistPath
	}

	// 确保文件路径是绝对路径
	if !filepath.IsAbs(filePath) {
		absPath, err := filepath.Abs(filePath)
		if err != nil {
			return nil, fmt.Errorf("获取绝对路径失败: %w", err)
		}
		filePath = absPath
	}

	manager := &IPWhitelistManager{
		entries:  []IPWhitelistEntry{},
		ipSet:    make(map[string]bool),
		filePath: filePath,
	}

	// 加载现有白名单
	if err := manager.load(); err != nil {
		logger.Warn("加载 IP 白名单失败，使用空白名单", logger.Err(err))
	}

	globalWhitelistManager = manager
	logger.Info("IP 白名单管理器已初始化",
		logger.String("file_path", filePath),
		logger.Int("whitelist_count", len(manager.entries)))

	return manager, nil
}

// GetIPWhitelistManager 获取全局白名单管理器
func GetIPWhitelistManager() *IPWhitelistManager {
	return globalWhitelistManager
}

// load 从文件加载白名单
func (m *IPWhitelistManager) load() error {
	// 检查文件是否存在
	if _, err := os.Stat(m.filePath); os.IsNotExist(err) {
		// 文件不存在，创建空白名单文件
		return m.save()
	}

	data, err := os.ReadFile(m.filePath)
	if err != nil {
		return fmt.Errorf("读取白名单文件失败: %w", err)
	}

	var entries []IPWhitelistEntry
	if err := utils.SafeUnmarshal(data, &entries); err != nil {
		return fmt.Errorf("解析白名单文件失败: %w", err)
	}

	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.entries = entries
	m.ipSet = make(map[string]bool)
	for _, entry := range entries {
		m.ipSet[entry.IP] = true
	}

	return nil
}

// save 保存白名单到文件（不加锁版本，调用者需传入快照）
func (m *IPWhitelistManager) save() error {
	return m.saveSnapshot(m.entries)
}

// saveSnapshot 保存白名单快照到文件（原子写入）
func (m *IPWhitelistManager) saveSnapshot(entries []IPWhitelistEntry) error {
	// 深拷贝快照
	snapshot := make([]IPWhitelistEntry, len(entries))
	copy(snapshot, entries)

	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化白名单失败: %w", err)
	}

	// 确保目录存在
	dir := filepath.Dir(m.filePath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}

	// 原子写入：写临时文件 + rename
	tempFile := m.filePath + ".tmp"
	if err := os.WriteFile(tempFile, data, 0600); err != nil {
		return fmt.Errorf("写入临时文件失败: %w", err)
	}

	// 原子替换
	if err := os.Rename(tempFile, m.filePath); err != nil {
		os.Remove(tempFile) // 清理临时文件
		return fmt.Errorf("替换白名单文件失败: %w", err)
	}

	return nil
}

// IsWhitelisted 检查 IP 是否在白名单中
func (m *IPWhitelistManager) IsWhitelisted(ip string) bool {
	// 规范化 IP
	parsedIP := net.ParseIP(ip)
	if parsedIP != nil {
		ip = parsedIP.String()
	}

	m.mutex.RLock()
	defer m.mutex.RUnlock()
	return m.ipSet[ip]
}

// AddIP 添加 IP 到白名单
func (m *IPWhitelistManager) AddIP(ip, description string) error {
	// 验证并规范化 IP 格式
	parsedIP := net.ParseIP(ip)
	if parsedIP == nil {
		return fmt.Errorf("无效的 IP 地址: %s", ip)
	}
	normalizedIP := parsedIP.String()

	m.mutex.Lock()
	defer m.mutex.Unlock()

	// 检查是否已存在
	if m.ipSet[normalizedIP] {
		return fmt.Errorf("IP 已在白名单中: %s", normalizedIP)
	}

	// 添加到白名单
	entry := IPWhitelistEntry{
		IP:          normalizedIP,
		Description: description,
		AddedAt:     time.Now().Format(time.RFC3339),
	}
	m.entries = append(m.entries, entry)
	m.ipSet[normalizedIP] = true

	// 在锁内保存到文件（使用快照避免长时间持锁）
	snapshot := make([]IPWhitelistEntry, len(m.entries))
	copy(snapshot, m.entries)

	// 临时释放锁进行文件 I/O，但保持操作原子性
	// 注意：这里仍然持有锁，确保整个操作的原子性
	if err := m.saveSnapshot(snapshot); err != nil {
		// 回滚内存状态
		m.entries = m.entries[:len(m.entries)-1]
		delete(m.ipSet, normalizedIP)
		return fmt.Errorf("保存白名单失败: %w", err)
	}

	logger.Info("添加 IP 到白名单",
		logger.String("ip", normalizedIP),
		logger.String("description", description))

	return nil
}

// RemoveIP 从白名单移除 IP
func (m *IPWhitelistManager) RemoveIP(ip string) error {
	// 规范化 IP
	parsedIP := net.ParseIP(ip)
	if parsedIP != nil {
		ip = parsedIP.String()
	}

	m.mutex.Lock()
	defer m.mutex.Unlock()

	// 检查是否存在
	if !m.ipSet[ip] {
		return fmt.Errorf("IP 不在白名单中: %s", ip)
	}

	// 从列表中移除
	newEntries := make([]IPWhitelistEntry, 0, len(m.entries)-1)
	for _, entry := range m.entries {
		if entry.IP != ip {
			newEntries = append(newEntries, entry)
		}
	}

	// 备份旧状态用于回滚
	oldEntries := m.entries
	oldInSet := m.ipSet[ip]

	// 更新状态
	m.entries = newEntries
	delete(m.ipSet, ip)

	// 在锁内保存到文件（使用快照）
	snapshot := make([]IPWhitelistEntry, len(m.entries))
	copy(snapshot, m.entries)

	if err := m.saveSnapshot(snapshot); err != nil {
		// 回滚内存状态
		m.entries = oldEntries
		m.ipSet[ip] = oldInSet
		return fmt.Errorf("保存白名单失败: %w", err)
	}

	logger.Info("从白名单移除 IP", logger.String("ip", ip))

	return nil
}

// GetAll 获取所有白名单条目
func (m *IPWhitelistManager) GetAll() []IPWhitelistEntry {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	// 返回副本
	entries := make([]IPWhitelistEntry, len(m.entries))
	copy(entries, m.entries)
	return entries
}

// Count 获取白名单数量
func (m *IPWhitelistManager) Count() int {
	m.mutex.RLock()
	defer m.mutex.RUnlock()
	return len(m.entries)
}
