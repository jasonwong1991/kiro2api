package auth

import (
	"testing"
	"time"
)

func TestProxyPoolManager_Creation(t *testing.T) {
	// 测试创建代理池
	proxies := []string{
		"http://user1:pass1@proxy1.com:8080",
		"http://user2:pass2@proxy2.com:8080",
		"http://user3:pass3@proxy3.com:8080",
	}

	pm, err := NewProxyPoolManager(proxies)
	if err != nil {
		t.Fatalf("创建代理池失败: %v", err)
	}
	defer pm.Stop()

	if len(pm.proxies) != 3 {
		t.Errorf("期望3个代理，实际得到%d个", len(pm.proxies))
	}

	// 检查代理客户端是否已创建
	if len(pm.proxyClients) != 3 {
		t.Errorf("期望3个代理客户端，实际得到%d个", len(pm.proxyClients))
	}
}

func TestProxyPoolManager_LoadBalance(t *testing.T) {
	// 测试负载均衡分配
	proxies := []string{
		"http://user1:pass1@proxy1.com:8080",
		"http://user2:pass2@proxy2.com:8080",
		"http://user3:pass3@proxy3.com:8080",
	}

	pm, err := NewProxyPoolManager(proxies)
	if err != nil {
		t.Fatalf("创建代理池失败: %v", err)
	}
	defer pm.Stop()

	// 模拟10个账号分配代理
	// 期望分配：4、3、3
	assignments := make(map[string]int) // proxyURL -> count

	for i := 0; i < 10; i++ {
		tokenIndex := string(rune('0' + i))
		proxyURL, _, err := pm.GetProxyForToken(tokenIndex)
		if err != nil {
			t.Fatalf("获取代理失败: %v", err)
		}
		assignments[proxyURL]++
	}

	// 验证分配结果
	if len(assignments) != 3 {
		t.Errorf("期望分配到3个代理，实际分配到%d个", len(assignments))
	}

	// 检查分配是否均衡（允许差异1）
	min, max := 100, 0
	for _, count := range assignments {
		if count < min {
			min = count
		}
		if count > max {
			max = count
		}
	}

	if max-min > 1 {
		t.Errorf("负载不均衡，最大差异%d，分配情况: %v", max-min, assignments)
	}

	t.Logf("分配结果: %v", assignments)
}

func TestProxyPoolManager_SessionPersistence(t *testing.T) {
	// 测试会话保持
	proxies := []string{
		"http://user1:pass1@proxy1.com:8080",
		"http://user2:pass2@proxy2.com:8080",
	}

	pm, err := NewProxyPoolManager(proxies)
	if err != nil {
		t.Fatalf("创建代理池失败: %v", err)
	}
	defer pm.Stop()

	tokenIndex := "0"

	// 第一次获取
	proxy1, _, err := pm.GetProxyForToken(tokenIndex)
	if err != nil {
		t.Fatalf("获取代理失败: %v", err)
	}

	// 第二次获取，应该返回相同的代理
	proxy2, _, err := pm.GetProxyForToken(tokenIndex)
	if err != nil {
		t.Fatalf("获取代理失败: %v", err)
	}

	if proxy1 != proxy2 {
		t.Errorf("会话保持失败: 第一次%s，第二次%s", proxy1, proxy2)
	}

	t.Logf("会话保持成功: %s", proxy1)
}

func TestProxyPoolManager_Stats(t *testing.T) {
	// 测试统计信息
	proxies := []string{
		"http://user1:pass1@proxy1.com:8080",
		"http://user2:pass2@proxy2.com:8080",
	}

	pm, err := NewProxyPoolManager(proxies)
	if err != nil {
		t.Fatalf("创建代理池失败: %v", err)
	}
	defer pm.Stop()

	// 分配一些代理
	for i := 0; i < 5; i++ {
		tokenIndex := string(rune('0' + i))
		_, _, _ = pm.GetProxyForToken(tokenIndex)
	}

	stats := pm.GetStats()

	if stats["total_proxies"].(int) != 2 {
		t.Errorf("期望2个代理，实际%v", stats["total_proxies"])
	}

	if stats["assigned_tokens"].(int) != 5 {
		t.Errorf("期望分配5个token，实际%v", stats["assigned_tokens"])
	}

	t.Logf("统计信息: %+v", stats)
}

func TestProxyPoolManager_ResetTokenProxy(t *testing.T) {
	// 测试重置token代理绑定
	proxies := []string{
		"http://user1:pass1@proxy1.com:8080",
		"http://user2:pass2@proxy2.com:8080",
	}

	pm, err := NewProxyPoolManager(proxies)
	if err != nil {
		t.Fatalf("创建代理池失败: %v", err)
	}
	defer pm.Stop()

	tokenIndex := "0"

	// 获取代理
	proxy1, _, err := pm.GetProxyForToken(tokenIndex)
	if err != nil {
		t.Fatalf("获取代理失败: %v", err)
	}

	// 重置绑定
	pm.ResetTokenProxy(tokenIndex)

	// 再次获取，可能得到不同的代理
	proxy2, _, err := pm.GetProxyForToken(tokenIndex)
	if err != nil {
		t.Fatalf("获取代理失败: %v", err)
	}

	t.Logf("重置前: %s, 重置后: %s", proxy1, proxy2)
}

func TestLoadProxyPoolFromEnv(t *testing.T) {
	// 测试从环境变量加载代理池
	// 注意：这个测试不设置环境变量，只验证函数不会崩溃
	proxies := LoadProxyPoolFromEnv()

	if proxies != nil && len(proxies) > 0 {
		t.Logf("从环境变量加载了%d个代理", len(proxies))
	} else {
		t.Logf("环境变量未配置代理池（符合预期）")
	}
}

func TestProxyPoolManager_HealthCheck(t *testing.T) {
	// 测试健康检查初始化
	proxies := []string{
		"http://user1:pass1@proxy1.com:8080",
	}

	pm, err := NewProxyPoolManager(proxies)
	if err != nil {
		t.Fatalf("创建代理池失败: %v", err)
	}

	// 等待一小段时间，确保健康检查goroutine启动
	time.Sleep(100 * time.Millisecond)

	// 停止健康检查
	pm.Stop()

	// 再等待一小段时间，确保goroutine正常退出
	time.Sleep(100 * time.Millisecond)

	t.Log("健康检查协程正常启动和停止")
}

func TestLoadProxiesFromFile(t *testing.T) {
	// 测试从文件加载代理列表
	proxies, err := loadProxiesFromFile("../proxies.txt.example")
	if err != nil {
		t.Logf("加载示例文件失败（可能不存在）: %v", err)
		return
	}

	// 示例文件中的代理都是注释，所以应该为空
	t.Logf("从文件加载了%d个代理", len(proxies))
}

func TestIsFilePath(t *testing.T) {
	// 测试文件路径判断
	tests := []struct {
		input    string
		expected bool
	}{
		{"/path/to/proxies.txt", true},
		{"./proxies.txt", true},
		{"../proxies.txt", true},
		{"proxies.txt", true},
		{"proxies.conf", true},
		{"proxies.list", true},
		{"http://proxy1.com:8080,http://proxy2.com:8080", false},
		{"http://proxy1.com:8080", false},
	}

	for _, tt := range tests {
		result := isFilePath(tt.input)
		if result != tt.expected {
			t.Errorf("isFilePath(%q) = %v, expected %v", tt.input, result, tt.expected)
		}
	}
}

func TestProxyPoolManager_Fallback(t *testing.T) {
	// 测试所有代理都不健康时的回退处理
	// 当所有代理都不健康时，系统会选择失败次数最少的代理进行尝试
	proxies := []string{
		"http://user1:pass1@proxy1.com:8080",
	}

	pm, err := NewProxyPoolManager(proxies)
	if err != nil {
		t.Fatalf("创建代理池失败: %v", err)
	}
	defer pm.Stop()

	// 手动标记所有代理为不健康
	pm.mutex.Lock()
	for _, proxy := range pm.proxies {
		proxy.Healthy = false
	}
	pm.mutex.Unlock()

	// 获取代理，即使不健康也会选择失败次数最少的代理（容错降级）
	proxyURL, client, err := pm.GetProxyForToken("test")

	// 不应该返回错误
	if err != nil {
		t.Errorf("不应该返回错误: %v", err)
	}

	// 当所有代理都不健康时，会选择失败次数最少的代理
	if proxyURL == "" {
		t.Errorf("期望选择失败次数最少的代理，实际返回空字符串")
	}

	// client 应该不为 nil
	if client == nil {
		t.Errorf("期望 client 不为 nil")
	}

	t.Logf("回退处理测试通过：选择了代理 %s", proxyURL)
}
