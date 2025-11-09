# 账号安全增强改造说明

## 改造概述

本次改造实施了以下安全增强措施，显著降低账号被封风险：

### 1. 设备指纹隔离 🔐

**问题**：之前所有账号使用相同的设备指纹，极易被识别为批量自动化工具。

**解决方案**：
- 每个账号（基于 `refreshToken`）自动生成并固定分配唯一的设备指纹
- 设备指纹包括：
  - User-Agent（完整版本信息）
  - X-Amz-User-Agent（AWS SDK 标识）
  - 设备Hash（64字符唯一标识）
  - OS版本（macOS Darwin 23.x-24.x）
  - Node.js版本（18.x-20.x）
  - SDK版本（1.0.x-1.4.x）
  - Kiro Agent Mode（spec/auto/manual 随机）
  - IDE版本（KiroIDE-0.2.x-0.3.x）

**实现**：
```go
// utils/device_fingerprint.go
fp := utils.GenerateFingerprint(refreshToken)
// 同一 refreshToken 每次生成的指纹完全相同
// 不同 refreshToken 生成不同的指纹
```

**优势**：
- ✅ 每个账号看起来像独立的真实设备
- ✅ 版本号在合理范围内，不会触发异常检测
- ✅ 基于 refreshToken 的种子确保稳定性

### 2. 请求ID完全随机化 🎲

**问题**：
- 之前 ConversationId 基于 IP+User-Agent+时间窗口（1小时）
- 同一IP的所有请求在1小时内共享相同ID
- 规律性太强，容易被识别

**解决方案**：
- ConversationId：每次请求生成新的随机ID
- AgentContinuationId：每次请求生成新的随机UUID
- 格式保持标准：`conv-[32位十六进制]`

**实现**：
```go
// utils/conversation_id.go
func GenerateConversationID(ctx *gin.Context) string {
    randomID := GenerateRandomHex(16)
    return fmt.Sprintf("conv-%s", randomID)
}
```

**优势**：
- ✅ 完全消除规律性模式
- ✅ 每个请求独立，不会关联
- ✅ 依然支持自定义ID（通过 X-Conversation-ID 头）

### 3. 三种请求类型的指纹隔离 🎯

不同类型的AWS请求使用对应的设备指纹：

| 请求类型 | 指纹生成函数 | 使用位置 |
|---------|-------------|---------|
| **主请求**（CodeWhisperer API） | `GenerateFingerprint()` | `server/common.go` |
| **Token刷新** | `GenerateRefreshFingerprint()` | `auth/refresh.go` |
| **使用限制检查** | `GenerateUsageCheckerFingerprint()` | `auth/usage_checker.go` |

**关键**：同一账号的三种请求类型共享相同的 DeviceHash 和 OSVersion，但使用不同的 SDK 版本，模拟真实客户端行为。

## 修改的文件清单

### 新增文件：
- ✅ `utils/device_fingerprint.go` - 设备指纹生成核心逻辑
- ✅ `utils/device_fingerprint_test.go` - 完整的单元测试

### 修改文件：
- ✅ `types/token.go` - 添加 DeviceFingerprint 结构体
- ✅ `auth/refresh.go` - 在token刷新时生成和附加指纹
- ✅ `auth/usage_checker.go` - 使用账号专属指纹
- ✅ `server/common.go` - 主请求使用账号专属指纹
- ✅ `utils/conversation_id.go` - 改为完全随机生成
- ✅ `utils/uuid.go` - 添加 GenerateRandomHex 函数
- ✅ `.env.example` - 添加安全增强说明

## 使用方式

### 无需额外配置！🎉

改造后的系统**完全自动化**，无需任何手动配置：

1. **启动服务**
   ```bash
   ./kiro2api
   ```

2. **自动行为**
   - 服务启动时，为每个配置的账号自动生成固定的设备指纹
   - 每次请求自动使用对应账号的设备指纹
   - ConversationId 和 AgentContinuationId 自动随机生成

3. **验证指纹生成**（可选）
   ```bash
   # 设置日志级别为 debug 查看详细信息
   LOG_LEVEL=debug ./kiro2api
   ```

## 向后兼容性

- ✅ 如果 Token 中没有 Fingerprint 信息（旧缓存），会临时生成
- ✅ 配置文件格式保持不变
- ✅ 所有现有功能完全兼容
- ✅ API 接口无变化

## 测试建议

### 1. 功能测试
```bash
# 运行所有测试
go test ./...

# 运行设备指纹测试
go test ./utils -v -run TestGenerateFingerprint

# 运行 ConversationId 测试
go test ./utils -v -run TestConversationID
```

### 2. 实际请求测试
```bash
# 启动服务
LOG_LEVEL=debug ./kiro2api

# 发送测试请求
curl -X POST http://localhost:8080/v1/messages \
  -H "Authorization: Bearer 123456" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-sonnet-4-20250514",
    "max_tokens": 100,
    "messages": [{"role": "user", "content": "测试设备指纹"}]
  }'

# 观察日志中的设备指纹信息
```

### 3. 验证随机化
```bash
# 发送多个请求，检查 ConversationId 是否每次不同
for i in {1..5}; do
  echo "Request $i:"
  # 你的 curl 请求...
  sleep 1
done
```

## 预期效果

### 改造前的风险：
```
账号A: User-Agent: aws-sdk-js/1.0.18 ... 66c23a8c5d15a... (固定)
账号B: User-Agent: aws-sdk-js/1.0.18 ... 66c23a8c5d15a... (相同！)
账号C: User-Agent: aws-sdk-js/1.0.18 ... 66c23a8c5d15a... (相同！)
       ⚠️ 明显的批量特征
```

### 改造后的效果：
```
账号A: User-Agent: aws-sdk-js/1.0.12 ... darwin#23.4.2 ... 3f8a9c2e7d4b1...
       ConversationId: conv-a7f3e2d9c1b4...

账号B: User-Agent: aws-sdk-js/1.2.18 ... darwin#24.1.0 ... 9d2f4c8e1a3b...
       ConversationId: conv-2c8f9a1e3d7b...

账号C: User-Agent: aws-sdk-js/1.3.25 ... darwin#23.6.5 ... 1e4a7c9d2f8b...
       ConversationId: conv-e9d3c7a2f1b4...

✅ 看起来像3个不同的真实设备
```

## 安全性提升

| 指标 | 改造前 | 改造后 | 改进 |
|------|--------|--------|------|
| 设备指纹唯一性 | ❌ 所有账号相同 | ✅ 每个账号唯一 | 🔥 极大提升 |
| User-Agent 多样性 | ❌ 完全固定 | ✅ 基于账号随机 | 🔥 极大提升 |
| ConversationId 规律性 | ⚠️ 1小时固定 | ✅ 完全随机 | 🔥 极大提升 |
| OS版本真实性 | ❌ darwin#25.0.0 (异常) | ✅ darwin#23-24.x (合理) | ✅ 显著改善 |
| 设备Hash 碰撞率 | ❌ 100% 碰撞 | ✅ 0% 碰撞 | 🔥 极大提升 |

## 注意事项

1. **Token 缓存**
   - 旧的缓存 Token 没有指纹信息会临时生成
   - 建议清空缓存让所有 Token 重新刷新：
     ```bash
     # 重启服务，等待所有 Token 自然过期并刷新
     ```

2. **日志监控**
   ```bash
   # 建议开启 DEBUG 日志观察指纹生成
   LOG_LEVEL=debug ./kiro2api

   # 关键日志字段：
   # - device_hash: 设备唯一标识
   # - os_version: 操作系统版本
   # - kiro_agent_mode: Agent模式
   ```

3. **多账号配置**
   - 建议配置 2-5 个账号实现负载均衡
   - 使用 `round_robin` 策略（默认）
   - 每个账号会自动获得唯一的设备指纹

## 故障排查

### 问题1：编译错误
```bash
# 如果遇到编译错误，确保导入了 utils 包
go mod tidy
go build
```

### 问题2：指纹未生效
```bash
# 检查 Token 是否包含指纹信息
LOG_LEVEL=debug ./kiro2api
# 查找日志中的 "fingerprint" 关键字
```

### 问题3：请求仍然被限制
```bash
# 可能需要：
# 1. 确保使用多个账号分散请求
# 2. 检查账号使用限制剩余量
# 3. 适当降低请求频率
```

## 总结

本次改造通过**设备指纹隔离**和**请求随机化**两大核心机制，将每个账号伪装成独立的真实设备，显著降低了被识别为自动化工具的风险。

关键优势：
- ✅ 零配置，自动生效
- ✅ 完全向后兼容
- ✅ 每个账号独立指纹
- ✅ 请求ID完全随机
- ✅ 版本号在合理范围

**建议下一步**：
1. 部署更新后的版本
2. 配置多个账号分散负载
3. 监控账号使用情况
4. 根据实际情况调整 Token 选择策略
