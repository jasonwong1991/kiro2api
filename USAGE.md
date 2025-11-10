# KIRO2API 使用指南

## 快速开始

### 1. 配置环境变量

创建 `.env` 文件：

```bash
# API 认证密钥（必需）
KIRO_CLIENT_TOKEN=your_client_token_here

# Token 配置（二选一）
# 方式1: JSON 字符串
KIRO_AUTH_TOKEN='[{"auth":"Social","refreshToken":"your_refresh_token"}]'

# 方式2: 配置文件路径（推荐）
KIRO_AUTH_TOKEN=/path/to/tokens.json

# 管理员 Token（可选，用于 Dashboard 管理功能，不设置则禁用管理 API）
# KIRO_ADMIN_TOKEN=your_admin_token_here

# 服务端口（默认 8080）
PORT=8080

# 日志配置
LOG_LEVEL=info
LOG_FORMAT=json
```

### 2. 配置 Token 文件

创建 `tokens.json`：

```json
[
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
]
```

**字段说明**：
- `auth`: 认证类型（`Social` 或 `IdC`）
- `refreshToken`: Refresh Token（必需）
- `clientId`: IdC 认证的客户端 ID（IdC 必需）
- `clientSecret`: IdC 认证的客户端密钥（IdC 必需）
- `disabled`: 是否禁用该账号（可选，默认 false）

### 3. 启动服务

**方式1: 直接运行**
```bash
./kiro2api
```

**方式2: Docker**
```bash
docker run -d \
  --name kiro2api \
  -p 8080:8080 \
  -v $(pwd)/tokens.json:/app/tokens.json \
  -e KIRO_CLIENT_TOKEN=your_token \
  -e KIRO_AUTH_TOKEN=/app/tokens.json \
  -e KIRO_ADMIN_TOKEN=your_admin_token \
  your-registry/kiro2api:latest
```

**方式3: Docker Compose**
```bash
docker-compose up -d
```

## API 使用

### Claude API 兼容端点

**发送消息**
```bash
curl -X POST http://localhost:8080/v1/messages \
  -H "Authorization: Bearer your_client_token" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-sonnet-4",
    "max_tokens": 1024,
    "messages": [
      {"role": "user", "content": "Hello!"}
    ]
  }'
```

**流式响应**
```bash
curl -X POST http://localhost:8080/v1/messages \
  -H "Authorization: Bearer your_client_token" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-sonnet-4",
    "max_tokens": 1024,
    "stream": true,
    "messages": [
      {"role": "user", "content": "Hello!"}
    ]
  }'
```

**Token 计数**
```bash
curl -X POST http://localhost:8080/v1/messages/count_tokens \
  -H "Authorization: Bearer your_client_token" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-sonnet-4",
    "messages": [
      {"role": "user", "content": "Hello!"}
    ]
  }'
```

**模型列表**
```bash
curl http://localhost:8080/v1/models \
  -H "Authorization: Bearer your_client_token"
```

### 支持的模型

| 简写 | 完整模型名 |
|------|-----------|
| `claude-sonnet-4` | `claude-sonnet-4-20250514` |
| `claude-sonnet-4-5` | `claude-sonnet-4-5-20250611` |
| `claude-opus-4` | `claude-opus-4-20250514` |

## Token Dashboard 管理

### 访问 Dashboard

浏览器打开：`http://localhost:8080/`

### 基础功能（无需认证）

- ✅ 查看所有 Token 状态
- ✅ 实时监控剩余次数、过期时间
- ✅ 手动刷新 / 自动刷新（30秒）
- ✅ 状态徽章显示

### 管理功能（需要管理员认证）

1. **启用管理模式**
   - 在页面顶部输入管理员 Token（`.env` 中的 `KIRO_ADMIN_TOKEN`）
   - 点击 **🔓 启用管理** 按钮
   - Token 会保存在浏览器 localStorage

2. **导出配置**
   - **导出全部**: 点击 **📥 导出全部配置** 按钮
   - **导出单个**: 在表格操作列点击 **📥** 按钮

3. **删除失效 Token**
   - **批量删除**: 点击 **🗑️ 批量删除失效Token** 按钮
   - **删除单个**: 在失效 Token 行的操作列点击 **🗑️** 按钮
   - **注意**: 只能删除失效的 Token，正常 Token 无法删除

4. **退出管理模式**
   - 点击 **🔒 退出管理** 按钮

### Token 状态说明

| 状态 | 颜色 | 说明 |
|------|------|------|
| 正常 | 绿色 | Token 可用，剩余次数充足 |
| 即将耗尽 | 橙色 | 剩余次数 ≤ 5 |
| 已耗尽 | 灰色 | 剩余次数 = 0（额度用完） |
| 已过期 | 红色 | Token 已过期 |
| 失效 | 红色背景 | Refresh Token 无效（需要删除） |

## 管理 API（高级）

所有管理 API 需要在 `Authorization` header 中提供管理员 Token。

### 列出所有 Token 状态

```bash
curl -X GET http://localhost:8080/v1/admin/tokens \
  -H "Authorization: Bearer your_admin_token"
```

### 获取单个 Token 状态

```bash
curl -X GET http://localhost:8080/v1/admin/tokens/0 \
  -H "Authorization: Bearer your_admin_token"
```

### 导出 Token 配置

**导出全部**:
```bash
curl -X POST http://localhost:8080/v1/admin/tokens/export \
  -H "Authorization: Bearer your_admin_token" \
  -H "Content-Type: application/json" \
  -d '{}'
```

**导出指定账号**:
```bash
curl -X POST http://localhost:8080/v1/admin/tokens/export \
  -H "Authorization: Bearer your_admin_token" \
  -H "Content-Type: application/json" \
  -d '{"indices": [0, 2]}'
```

### 删除失效 Token

**删除单个**:
```bash
curl -X DELETE http://localhost:8080/v1/admin/tokens/1 \
  -H "Authorization: Bearer your_admin_token"
```

**批量删除所有失效**:
```bash
curl -X DELETE http://localhost:8080/v1/admin/tokens/invalid \
  -H "Authorization: Bearer your_admin_token"
```

### 手动同步配置文件

```bash
curl -X POST http://localhost:8080/v1/admin/tokens/sync \
  -H "Authorization: Bearer your_admin_token"
```

## Claude Code 集成

### 配置

```bash
# 设置代理地址
export ANTHROPIC_BASE_URL="http://localhost:8080/v1"
export ANTHROPIC_API_KEY="your_kiro_client_token"

# 使用 Claude Code
claude-code --model claude-sonnet-4 "帮我重构这段代码"
```

### 支持的功能

- ✅ 完整 Anthropic API 兼容
- ✅ 流式响应零延迟
- ✅ 工具调用完整支持
- ✅ 多模态图片处理
- ✅ Token 计数接口

## Docker 部署

### 构建镜像

**标准版本**（推荐）:
```bash
docker build -t kiro2api:latest .
```

**低内存版本**（适用于 1GB VPS）:
```bash
docker build -f Dockerfile.lowmem -t kiro2api:lowmem .
```

### 运行容器

```bash
docker run -d \
  --name kiro2api \
  -p 8080:8080 \
  -v $(pwd)/tokens.json:/app/tokens.json \
  -e KIRO_CLIENT_TOKEN=your_token \
  -e KIRO_AUTH_TOKEN=/app/tokens.json \
  -e KIRO_ADMIN_TOKEN=your_admin_token \
  -e LOG_LEVEL=info \
  kiro2api:latest
```

### Docker Compose

```yaml
version: '3.8'

services:
  kiro2api:
    image: kiro2api:latest
    container_name: kiro2api
    ports:
      - "8080:8080"
    volumes:
      - ./tokens.json:/app/tokens.json
    environment:
      - KIRO_CLIENT_TOKEN=your_client_token
      - KIRO_AUTH_TOKEN=/app/tokens.json
      - KIRO_ADMIN_TOKEN=your_admin_token
      - LOG_LEVEL=info
      - LOG_FORMAT=json
      - GIN_MODE=release
    restart: unless-stopped
```

## 环境变量完整列表

| 变量名 | 必需 | 默认值 | 说明 |
|--------|------|--------|------|
| `KIRO_CLIENT_TOKEN` | ✅ | - | API 认证密钥 |
| `KIRO_AUTH_TOKEN` | ✅ | - | Token 配置（JSON 字符串或文件路径） |
| `KIRO_ADMIN_TOKEN` | ❌ | 空（禁用） | 管理员 Token（启用管理 API） |
| `PORT` | ❌ | `8080` | 服务端口 |
| `LOG_LEVEL` | ❌ | `info` | 日志级别（debug/info/warn/error） |
| `LOG_FORMAT` | ❌ | `json` | 日志格式（text/json） |
| `GIN_MODE` | ❌ | `release` | Gin 运行模式（debug/release/test） |
| `MAX_TOOL_DESCRIPTION_LENGTH` | ❌ | `1024` | 工具描述最大长度 |

## 安全建议

### 1. Token 保护

- ✅ 使用强密码（至少 32 位随机字符）
- ✅ 定期更换 Token
- ✅ 不要将 Token 提交到代码仓库
- ✅ 使用环境变量或配置文件管理 Token

### 2. 访问控制

- ✅ 仅在内网或 VPN 环境中暴露服务
- ✅ 使用防火墙限制访问 IP
- ✅ 生产环境使用 HTTPS
- ✅ 不要将管理员 Token 暴露给普通用户

### 3. 配置文件

- ✅ 设置正确的文件权限（`chmod 600 tokens.json`）
- ✅ 定期备份配置文件
- ✅ 使用 `.gitignore` 排除敏感文件

### 4. 日志管理

- ✅ 定期清理日志文件
- ✅ 监控异常访问
- ✅ 设置日志轮转

## 故障排查

### 服务无法启动

1. 检查端口是否被占用：`lsof -i :8080`
2. 检查环境变量是否正确配置
3. 检查配置文件格式是否正确
4. 查看日志：`LOG_LEVEL=debug ./kiro2api`

### Token 无法使用

1. 检查 Refresh Token 是否过期
2. 检查认证类型（Social/IdC）是否正确
3. 检查 IdC 的 clientId 和 clientSecret
4. 使用 Dashboard 查看 Token 状态

### API 请求失败

1. 检查 `KIRO_CLIENT_TOKEN` 是否正确
2. 检查请求格式是否符合 Anthropic API 规范
3. 检查模型名称是否正确
4. 查看服务器日志获取详细错误信息

### Dashboard 无法访问

1. 检查服务是否正常运行
2. 检查防火墙是否开放端口
3. 检查浏览器控制台是否有错误
4. 清除浏览器缓存和 localStorage

## 性能优化

### 1. 内存优化

- 使用 Go 1.24 的 GC 优化
- 避免不必要的对象池
- 直接内存分配

### 2. 并发控制

- 每个账号独立的并发锁
- 避免全局锁竞争
- 异步日志写入

### 3. 超时策略

- 根据 MaxTokens 动态调整超时
- 工具调用自动延长超时
- 智能重试机制

## 开发指南

### 编译

```bash
go build -o kiro2api main.go
```

### 测试

```bash
# 运行所有测试
go test ./...

# 单包测试
go test ./parser -v

# 基准测试
go test ./... -bench=. -benchmem
```

### 代码质量

```bash
# 静态检查
go vet ./...

# 格式化
go fmt ./...

# Linter
golangci-lint run
```

## 技术架构

### 核心组件

- **server/** - HTTP 服务器、路由、处理器、中间件
- **converter/** - API 格式转换（Anthropic ↔ OpenAI ↔ CodeWhisperer）
- **parser/** - EventStream 解析、工具调用处理、会话管理
- **auth/** - Token 管理（顺序选择策略、并发控制、使用限制监控）
- **utils/** - 请求分析、Token 估算、HTTP 工具
- **types/** - 数据结构定义
- **logger/** - 结构化日志
- **config/** - 配置常量和模型映射

### 设计原则

- **KISS**: 保持简单直接
- **YAGNI**: 只实现需要的功能
- **DRY**: 避免重复代码
- **SOLID**: 遵循面向对象设计原则

## 更新日志

### v1.1.0 (2025-11-10)
- ✨ 新增 Token Dashboard 管理界面
- ✨ 新增管理员认证功能
- ✨ 新增导出配置功能（全部/单个）
- ✨ 新增删除失效 Token 功能（批量/单个）
- ✨ 新增失效 Token 自动检测
- 🎨 优化 UI 设计和交互体验

### v1.0.0 (2025-11-09)
- 🎉 初始版本发布
- ✅ Anthropic API 完整兼容
- ✅ 多账号池管理
- ✅ 双认证方式支持（Social/IdC）
- ✅ 流式响应优化
- ✅ 工具调用支持

## 许可证

MIT License

## 支持

如有问题或建议，请提交 Issue 或 Pull Request。
