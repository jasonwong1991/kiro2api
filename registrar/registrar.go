package registrar

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"kiro2api/auth"
	"kiro2api/logger"
)

// registeredAccount represents the JSON output from register_one.py (stdout)
type registeredAccount struct {
	Email        string `json:"email"`
	Password     string `json:"password"`
	Username     string `json:"username"`
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
	RefreshToken string `json:"refreshToken"`
	AccessToken  string `json:"accessToken"`
	ProfileArn   string `json:"profileArn"`
}

// Registrar manages automatic account registration to maintain target count.
type Registrar struct {
	tokenManager  *auth.TokenManager
	targetCount   int
	maxWorkers    int
	checkInterval time.Duration
	proxy         string
	scriptDir     string
	emailProvider string
	ctx           context.Context
	cancel        context.CancelFunc
	running       atomic.Bool
	wg            sync.WaitGroup
}

// New creates a new Registrar instance.
func New(tm *auth.TokenManager) *Registrar {
	targetCount := getEnvInt("KIRO_TARGET_ACCOUNTS", 0)
	maxWorkers := getEnvInt("KIRO_MAX_REGISTER_WORKERS", 50)
	checkInterval := getEnvDuration("KIRO_REGISTER_CHECK_INTERVAL", 30*time.Second)
	proxy := os.Getenv("KIRO_REGISTER_PROXY")
	scriptDir := os.Getenv("KIRO_REGISTER_SCRIPT_DIR")
	emailProvider := os.Getenv("KIRO_REGISTER_EMAIL_PROVIDER")

	if scriptDir == "" {
		scriptDir = "./register"
	}

	return &Registrar{
		tokenManager:  tm,
		targetCount:   targetCount,
		maxWorkers:    maxWorkers,
		checkInterval: checkInterval,
		proxy:         proxy,
		scriptDir:     scriptDir,
		emailProvider: emailProvider,
	}
}

// Start launches the background account maintenance service.
func (r *Registrar) Start() {
	if r.targetCount <= 0 {
		logger.Info("自动注册服务已禁用（KIRO_TARGET_ACCOUNTS 未设置或为 0）")
		return
	}

	scriptPath := filepath.Join(r.scriptDir, "register_one.py")
	if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
		logger.Error("注册脚本不存在，自动注册服务无法启动",
			logger.String("script_path", scriptPath))
		return
	}

	r.ctx, r.cancel = context.WithCancel(context.Background())
	r.running.Store(true)

	logger.Info("自动注册服务已启动",
		logger.Int("target_count", r.targetCount),
		logger.Int("max_workers", r.maxWorkers),
		logger.Duration("check_interval", r.checkInterval),
		logger.String("script_dir", r.scriptDir))

	r.wg.Add(1)
	go r.loop()
}

// Stop stops the registration service and waits for goroutines to finish.
func (r *Registrar) Stop() {
	if !r.running.Load() {
		return
	}
	logger.Info("正在停止自动注册服务...")
	r.cancel()
	r.wg.Wait()
	r.running.Store(false)
	logger.Info("自动注册服务已停止")
}

func (r *Registrar) loop() {
	defer r.wg.Done()

	// Check immediately on first run
	r.checkAndReplenish()

	ticker := time.NewTicker(r.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-r.ctx.Done():
			return
		case <-ticker.C:
			r.checkAndReplenish()
		}
	}
}

func (r *Registrar) checkAndReplenish() {
	currentCount := r.getValidAccountCount()
	if currentCount >= r.targetCount {
		logger.Debug("账号数量充足，无需注册",
			logger.Int("current", currentCount),
			logger.Int("target", r.targetCount))
		return
	}

	deficit := r.targetCount - currentCount
	if deficit > r.maxWorkers {
		deficit = r.maxWorkers
	}

	logger.Info("账号数量不足，开始注册",
		logger.Int("current", currentCount),
		logger.Int("target", r.targetCount),
		logger.Int("deficit", r.targetCount-currentCount),
		logger.Int("this_batch", deficit))

	var (
		successCount atomic.Int32
		failCount    atomic.Int32
		batchWg      sync.WaitGroup
		sem          = make(chan struct{}, r.maxWorkers)
	)

	for i := 0; i < deficit; i++ {
		select {
		case <-r.ctx.Done():
			logger.Info("注册服务已取消，停止当前批次")
			batchWg.Wait()
			return
		default:
		}

		sem <- struct{}{}
		batchWg.Add(1)
		go func() {
			defer batchWg.Done()
			defer func() { <-sem }()

			if err := r.registerOneAccount(); err != nil {
				failCount.Add(1)
				logger.Warn("注册账号失败", logger.Err(err))
			} else {
				successCount.Add(1)
			}
		}()
	}

	batchWg.Wait()

	success := successCount.Load()
	fail := failCount.Load()

	if success > 0 {
		if err := r.tokenManager.SaveConfig(); err != nil {
			logger.Error("保存配置失败", logger.Err(err))
		}
		logger.Info("注册批次完成",
			logger.Int("success", int(success)),
			logger.Int("failed", int(fail)),
			logger.Int("current_total", r.getValidAccountCount()))
	}

	if fail > 0 && success == 0 {
		logger.Warn("本次注册批次全部失败，将在下个周期重试",
			logger.Int("failed", int(fail)))
	}
}

// registerOneAccount runs register_one.py, captures stdout JSON, imports directly.
func (r *Registrar) registerOneAccount() error {
	args := []string{"register_one.py"}

	if r.proxy != "" {
		args = append(args, "-p", r.proxy)
	}
	if r.emailProvider != "" {
		args = append(args, "-e", r.emailProvider)
	}

	ctx, cancel := context.WithTimeout(r.ctx, 10*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "python3", args...)
	cmd.Dir = r.scriptDir

	// stdout = clean JSON, stderr = logs
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("注册脚本执行失败: %w, stderr: %s", err, stderr.String())
	}

	stdoutBytes := bytes.TrimSpace(stdout.Bytes())
	if len(stdoutBytes) == 0 {
		return fmt.Errorf("注册脚本无输出, stderr: %s", stderr.String())
	}

	var acct registeredAccount
	if err := json.Unmarshal(stdoutBytes, &acct); err != nil {
		return fmt.Errorf("解析注册结果失败: %w, stdout: %s", err, string(stdoutBytes))
	}

	if acct.RefreshToken == "" || acct.ClientID == "" || acct.ClientSecret == "" {
		return fmt.Errorf("注册结果缺少必要字段 (refreshToken/clientId/clientSecret)")
	}

	config := auth.AuthConfig{
		AuthType:     auth.AuthMethodIdC,
		RefreshToken: acct.RefreshToken,
		ClientID:     acct.ClientID,
		ClientSecret: acct.ClientSecret,
		Region:       auth.DefaultRegion,
		ProfileArn:   acct.ProfileArn,
	}

	// 先添加配置，然后通过 refresh token 刷新验证账号是否有效
	if err := r.tokenManager.AddToken(config); err != nil {
		return fmt.Errorf("添加账号失败: %w (email=%s)", err, acct.Email)
	}

	logger.Info("成功注册并导入新账号（已触发 refresh 验证）", logger.String("email", acct.Email))
	return nil
}

func (r *Registrar) getValidAccountCount() int {
	statuses := r.tokenManager.GetAllTokensStatus()
	count := 0
	for _, s := range statuses {
		if !s.IsInvalid && !s.Disabled {
			count++
		}
	}
	return count
}

func getEnvInt(key string, defaultVal int) int {
	val := os.Getenv(key)
	if val == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(val)
	if err != nil {
		logger.Warn("环境变量格式无效，使用默认值",
			logger.String("key", key),
			logger.String("value", val),
			logger.Int("default", defaultVal))
		return defaultVal
	}
	return n
}

func getEnvDuration(key string, defaultVal time.Duration) time.Duration {
	val := os.Getenv(key)
	if val == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(val)
	if err == nil {
		return time.Duration(n) * time.Second
	}
	d, err := time.ParseDuration(val)
	if err != nil {
		logger.Warn("环境变量格式无效，使用默认值",
			logger.String("key", key),
			logger.String("value", val),
			logger.String("default", defaultVal.String()))
		return defaultVal
	}
	return d
}
