package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"kiro2api/logger"
)

// AuthConfig 简化的认证配置
type AuthConfig struct {
	AuthType     string `json:"auth"`
	RefreshToken string `json:"refreshToken"`
	ClientID     string `json:"clientId,omitempty"`
	ClientSecret string `json:"clientSecret,omitempty"`
	Region       string `json:"region,omitempty"`
	Disabled     bool   `json:"disabled,omitempty"`
}

// DefaultRegion 默认 AWS 区域
const DefaultRegion = "us-east-1"

// 认证方法常量
const (
	AuthMethodSocial = "Social"
	AuthMethodIdC    = "IdC"
)

// DefaultConfigPath 默认配置文件路径
const DefaultConfigPath = "tokens.json"

// loadConfigs 从环境变量加载配置
// 允许空配置，服务仍可启动（通过 WebUI 配置）
func loadConfigs() ([]AuthConfig, error) {
	// 检测并警告弃用的环境变量
	deprecatedVars := []string{
		"REFRESH_TOKEN",
		"AWS_REFRESHTOKEN",
		"IDC_REFRESH_TOKEN",
		"BULK_REFRESH_TOKENS",
	}

	for _, envVar := range deprecatedVars {
		if os.Getenv(envVar) != "" {
			logger.Warn("检测到已弃用的环境变量",
				logger.String("变量名", envVar),
				logger.String("迁移说明", "请迁移到KIRO_AUTH_TOKEN的JSON格式"))
			logger.Warn("迁移示例",
				logger.String("新格式", `KIRO_AUTH_TOKEN='[{"auth":"Social","refreshToken":"your_token"}]'`))
		}
	}

	// 只支持KIRO_AUTH_TOKEN的JSON格式（支持文件路径或JSON字符串）
	jsonData := os.Getenv("KIRO_AUTH_TOKEN")
	if jsonData == "" {
		// 尝试从默认配置文件加载（兼容 tokens.json 被挂载成目录的场景）
		defaultPath, err := resolveConfigFilePath(DefaultConfigPath)
		if err != nil {
			logger.Warn("解析默认配置路径失败", logger.Err(err))
		} else {
			content, readErr := os.ReadFile(defaultPath)
			if readErr == nil {
				configs, err := parseJSONConfig(string(content))
				if err != nil {
					logger.Warn("解析默认配置文件失败", logger.Err(err))
					return []AuthConfig{}, nil
				}
				validConfigs := processConfigs(configs)
				logger.Info("从默认配置文件加载认证配置",
					logger.String("file_path", defaultPath),
					logger.Int("valid_config_count", len(validConfigs)))
				return validConfigs, nil
			}
			if !os.IsNotExist(readErr) {
				logger.Warn("读取默认配置文件失败", logger.Err(readErr))
				return []AuthConfig{}, nil
			}
		}
		// 允许空配置启动，可通过 WebUI 配置
		logger.Info("未配置 KIRO_AUTH_TOKEN，服务将以空配置启动，可通过 WebUI 添加 Token")
		return []AuthConfig{}, nil
	}

	// 判断是文件路径还是 JSON 字符串
	var configData string
	isFilePath := !isLikelyJSONConfigValue(jsonData)

	if isFilePath {
		resolvedPath, err := resolveConfigFilePath(jsonData)
		if err != nil {
			return nil, fmt.Errorf("解析配置路径失败: %w\n配置路径: %s", err, jsonData)
		}

		// 读取文件内容（文件不存在则允许空配置启动）
		content, err := os.ReadFile(resolvedPath)
		if err != nil {
			if os.IsNotExist(err) {
				// 文件不存在，返回空配置（WebUI 导入时会自动创建）
				logger.Info("配置文件不存在，服务将以空配置启动，可通过 WebUI 添加 Token",
					logger.String("file_path", resolvedPath),
					logger.String("config_path", strings.TrimSpace(jsonData)))
				return []AuthConfig{}, nil
			}
			return nil, fmt.Errorf("读取配置文件失败: %w\n文件路径: %s", err, resolvedPath)
		}
		configData = string(content)
		logger.Info("从文件加载认证配置",
			logger.String("file_path", resolvedPath),
			logger.String("config_path", strings.TrimSpace(jsonData)))
	} else {
		// JSON 字符串
		configData = jsonData
		logger.Debug("从环境变量加载JSON配置")
	}

	// 解析JSON配置
	configs, err := parseJSONConfig(configData)
	if err != nil {
		return nil, fmt.Errorf("解析KIRO_AUTH_TOKEN失败: %w\n"+
			"请检查JSON格式是否正确\n"+
			"示例: KIRO_AUTH_TOKEN='[{\"auth\":\"Social\",\"refreshToken\":\"token1\"}]'", err)
	}

	if len(configs) == 0 {
		logger.Info("KIRO_AUTH_TOKEN 配置为空，可通过 WebUI 添加 Token")
		return []AuthConfig{}, nil
	}

	validConfigs := processConfigs(configs)
	if len(validConfigs) == 0 {
		logger.Warn("没有有效的认证配置，可通过 WebUI 添加 Token")
		return []AuthConfig{}, nil
	}

	logger.Info("成功加载认证配置",
		logger.Int("总配置数", len(configs)),
		logger.Int("有效配置数", len(validConfigs)))

	return validConfigs, nil
}

// GetConfigs 公开的配置获取函数，供其他包调用
func GetConfigs() ([]AuthConfig, error) {
	return loadConfigs()
}

// parseJSONConfig 解析JSON配置字符串
func parseJSONConfig(jsonData string) ([]AuthConfig, error) {
	var configs []AuthConfig

	// 尝试解析为数组
	if err := json.Unmarshal([]byte(jsonData), &configs); err != nil {
		// 尝试解析为单个对象
		var single AuthConfig
		if err := json.Unmarshal([]byte(jsonData), &single); err != nil {
			return nil, fmt.Errorf("JSON格式无效: %w", err)
		}
		configs = []AuthConfig{single}
	}

	return configs, nil
}

// processConfigs 处理和验证配置
func processConfigs(configs []AuthConfig) []AuthConfig {
	var validConfigs []AuthConfig

	for i, config := range configs {
		// 验证必要字段
		if config.RefreshToken == "" {
			continue
		}

		// 设置默认认证类型
		if config.AuthType == "" {
			config.AuthType = AuthMethodIdC
		}

		// 设置默认区域
		if config.Region == "" {
			config.Region = DefaultRegion
		}

		// 验证IdC认证的必要字段
		if config.AuthType == AuthMethodIdC {
			if config.ClientID == "" || config.ClientSecret == "" {
				continue
			}
		}

		// 跳过禁用的配置
		if config.Disabled {
			continue
		}

		validConfigs = append(validConfigs, config)
		_ = i // 避免未使用变量警告
	}

	return validConfigs
}

// SaveConfigToFile 保存配置到文件
func SaveConfigToFile(configs []AuthConfig, filePath string) error {
	if filePath == "" {
		return fmt.Errorf("配置文件路径为空")
	}

	resolvedPath, err := resolveConfigFilePath(filePath)
	if err != nil {
		return fmt.Errorf("解析配置路径失败: %w\n配置路径: %s", err, filePath)
	}

	// 序列化配置
	data, err := json.MarshalIndent(configs, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化配置失败: %w", err)
	}

	// 确保父目录存在（支持目录挂载）
	parentDir := filepath.Dir(resolvedPath)
	if err := os.MkdirAll(parentDir, 0700); err != nil {
		return fmt.Errorf("创建配置目录失败: %w\n目录: %s", err, parentDir)
	}

	// 写入文件
	if err := os.WriteFile(resolvedPath, data, 0600); err != nil {
		return fmt.Errorf("写入配置文件失败: %w\n文件路径: %s", err, resolvedPath)
	}

	logger.Info("配置文件已更新",
		logger.String("file_path", resolvedPath),
		logger.String("config_path", strings.TrimSpace(filePath)),
		logger.Int("config_count", len(configs)))

	return nil
}
