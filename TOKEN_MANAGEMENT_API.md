# Token 管理 API 文档

## 概述

Token 管理 API 提供了对 refresh token 的完整管理功能，包括：
- 查看所有账号状态（包括失效状态）
- 手动删除单个或批量删除失效账号
- 导出账号配置
- 自动同步更新 `auth_config.json` 文件

## 配置

### 环境变量

在 `.env` 文件中添加：

```bash
# 管理员 API 认证密钥（必需）
KIRO_ADMIN_TOKEN=your_secure_admin_token_here
```

**安全建议**：
- 使用强密码（至少 32 位随机字符）
- 不要与 `KIRO_CLIENT_TOKEN` 使用相同的值
- 不要将管理员 token 暴露给普通用户

## API 端点

所有管理 API 端点都需要在 `Authorization` header 中提供管理员 token：

```bash
Authorization: Bearer your_admin_token_here
```

### 1. 列出所有 Token 状态

**请求**：
```bash
GET /v1/admin/tokens
```

**响应示例**：
```json
{
  "success": true,
  "data": {
    "tokens": [
      {
        "index": 0,
        "auth_type": "Social",
        "refresh_token_preview": "aorA****Gj12",
        "disabled": false,
        "is_invalid": false,
        "available": 150.5,
        "usage_info": {
          "usageBreakdownList": [...]
        },
        "last_used": "2025-11-09T10:30:00Z"
      },
      {
        "index": 1,
        "auth_type": "IdC",
        "refresh_token_preview": "eyJr****M123",
        "disabled": false,
        "is_invalid": true,
        "invalidated_at": "2025-11-09T09:15:00Z",
        "available": 0,
        "last_used": "2025-11-09T09:00:00Z"
      }
    ],
    "total": 2
  }
}
```

**字段说明**：
- `index`: 账号索引（用于删除操作）
- `auth_type`: 认证类型（Social 或 IdC）
- `refresh_token_preview`: Token 预览（只显示前后各4位）
- `is_invalid`: 是否失效（true 表示 refresh token 无效，非额度耗尽）
- `invalidated_at`: 失效时间
- `available`: 剩余可用额度
- `last_used`: 最后使用时间

### 2. 获取单个 Token 状态

**请求**：
```bash
GET /v1/admin/tokens/:index
```

**示例**：
```bash
curl -X GET http://localhost:8080/v1/admin/tokens/0 \
  -H "Authorization: Bearer your_admin_token"
```

**响应**：
```json
{
  "success": true,
  "data": {
    "index": 0,
    "auth_type": "Social",
    "refresh_token_preview": "aorA****Gj12",
    "disabled": false,
    "is_invalid": false,
    "available": 150.5
  }
}
```

### 3. 导出 Token 配置

**请求**：
```bash
POST /v1/admin/tokens/export
Content-Type: application/json

{
  "indices": [0, 2]  // 空数组或不提供表示导出全部
}
```

**示例 - 导出全部**：
```bash
curl -X POST http://localhost:8080/v1/admin/tokens/export \
  -H "Authorization: Bearer your_admin_token" \
  -H "Content-Type: application/json" \
  -d '{}'
```

**示例 - 导出指定账号**：
```bash
curl -X POST http://localhost:8080/v1/admin/tokens/export \
  -H "Authorization: Bearer your_admin_token" \
  -H "Content-Type: application/json" \
  -d '{"indices": [0, 2]}'
```

**响应**：
```json
{
  "success": true,
  "data": {
    "configs": [
      {
        "auth": "Social",
        "refreshToken": "aorAAAAAGj12...",
        "disabled": false
      },
      {
        "auth": "IdC",
        "refreshToken": "eyJraWQi...",
        "clientId": "uG-18bI....",
        "clientSecret": "eyJraWQiOiJrZXktM.....",
        "disabled": false
      }
    ],
    "count": 2
  }
}
```

### 4. 删除单个失效 Token

**请求**：
```bash
DELETE /v1/admin/tokens/:index
```

**限制**：只能删除失效的 token（`is_invalid: true`）

**示例**：
```bash
curl -X DELETE http://localhost:8080/v1/admin/tokens/1 \
  -H "Authorization: Bearer your_admin_token"
```

**响应**：
```json
{
  "success": true,
  "message": "Token 已删除"
}
```

**错误响应**（尝试删除未失效的 token）：
```json
{
  "error": {
    "message": "只能删除失效的 token，索引 0 的 token 未失效",
    "code": "bad_request"
  }
}
```

### 5. 批量删除所有失效 Token

**请求**：
```bash
DELETE /v1/admin/tokens/invalid
```

**示例**：
```bash
curl -X DELETE http://localhost:8080/v1/admin/tokens/invalid \
  -H "Authorization: Bearer your_admin_token"
```

**响应**：
```json
{
  "success": true,
  "message": "已删除所有失效 token",
  "data": {
    "removed_count": 3
  }
}
```

### 6. 手动同步配置文件

**请求**：
```bash
POST /v1/admin/tokens/sync
```

**说明**：手动触发配置文件同步（删除操作会自动同步）

**示例**：
```bash
curl -X POST http://localhost:8080/v1/admin/tokens/sync \
  -H "Authorization: Bearer your_admin_token"
```

**响应**：
```json
{
  "success": true,
  "message": "配置文件已同步"
}
```

## 失效检测机制

### 自动检测

系统在刷新 token 时自动检测失效错误：

**失效标识**（HTTP 401/403 + 以下错误信息）：
- `invalid_grant`
- `invalid_token`
- `token_expired`
- `unauthorized_client`
- `InvalidToken`
- `ExpiredToken`
- `UnauthorizedClient`

**非失效错误**：
- 额度耗尽（由 `usage_checker` 处理）
- 网络错误
- 其他临时错误

### 失效状态

失效的 token 会被标记为 `is_invalid: true`，并记录失效时间 `invalidated_at`。

## 配置文件同步

### 自动同步

当使用文件配置（`KIRO_AUTH_TOKEN=/path/to/auth_config.json`）时：
- 删除单个 token → 自动更新文件
- 批量删除失效 token → 自动更新文件

### 手动同步

使用 `POST /v1/admin/tokens/sync` 端点手动触发同步。

### 环境变量配置

如果使用环境变量 JSON 字符串配置（`KIRO_AUTH_TOKEN='[...]'`），则无法同步到文件。

## 使用场景

### 场景 1：定期清理失效账号

```bash
# 1. 查看所有账号状态
curl -X GET http://localhost:8080/v1/admin/tokens \
  -H "Authorization: Bearer your_admin_token"

# 2. 批量删除所有失效账号
curl -X DELETE http://localhost:8080/v1/admin/tokens/invalid \
  -H "Authorization: Bearer your_admin_token"
```

### 场景 2：导出备份配置

```bash
# 导出所有账号配置到文件
curl -X POST http://localhost:8080/v1/admin/tokens/export \
  -H "Authorization: Bearer your_admin_token" \
  -H "Content-Type: application/json" \
  -d '{}' > backup_tokens.json
```

### 场景 3：删除特定失效账号

```bash
# 1. 查看账号状态，找到失效的账号索引
curl -X GET http://localhost:8080/v1/admin/tokens \
  -H "Authorization: Bearer your_admin_token"

# 2. 删除索引为 2 的失效账号
curl -X DELETE http://localhost:8080/v1/admin/tokens/2 \
  -H "Authorization: Bearer your_admin_token"
```

## 安全注意事项

1. **管理员 Token 保护**：
   - 使用强密码
   - 定期更换
   - 不要记录在日志中
   - 不要暴露给前端

2. **访问控制**：
   - 仅在内网或 VPN 环境中暴露管理 API
   - 使用防火墙限制访问 IP
   - 考虑使用 HTTPS

3. **审计日志**：
   - 所有管理操作都会记录在日志中
   - 包括请求 ID、客户端 IP、操作类型

## 错误处理

### 常见错误

**401 Unauthorized**：
```json
{
  "error": {
    "message": "无效的管理员 token",
    "code": "unauthorized"
  }
}
```

**403 Forbidden**：
```json
{
  "error": {
    "message": "管理 API 未启用",
    "code": "forbidden"
  }
}
```

**404 Not Found**：
```json
{
  "error": {
    "message": "索引超出范围: 10",
    "code": "not_found"
  }
}
```

**400 Bad Request**：
```json
{
  "error": {
    "message": "只能删除失效的 token，索引 0 的 token 未失效",
    "code": "bad_request"
  }
}
```

## 日志示例

```
INFO  TokenManager初始化 config_count=3 config_order_count=3 selection_strategy=round_robin config_path=/path/to/auth_config.json
WARN  检测到token失效 config_index=1 auth_type=Social error=...
INFO  删除失效token index=1 remaining_count=2
INFO  配置文件已更新 file_path=/path/to/auth_config.json config_count=2
INFO  批量删除失效token removed_count=2 remaining_count=1
```

## 最佳实践

1. **定期监控**：
   - 每天检查一次 token 状态
   - 及时发现失效账号

2. **自动化清理**：
   - 使用 cron 定期调用批量删除 API
   - 示例：`0 2 * * * curl -X DELETE http://localhost:8080/v1/admin/tokens/invalid -H "Authorization: Bearer $ADMIN_TOKEN"`

3. **配置备份**：
   - 删除前先导出配置
   - 定期备份 `auth_config.json`

4. **日志监控**：
   - 监控失效检测日志
   - 设置告警通知

## 技术实现

### 架构设计

- **失效检测**：`auth/refresh.go:206-262` - `isTokenInvalidError()`
- **状态管理**：`auth/token_manager.go:32` - `invalidated map[int]time.Time`
- **管理方法**：`auth/token_manager.go:477-687`
- **API 处理器**：`server/admin_handlers.go`
- **配置同步**：`auth/config.go:155-177` - `SaveConfigToFile()`

### 并发安全

所有操作都在 `TokenManager.mutex` 保护下进行，确保线程安全。

### SOLID 原则

- **单一职责**：TokenManager 负责 token 管理，AdminHandlers 负责 HTTP 处理
- **开闭原则**：通过接口扩展，不修改现有代码
- **依赖倒置**：依赖 AuthService 抽象，不依赖具体实现
