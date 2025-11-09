# 🐳 自建 Docker 镜像发布指南

适用于二次开发项目，需要发布自己的 Docker 镜像。

---

## 📋 方案对比

| 方案 | 构建位置 | 发布平台 | 自动化 | 费用 | 推荐度 |
|------|---------|---------|--------|------|--------|
| **GitHub Actions** | GitHub 服务器 | ghcr.io | ✅ 自动 | 免费 | ⭐⭐⭐⭐⭐ |
| **本地构建** | 本地 Mac | Docker Hub | ❌ 手动 | 免费 | ⭐⭐⭐ |
| **本地构建** | 本地 Mac | ghcr.io | ❌ 手动 | 免费 | ⭐⭐⭐ |

---

## 🚀 方案 A: GitHub Actions 自动构建 (推荐)

### 优势

- ✅ **零本地资源消耗**: 在 GitHub 服务器上构建
- ✅ **完全自动化**: 每次 push 自动构建并发布
- ✅ **多平台支持**: 自动构建 amd64 和 arm64
- ✅ **免费无限制**: GitHub Actions 对公开仓库免费
- ✅ **内置缓存**: 加速后续构建

### 配置步骤

#### 1. 启用 GitHub Container Registry

已自动配置，无需手动操作。工作流文件位于：
```
.github/workflows/docker-build.yml
```

#### 2. 推送代码触发构建

```bash
# 提交更改
git add .
git commit -m "feat: 添加自动 Docker 构建"
git push origin main
```

#### 3. 查看构建进度

访问你的 GitHub 仓库：
```
https://github.com/你的用户名/kiro2api/actions
```

#### 4. 构建完成后拉取镜像

```bash
# 镜像地址格式
ghcr.io/你的用户名/kiro2api:latest

# 示例（替换为你的用户名）
docker pull ghcr.io/jasonwong/kiro2api:latest
```

### 触发构建的方式

| 操作 | 触发条件 | 生成的标签 |
|------|---------|-----------|
| Push to main | `git push origin main` | `latest` |
| Push tag | `git tag v1.0.0 && git push --tags` | `v1.0.0`, `1.0`, `1`, `latest` |
| 手动触发 | GitHub Actions 页面点击 "Run workflow" | `latest` |

### 镜像标签说明

```bash
# 最新版本
ghcr.io/你的用户名/kiro2api:latest

# 特定版本（需要打 tag）
ghcr.io/你的用户名/kiro2api:v1.0.0
ghcr.io/你的用户名/kiro2api:1.0
ghcr.io/你的用户名/kiro2api:1

# 分支版本
ghcr.io/你的用户名/kiro2api:main
```

### 配置文件说明

**文件**: `.github/workflows/docker-build.yml`

**关键配置**:
```yaml
# 使用优化的 Dockerfile
file: ./Dockerfile.lowmem

# 支持多平台
platforms: linux/amd64,linux/arm64

# 自动发布到 ghcr.io
registry: ghcr.io

# 使用 GitHub Actions 缓存加速构建
cache-from: type=gha
cache-to: type=gha,mode=max
```

---

## 🔧 方案 B: 本地构建 + 手动推送

适用于需要本地测试或无法使用 GitHub Actions 的场景。

### B1: 推送到 Docker Hub

#### 1. 注册 Docker Hub 账号

访问: https://hub.docker.com/signup

#### 2. 本地登录

```bash
docker login
# 输入用户名和密码
```

#### 3. 构建镜像

```bash
# 使用优化的 Dockerfile
docker build -f Dockerfile.lowmem -t 你的用户名/kiro2api:latest .

# 示例
docker build -f Dockerfile.lowmem -t jasonwong/kiro2api:latest .
```

#### 4. 推送镜像

```bash
docker push 你的用户名/kiro2api:latest
```

#### 5. VPS 上拉取

```bash
docker pull 你的用户名/kiro2api:latest
```

---

### B2: 推送到 GitHub Container Registry

#### 1. 创建 Personal Access Token

访问: https://github.com/settings/tokens

权限选择:
- ✅ `write:packages`
- ✅ `read:packages`
- ✅ `delete:packages`

#### 2. 本地登录

```bash
# 使用 token 登录
echo "你的_TOKEN" | docker login ghcr.io -u 你的用户名 --password-stdin
```

#### 3. 构建镜像

```bash
# 注意镜像名格式必须是 ghcr.io/用户名/仓库名
docker build -f Dockerfile.lowmem -t ghcr.io/你的用户名/kiro2api:latest .

# 示例
docker build -f Dockerfile.lowmem -t ghcr.io/jasonwong/kiro2api:latest .
```

#### 4. 推送镜像

```bash
docker push ghcr.io/你的用户名/kiro2api:latest
```

#### 5. 设置镜像为公开

访问: https://github.com/你的用户名?tab=packages

找到 `kiro2api` 包 → Package settings → Change visibility → Public

#### 6. VPS 上拉取

```bash
docker pull ghcr.io/你的用户名/kiro2api:latest
```

---

## 📦 VPS 部署配置

### 修改 docker-compose.yml

```yaml
services:
  kiro2api:
    # 替换 build 配置为你的镜像
    image: ghcr.io/你的用户名/kiro2api:latest

    # 或使用 Docker Hub
    # image: 你的用户名/kiro2api:latest

    container_name: kiro2api
    restart: unless-stopped
    ports:
      - "5656:8080"
    environment:
      - KIRO_AUTH_TOKEN=/app/tokens.json
      - KIRO_CLIENT_TOKEN=${KIRO_CLIENT_TOKEN:-Wang1234}
      - PORT=8080
      - GIN_MODE=release
      - LOG_LEVEL=info
      - LOG_FORMAT=json
    volumes:
      - aws_sso_cache:/home/appuser/.aws/sso/cache
      - ./tokens.json:/app/tokens.json:ro
    deploy:
      resources:
        limits:
          memory: 512M
        reservations:
          memory: 256M

volumes:
  aws_sso_cache:
```

### 部署命令

```bash
# 1. 拉取最新镜像
docker compose pull

# 2. 重启服务
docker compose up -d

# 3. 查看日志
docker compose logs -f
```

---

## 🔄 更新流程

### GitHub Actions 自动构建

```bash
# 1. 修改代码
vim server/common.go

# 2. 提交并推送
git add .
git commit -m "feat: 新功能"
git push origin main

# 3. 等待 GitHub Actions 构建完成（约 3-5 分钟）

# 4. VPS 上更新
docker compose pull
docker compose up -d
```

### 本地构建手动推送

```bash
# 1. 修改代码
vim server/common.go

# 2. 本地构建
docker build -f Dockerfile.lowmem -t ghcr.io/你的用户名/kiro2api:latest .

# 3. 推送镜像
docker push ghcr.io/你的用户名/kiro2api:latest

# 4. VPS 上更新
docker compose pull
docker compose up -d
```

---

## 🏷️ 版本管理

### 使用 Git Tags 管理版本

```bash
# 1. 创建版本标签
git tag -a v1.0.0 -m "Release version 1.0.0"

# 2. 推送标签
git push origin v1.0.0

# 3. GitHub Actions 自动构建多个标签
# - ghcr.io/你的用户名/kiro2api:v1.0.0
# - ghcr.io/你的用户名/kiro2api:1.0
# - ghcr.io/你的用户名/kiro2api:1
# - ghcr.io/你的用户名/kiro2api:latest
```

### VPS 上使用特定版本

```yaml
services:
  kiro2api:
    # 使用特定版本
    image: ghcr.io/你的用户名/kiro2api:v1.0.0

    # 或使用 latest（自动更新）
    # image: ghcr.io/你的用户名/kiro2api:latest
```

---

## 🔍 故障排查

### GitHub Actions 构建失败

**查看日志**:
```
https://github.com/你的用户名/kiro2api/actions
```

**常见问题**:

1. **权限错误**: 确保仓库设置中启用了 Actions
   - Settings → Actions → General → Workflow permissions → Read and write permissions

2. **Dockerfile 路径错误**: 确保 `Dockerfile.lowmem` 存在
   ```bash
   ls -la Dockerfile.lowmem
   ```

3. **构建超时**: GitHub Actions 有 6 小时限制，通常不会超时

### 镜像拉取失败

**问题**: `Error response from daemon: unauthorized`

**解决方案**:

1. **ghcr.io 镜像**: 确保镜像设置为 Public
   - https://github.com/你的用户名?tab=packages
   - Package settings → Change visibility → Public

2. **Docker Hub 镜像**: 确保镜像为公开或已登录
   ```bash
   docker login
   ```

### 镜像大小过大

**查看镜像大小**:
```bash
docker images | grep kiro2api
```

**优化建议**:
- ✅ 已使用 `Dockerfile.lowmem`（最优化）
- ✅ 已使用多阶段构建
- ✅ 已使用 `-ldflags="-s -w"` 压缩二进制

**预期大小**: 30-40MB (Alpine + Go binary)

---

## 📊 性能对比

| 方案 | 构建时间 | 构建资源 | 自动化 | 多平台 |
|------|---------|---------|--------|--------|
| GitHub Actions | 3-5分钟 | GitHub 服务器 | ✅ | ✅ amd64+arm64 |
| 本地构建 (Mac) | 2-3分钟 | 本地 Mac | ❌ | ⚠️ 仅当前平台 |
| VPS 构建 | ❌ OOM | 1GB RAM | ❌ | ❌ |

---

## 🎯 最佳实践

### 开发流程

1. **本地开发**: 使用 `go run main.go` 测试
2. **提交代码**: `git push origin main`
3. **自动构建**: GitHub Actions 自动构建镜像
4. **VPS 部署**: `docker compose pull && docker compose up -d`

### 版本管理

1. **开发版本**: 使用 `latest` 标签
2. **稳定版本**: 使用 `v1.0.0` 等语义化版本
3. **回滚**: 使用特定版本标签

### 安全建议

1. **不要在镜像中包含敏感信息**
   - ✅ 使用环境变量传递 Token
   - ✅ 使用 Volume 挂载配置文件
   - ❌ 不要在 Dockerfile 中硬编码密钥

2. **定期更新基础镜像**
   ```bash
   # 更新 Dockerfile.lowmem 中的基础镜像版本
   FROM golang:1.24-alpine  # 定期更新
   FROM alpine:3.19         # 定期更新
   ```

---

## 📚 相关资源

- [GitHub Actions 文档](https://docs.github.com/en/actions)
- [GitHub Container Registry 文档](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry)
- [Docker Hub 文档](https://docs.docker.com/docker-hub/)
- [Docker Buildx 文档](https://docs.docker.com/buildx/working-with-buildx/)

---

## 🆘 获取帮助

### GitHub Actions 日志

```bash
# 查看最近的 workflow 运行
gh run list

# 查看特定 run 的日志
gh run view <run-id> --log
```

### 本地测试 GitHub Actions

使用 [act](https://github.com/nektos/act) 在本地测试 workflow:

```bash
# 安装 act
brew install act

# 运行 workflow
act push
```

---

**更新时间**: 2025-11-09
**适用版本**: kiro2api v1.x (二次开发版本)
