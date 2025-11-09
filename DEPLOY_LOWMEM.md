# 低内存 VPS 部署指南

适用于 **1GB RAM 单核 VPS** 的优化部署方案。

---

## 🚨 问题诊断

### 原始配置的内存问题

**症状**: `docker compose build` 导致 VPS 卡死/冻结

**根本原因**: Docker 构建需要 **900MB-1.4GB RAM**，超出 1GB VPS 容量

**内存瓶颈**:
1. 交叉编译工具链 (tonistiigi/xx + Clang/LLVM): 150-250MB
2. CGO 编译 (不必要): 200-300MB
3. BuildKit 缓存挂载: 内存峰值
4. Go 模块缓存: 505MB 依赖 + 246MB 构建缓存
5. 无资源限制: Docker 可消耗所有系统内存

---

## ✅ 解决方案

### 方案 1: 使用预构建镜像 (推荐)

**优点**: 无需构建，零内存压力，部署最快

#### 1.1 使用上游镜像（未修改源码）

```bash
# 1. 拉取预构建镜像
docker pull ghcr.io/caidaoli/kiro2api:latest

# 2. 修改 docker-compose.yml
services:
  kiro2api:
    image: ghcr.io/caidaoli/kiro2api:latest  # 替换 build 配置
    # ... 其他配置保持不变
```

#### 1.2 使用自建镜像（二次开发）

**如果你修改了源代码**，需要发布自己的镜像。推荐使用 **GitHub Actions 自动构建**：

```bash
# 1. 提交代码到 GitHub
git add .
git commit -m "feat: 自定义功能"
git push origin main

# 2. GitHub Actions 自动构建镜像（3-5分钟）
# 访问 https://github.com/你的用户名/kiro2api/actions 查看进度

# 3. VPS 上拉取你的镜像
docker pull ghcr.io/你的用户名/kiro2api:latest

# 4. 修改 docker-compose.yml
services:
  kiro2api:
    image: ghcr.io/你的用户名/kiro2api:latest
```

**详细配置**: 参见 `DOCKER_REGISTRY.md`

**适用场景**:
- 生产环境部署
- 不需要修改源代码（使用上游镜像）
- 二次开发项目（使用自建镜像）
- 追求稳定性和快速部署

---

### 方案 2: 优化构建配置 (本地构建)

**优点**: 支持代码定制，内存占用减少 50%+

#### 2.1 使用优化的 Dockerfile

```bash
# 使用 Dockerfile.lowmem (已创建)
docker compose -f docker-compose.lowmem.yml build
```

**优化内容**:
- ✅ 移除交叉编译工具链 (节省 150-250MB)
- ✅ 禁用 CGO (节省 200-300MB)
- ✅ 移除缓存挂载 (避免内存峰值)
- ✅ 添加资源限制 (防止 OOM)

**预期内存**: 400-600MB (vs 原版 900MB-1.4GB)

#### 2.2 构建命令

```bash
# 方式 1: 使用优化的 docker-compose 配置
docker compose -f docker-compose.lowmem.yml build --no-cache

# 方式 2: 直接使用 Dockerfile.lowmem
docker build -f Dockerfile.lowmem -t kiro2api:lowmem .

# 方式 3: 限制 Docker 构建资源
docker build -f Dockerfile.lowmem \
  --memory=700m \
  --memory-swap=1400m \
  --cpus=1 \
  -t kiro2api:lowmem .
```

**适用场景**:
- 需要修改源代码
- 本地开发测试
- 自定义构建需求

---

### 方案 3: 添加 Swap 空间 (临时方案)

**优点**: 允许使用原始 Dockerfile，无需修改代码

**缺点**: 构建速度慢（使用磁盘 swap），仅适合临时使用

```bash
# 1. 创建 2GB swap 文件
sudo fallocate -l 2G /swapfile
sudo chmod 600 /swapfile
sudo mkswap /swapfile
sudo swapon /swapfile

# 2. 验证 swap 已启用
free -h
# 应显示 Swap: 2.0Gi

# 3. 正常构建
docker compose build

# 4. 永久启用 swap (可选)
echo '/swapfile none swap sw 0 0' | sudo tee -a /etc/fstab

# 5. 构建完成后可禁用 swap (可选)
sudo swapoff /swapfile
sudo rm /swapfile
```

**适用场景**:
- 紧急情况下需要使用原始 Dockerfile
- 临时构建，不频繁重建
- 测试环境

---

## 📋 部署步骤 (推荐方案 2)

### 步骤 1: 准备文件

确保以下文件存在:
- ✅ `Dockerfile.lowmem` (已创建)
- ✅ `docker-compose.lowmem.yml` (已创建)
- ✅ `tokens.json` (需要手动创建，参考 `auth_config.json.example`)

### 步骤 2: 配置 Token

```bash
# 复制示例配置
cp auth_config.json.example tokens.json

# 编辑 tokens.json，填入真实的 refreshToken
nano tokens.json
```

### 步骤 3: 构建镜像

```bash
# 使用优化配置构建
docker compose -f docker-compose.lowmem.yml build --no-cache

# 监控内存使用（另开终端）
watch -n 1 free -h
```

### 步骤 4: 启动服务

```bash
# 启动容器
docker compose -f docker-compose.lowmem.yml up -d

# 查看日志
docker compose -f docker-compose.lowmem.yml logs -f
```

### 步骤 5: 验证部署

```bash
# 检查容器状态
docker compose -f docker-compose.lowmem.yml ps

# 测试 API
curl -X POST http://localhost:5656/v1/messages \
  -H "Authorization: Bearer Wang1234" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-sonnet-4-20250514",
    "max_tokens": 100,
    "messages": [{"role": "user", "content": "测试"}]
  }'
```

---

## 🔍 监控和故障排查

### 监控内存使用

```bash
# 实时监控系统内存
watch -n 1 free -h

# 监控 Docker 容器资源
docker stats kiro2api

# 查看构建过程内存峰值
docker system df -v
```

### 常见问题

#### 问题 1: 构建时仍然 OOM

**解决方案**:
```bash
# 1. 清理 Docker 缓存
docker system prune -a --volumes -f

# 2. 添加临时 swap
sudo fallocate -l 2G /swapfile
sudo chmod 600 /swapfile
sudo mkswap /swapfile
sudo swapon /swapfile

# 3. 重新构建
docker compose -f docker-compose.lowmem.yml build --no-cache
```

#### 问题 2: 容器运行时内存不足

**解决方案**:
```bash
# 调整 docker-compose.lowmem.yml 中的资源限制
deploy:
  resources:
    limits:
      memory: 384M  # 从 512M 降低到 384M
```

#### 问题 3: 构建速度慢

**解决方案**:
```bash
# 使用预构建镜像（方案 1）
docker pull ghcr.io/caidaoli/kiro2api:latest
```

---

## 📊 性能对比

| 配置 | 内存占用 | 构建时间 | 适用场景 |
|------|---------|---------|---------|
| 原始 Dockerfile | 900MB-1.4GB | 3-5分钟 | 多平台构建 |
| Dockerfile.lowmem | 400-600MB | 2-3分钟 | 1GB RAM VPS |
| 预构建镜像 | 0MB (无构建) | 30秒 | 生产环境 |
| 原始 + Swap | 900MB-1.4GB | 10-15分钟 | 临时方案 |

---

## 🎯 最佳实践

### 生产环境

1. **使用预构建镜像** (方案 1)
2. 配置自动更新: `docker compose pull && docker compose up -d`
3. 启用健康检查和自动重启

### 开发环境

1. **使用 Dockerfile.lowmem** (方案 2)
2. 本地构建测试
3. 定期清理 Docker 缓存: `docker system prune -f`

### 紧急情况

1. **添加 Swap 空间** (方案 3)
2. 构建完成后禁用 swap
3. 迁移到方案 1 或方案 2

---

## 📚 相关文件

- `Dockerfile.lowmem` - 优化的 Dockerfile (400-600MB)
- `docker-compose.lowmem.yml` - 优化的 docker-compose 配置
- `DOCKER_REGISTRY.md` - 自建镜像发布指南（GitHub Actions + Docker Hub）
- `docker_build_analysis.md` - 详细的技术分析报告
- `QUICKSTART_LOWMEM.md` - 快速部署参考卡
- `CLAUDE.md` - 项目开发规范

---

## 🔗 参考资源

- [Docker 内存限制文档](https://docs.docker.com/config/containers/resource_constraints/)
- [Go 编译优化指南](https://go.dev/doc/install/source#environment)
- [Alpine Linux 最佳实践](https://wiki.alpinelinux.org/wiki/Docker)

---

## ⚠️ 注意事项

1. **CGO 禁用**: `Dockerfile.lowmem` 禁用了 CGO，经测试项目可正常运行
2. **单平台构建**: 仅支持当前平台构建，不支持交叉编译
3. **资源限制**: 容器运行时限制为 512MB 内存，足够正常使用
4. **Swap 性能**: 使用 swap 会显著降低构建速度，仅适合临时使用

---

**更新时间**: 2025-11-09
**适用版本**: kiro2api v1.x
**测试环境**: 1GB RAM 单核 VPS (Ubuntu 22.04)
