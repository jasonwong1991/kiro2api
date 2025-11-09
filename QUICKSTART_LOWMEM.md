# 🚀 低内存 VPS 快速部署

**适用于**: 1GB RAM 单核 VPS

---

## 方案选择

### 🥇 方案 1A: 上游预构建镜像 (最快)

**适用**: 未修改源代码

```bash
# 1. 拉取镜像
docker pull ghcr.io/caidaoli/kiro2api:latest

# 2. 修改 docker-compose.yml
# 将 build: 部分替换为:
# image: ghcr.io/caidaoli/kiro2api:latest

# 3. 启动
docker compose up -d
```

**优点**: 无需构建，30秒部署完成

---

### 🥇 方案 1B: 自建镜像 (推荐二次开发)

**适用**: 修改了源代码，需要自己的镜像

```bash
# 1. 提交代码触发 GitHub Actions 构建
git add .
git commit -m "feat: 自定义功能"
git push origin main

# 2. 等待 GitHub Actions 构建完成（3-5分钟）
# 访问 https://github.com/你的用户名/kiro2api/actions

# 3. VPS 上拉取你的镜像
docker pull ghcr.io/你的用户名/kiro2api:latest

# 4. 修改 docker-compose.yml
# image: ghcr.io/你的用户名/kiro2api:latest

# 5. 启动
docker compose up -d
```

**优点**:
- ✅ 在 GitHub 服务器构建（零本地资源消耗）
- ✅ 完全自动化（push 即构建）
- ✅ 支持多平台（amd64 + arm64）
- ✅ 完全免费

**配置**: 参见 `DOCKER_REGISTRY.md`

---

### 🥈 方案 2: 优化构建 (推荐)

```bash
# 1. 使用优化配置构建
docker compose -f docker-compose.lowmem.yml build --no-cache

# 2. 启动
docker compose -f docker-compose.lowmem.yml up -d
```

**优点**: 支持代码定制，内存占用减少 50%+

---

### 🥉 方案 3: 添加 Swap (临时)

```bash
# 1. 创建 swap
sudo fallocate -l 2G /swapfile
sudo chmod 600 /swapfile
sudo mkswap /swapfile
sudo swapon /swapfile

# 2. 正常构建
docker compose build

# 3. 启动
docker compose up -d
```

**缺点**: 构建速度慢（10-15分钟）

---

## 验证部署

```bash
# 检查容器状态
docker compose ps

# 测试 API
curl -X POST http://localhost:5656/v1/messages \
  -H "Authorization: Bearer Wang1234" \
  -H "Content-Type: application/json" \
  -d '{"model":"claude-sonnet-4-20250514","max_tokens":100,"messages":[{"role":"user","content":"测试"}]}'
```

---

## 故障排查

### 构建时 OOM

```bash
# 清理缓存
docker system prune -a --volumes -f

# 添加 swap
sudo fallocate -l 2G /swapfile
sudo chmod 600 /swapfile
sudo mkswap /swapfile
sudo swapon /swapfile
```

### 监控内存

```bash
# 系统内存
watch -n 1 free -h

# 容器资源
docker stats kiro2api
```

---

## 📚 详细文档

- `DEPLOY_LOWMEM.md` - 完整部署指南
- `docker_build_analysis.md` - 技术分析报告
- `Dockerfile.lowmem` - 优化的 Dockerfile
- `docker-compose.lowmem.yml` - 优化的 docker-compose 配置

---

**更新时间**: 2025-11-09
