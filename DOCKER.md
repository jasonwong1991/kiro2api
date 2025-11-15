# Docker 部署指南

本文档介绍如何使用 Docker 和 Docker Compose 部署 kiro2api。

## 📦 镜像信息

### GitHub Container Registry (推荐)

项目使用 GitHub Actions 自动构建并推送到 GHCR：

```bash
# 镜像地址
ghcr.io/jasonwong1991/kiro2api:latest

# 支持的架构
- linux/amd64
- linux/arm64
```

### 可用标签

- `latest` - 最新稳定版本（main/master 分支）
- `main` - 主分支最新构建
- `v1.0.0` - 特定版本号（语义化版本）

## 🚀 快速开始

### 1. 准备配置文件

```bash
# 克隆仓库
git clone https://github.com/jasonwong/kiro2api.git
cd kiro2api

# 复制环境变量配置
cp .env.docker.example .env

# 创建 tokens.json 配置文件
cat > tokens.json <<'EOF'
[
  {
    "auth": "Social",
    "refreshToken": "your_social_refresh_token_here"
  },
  {
    "auth": "IdC",
    "refreshToken": "your_idc_refresh_token_here",
    "clientId": "your_idc_client_id",
    "clientSecret": "your_idc_client_secret"
  }
]
EOF

# 修改 .env 文件中的配置
vim .env
```

### 2. 启动服务

```bash
# 拉取最新镜像并启动
docker-compose pull
docker-compose up -d

# 查看日志
docker-compose logs -f kiro2api

# 查看服务状态
docker-compose ps
```

### 3. 验证服务

```bash
# 健康检查
curl http://localhost:5656/v1/models

# 测试 API
curl -X POST http://localhost:5656/v1/messages \
  -H "Authorization: Bearer 123456" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-sonnet-4-20250514",
    "max_tokens": 100,
    "messages": [
      {"role": "user", "content": "Hello"}
    ]
  }'
```

## 🔧 配置说明

### 环境变量

所有配置通过 `.env` 文件管理，主要配置项：

#### Token 管理

```bash
# Token 配置文件路径
KIRO_AUTH_TOKEN=/app/tokens.json

# Token 选择策略
# - sequential: 顺序选择
# - random: 随机选择
# - round_robin: 轮询选择（默认）
# - batch_rotate: 分批轮换（推荐，避免封号）
TOKEN_SELECTION_STRATEGY=batch_rotate

# 分批轮换批次大小（仅 batch_rotate 策略生效）
# 建议设置为总账号数的 1/3 到 1/5
KIRO_BATCH_SIZE=5
```

#### 认证配置

```bash
# 客户端认证 Token
KIRO_CLIENT_TOKEN=your_strong_password

# 管理员认证 Token（可选）
KIRO_ADMIN_TOKEN=your_admin_token
```

#### 服务配置

```bash
# Gin 运行模式
GIN_MODE=release

# 日志级别
LOG_LEVEL=info

# 日志格式
LOG_FORMAT=json
```

### 端口映射

默认端口映射：`5656:8080`

修改宿主机端口：

```yaml
# docker-compose.yml
ports:
  - "8080:8080"  # 修改为你需要的端口
```

### 数据持久化

项目使用 Docker 卷持久化以下数据：

```yaml
volumes:
  # AWS SSO 缓存（设备指纹）
  - aws_sso_cache:/home/appuser/.aws/sso/cache

  # Token 配置文件（只读）
  - ./tokens.json:/app/tokens.json:ro
```

## 📊 管理操作

### 查看日志

```bash
# 实时日志
docker-compose logs -f kiro2api

# 最近 100 行日志
docker-compose logs --tail=100 kiro2api

# 查看特定时间段日志
docker-compose logs --since="2025-01-01T00:00:00" kiro2api
```

### 重启服务

```bash
# 重启服务
docker-compose restart kiro2api

# 重新创建容器
docker-compose up -d --force-recreate kiro2api
```

### 更新镜像

```bash
# 拉取最新镜像
docker-compose pull

# 重新创建容器
docker-compose up -d

# 清理旧镜像
docker image prune -f
```

### 停止服务

```bash
# 停止服务（保留容器）
docker-compose stop

# 停止并删除容器
docker-compose down

# 停止并删除容器和卷
docker-compose down -v
```

### 备份数据

```bash
# 备份 tokens.json
cp tokens.json tokens.json.backup

# 备份 AWS SSO 缓存卷
docker run --rm \
  -v kiro2api_aws_sso_cache:/data \
  -v $(pwd):/backup \
  alpine tar czf /backup/aws_sso_cache_backup.tar.gz -C /data .
```

### 恢复数据

```bash
# 恢复 tokens.json
cp tokens.json.backup tokens.json

# 恢复 AWS SSO 缓存卷
docker run --rm \
  -v kiro2api_aws_sso_cache:/data \
  -v $(pwd):/backup \
  alpine tar xzf /backup/aws_sso_cache_backup.tar.gz -C /data
```

## 🔐 管理 API

如果设置了 `KIRO_ADMIN_TOKEN`，可以使用管理 API：

### 查看所有 Token 状态

```bash
curl -H "Authorization: Bearer your_admin_token" \
     http://localhost:5656/v1/admin/tokens
```

### 查看单个 Token 状态

```bash
curl -H "Authorization: Bearer your_admin_token" \
     http://localhost:5656/v1/admin/tokens/0
```

### 删除失效的 Token

```bash
# 删除所有失效的 token
curl -X DELETE \
     -H "Authorization: Bearer your_admin_token" \
     http://localhost:5656/v1/admin/tokens/invalid

# 删除指定索引的失效 token
curl -X DELETE \
     -H "Authorization: Bearer your_admin_token" \
     http://localhost:5656/v1/admin/tokens/0
```

### 导出 Token 配置

```bash
# 导出所有 token
curl -H "Authorization: Bearer your_admin_token" \
     http://localhost:5656/v1/admin/tokens/export

# 导出指定索引的 token
curl -H "Authorization: Bearer your_admin_token" \
     "http://localhost:5656/v1/admin/tokens/export?indices=0,1,2"
```

## 🏗️ 本地构建

如果需要本地构建镜像：

### 修改 docker-compose.yml

```yaml
services:
  kiro2api:
    # 注释掉预构建镜像
    # image: ghcr.io/jasonwong1991/kiro2api:latest

    # 启用本地构建
    build:
      context: .
      dockerfile: Dockerfile
```

### 构建并启动

```bash
# 构建镜像
docker-compose build

# 启动服务
docker-compose up -d
```

### 多架构构建

```bash
# 使用 buildx 构建多架构镜像
docker buildx build \
  --platform linux/amd64,linux/arm64 \
  -t kiro2api:local \
  --load \
  .
```

## 🔍 故障排查

### 容器无法启动

```bash
# 查看容器日志
docker-compose logs kiro2api

# 查看容器状态
docker-compose ps

# 检查配置文件
docker-compose config
```

### Token 刷新失败

```bash
# 检查 tokens.json 格式
cat tokens.json | jq .

# 查看详细日志
docker-compose logs -f kiro2api | grep -i "token"

# 进入容器检查
docker-compose exec kiro2api sh
```

### 健康检查失败

```bash
# 手动执行健康检查
docker-compose exec kiro2api wget -q --spider http://localhost:8080/v1/models

# 检查端口监听
docker-compose exec kiro2api netstat -tlnp
```

### 权限问题

```bash
# 检查文件权限
ls -la tokens.json

# 修复权限
chmod 644 tokens.json

# 检查卷权限
docker volume inspect kiro2api_aws_sso_cache
```

## 📈 性能优化

### 资源限制

在 `docker-compose.yml` 中配置资源限制：

```yaml
services:
  kiro2api:
    deploy:
      resources:
        limits:
          cpus: '2'
          memory: 512M
        reservations:
          cpus: '0.5'
          memory: 256M
```

### 日志轮转

配置 Docker 日志驱动：

```yaml
services:
  kiro2api:
    logging:
      driver: "json-file"
      options:
        max-size: "10m"
        max-file: "3"
```

## 🔒 安全建议

1. **使用强密码**
   - 修改 `KIRO_CLIENT_TOKEN` 为强密码
   - 设置 `KIRO_ADMIN_TOKEN` 并妥善保管

2. **限制网络访问**
   ```bash
   # 使用防火墙限制访问
   sudo ufw allow from 192.168.1.0/24 to any port 5656
   ```

3. **使用 HTTPS**
   - 配置反向代理（Nginx/Caddy）
   - 申请 SSL 证书（Let's Encrypt）

4. **定期更新**
   ```bash
   # 定期拉取最新镜像
   docker-compose pull
   docker-compose up -d
   ```

5. **备份数据**
   - 定期备份 `tokens.json`
   - 备份 `aws_sso_cache` 卷

## 📚 相关文档

- [主 README](README.md) - 项目概述和功能介绍
- [.env.example](.env.example) - 完整的环境变量配置说明
- [.env.docker.example](.env.docker.example) - Docker Compose 专用配置
- [Dockerfile](Dockerfile) - 多架构构建配置
- [GitHub Actions](.github/workflows/docker-build.yml) - CI/CD 配置

## 🆘 获取帮助

如果遇到问题：

1. 查看 [Issues](https://github.com/jasonwong/kiro2api/issues)
2. 提交新的 Issue
3. 查看项目文档

## 📝 许可证

本项目采用 MIT 许可证。详见 [LICENSE](LICENSE) 文件。
