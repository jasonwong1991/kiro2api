# 多平台构建 Dockerfile
# 使用交叉编译架构，支持高效的 arm64 和 amd64 构建
# 技术方案：tonistiigi/xx + Clang/LLVM 交叉编译

# 启用 BuildKit 新特性
# syntax=docker/dockerfile:1.4

# 构建阶段 - 使用 BUILDPLATFORM 在原生架构执行
# 使用 Go 1.24，避免 sonic 在更新 Go 版本下的兼容性编译错误
FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS builder

# 安装交叉编译工具链
# tonistiigi/xx 提供跨架构编译辅助工具
COPY --from=tonistiigi/xx:1.6.1 / /
RUN apk add --no-cache git ca-certificates tzdata clang lld

# 设置工作目录
WORKDIR /app

# 配置目标平台的交叉编译工具链
ARG TARGETPLATFORM
RUN xx-apk add musl-dev gcc

# 复制 go mod 文件
COPY go.mod go.sum ./

# 下载依赖（在原生平台执行，速度快）
RUN --mount=type=cache,target=/root/.cache/go-mod \
    go mod download

# 复制源代码
COPY . .

# 交叉编译二进制文件（启用 CGO 以支持 bytedance/sonic）
# xx-go 自动设置 GOOS/GOARCH/CC 等环境变量
ENV CGO_ENABLED=1
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/root/.cache/go-mod \
    xx-go build \
    -ldflags="-s -w" \
    -o kiro2api main.go && \
    xx-verify kiro2api

# 运行阶段
FROM alpine:3.19

# 安装运行时依赖（添加 su-exec 用于权限切换，添加 Python 支持注册模块）
RUN apk --no-cache add ca-certificates tzdata su-exec python3 py3-pip py3-cryptography py3-requests py3-urllib3

# 创建非 root 用户
RUN addgroup -g 1001 -S appgroup && \
    adduser -u 1001 -S appuser -G appgroup

# 设置工作目录
WORKDIR /app

# 从构建阶段复制二进制文件和静态资源
COPY --from=builder /app/kiro2api .
COPY --from=builder /app/static ./static
COPY --from=builder /app/docker-entrypoint.sh /docker-entrypoint.sh
COPY --from=builder /app/docker-init.sh /docker-init.sh
COPY --from=builder /app/register ./register

# 创建必要的目录并设置权限
RUN mkdir -p /home/appuser/.aws/sso/cache && \
    chown -R appuser:appgroup /app /home/appuser && \
    chmod +x /docker-init.sh /docker-entrypoint.sh

# 暴露默认端口
EXPOSE 8080

# 使用 docker-init.sh 作为入口点（以 root 身份运行，修复权限后切换到 appuser）
ENTRYPOINT ["/bin/sh", "/docker-init.sh"]
