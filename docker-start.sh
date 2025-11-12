#!/bin/bash

# ============================================================================
# kiro2api Docker 快速启动脚本
# ============================================================================
# 用途：自动检查配置并启动 Docker Compose 服务
# 使用：./docker-start.sh

set -e

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# 打印带颜色的消息
print_info() {
    echo -e "${BLUE}ℹ${NC} $1"
}

print_success() {
    echo -e "${GREEN}✓${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}⚠${NC} $1"
}

print_error() {
    echo -e "${RED}✗${NC} $1"
}

# 检查命令是否存在
check_command() {
    if ! command -v "$1" &> /dev/null; then
        print_error "$1 未安装，请先安装 Docker"
        exit 1
    fi
}

# 检查文件是否存在
check_file() {
    if [ ! -f "$1" ]; then
        return 1
    fi
    return 0
}

# 主函数
main() {
    echo ""
    print_info "kiro2api Docker 快速启动脚本"
    echo ""

    # 1. 检查 Docker 是否安装
    print_info "检查 Docker 环境..."
    check_command "docker"
    print_success "Docker 已安装"

    # 检查 Docker 是否运行
    if ! docker info &> /dev/null; then
        print_error "Docker 未运行，请先启动 Docker"
        exit 1
    fi
    print_success "Docker 正在运行"

    # 2. 检查配置文件
    print_info "检查配置文件..."

    # 检查 .env 文件
    if ! check_file ".env"; then
        print_warning ".env 文件不存在"
        if check_file ".env.docker.example"; then
            print_info "正在从 .env.docker.example 创建 .env..."
            cp .env.docker.example .env
            print_success ".env 文件已创建"
            print_warning "请编辑 .env 文件，配置你的认证信息"
            echo ""
            read -p "是否现在编辑 .env 文件？(y/n) " -n 1 -r
            echo ""
            if [[ $REPLY =~ ^[Yy]$ ]]; then
                ${EDITOR:-vim} .env
            fi
        else
            print_error ".env.docker.example 文件不存在"
            exit 1
        fi
    else
        print_success ".env 文件已存在"
    fi

    # 检查 tokens.json 文件
    if ! check_file "tokens.json"; then
        print_warning "tokens.json 文件不存在"
        if check_file "tokens.json.example"; then
            print_info "正在从 tokens.json.example 创建 tokens.json..."
            cp tokens.json.example tokens.json
            print_success "tokens.json 文件已创建"
            print_warning "请编辑 tokens.json 文件，填入你的 AWS CodeWhisperer refresh tokens"
            echo ""
            read -p "是否现在编辑 tokens.json 文件？(y/n) " -n 1 -r
            echo ""
            if [[ $REPLY =~ ^[Yy]$ ]]; then
                ${EDITOR:-vim} tokens.json
            fi
        else
            print_error "tokens.json.example 文件不存在"
            exit 1
        fi
    else
        print_success "tokens.json 文件已存在"
    fi

    # 检查 docker-compose.yml 文件
    if ! check_file "docker-compose.yml"; then
        print_error "docker-compose.yml 文件不存在"
        exit 1
    fi
    print_success "docker-compose.yml 文件已存在"

    # 3. 验证 docker-compose.yml 语法
    print_info "验证 docker-compose.yml 语法..."
    if docker compose -f docker-compose.yml config --quiet 2>&1; then
        print_success "docker-compose.yml 语法正确"
    else
        print_error "docker-compose.yml 语法错误"
        exit 1
    fi

    # 4. 拉取最新镜像
    print_info "拉取最新镜像..."
    if docker compose pull; then
        print_success "镜像拉取成功"
    else
        print_warning "镜像拉取失败，将尝试使用本地镜像"
    fi

    # 5. 启动服务
    echo ""
    print_info "启动服务..."
    if docker compose up -d; then
        print_success "服务启动成功"
    else
        print_error "服务启动失败"
        exit 1
    fi

    # 6. 等待服务就绪
    print_info "等待服务就绪..."
    sleep 5

    # 7. 检查服务状态
    print_info "检查服务状态..."
    if docker compose ps | grep -q "Up"; then
        print_success "服务运行正常"
    else
        print_error "服务未正常运行"
        print_info "查看日志："
        docker compose logs --tail=50 kiro2api
        exit 1
    fi

    # 8. 健康检查
    print_info "执行健康检查..."
    PORT=$(grep -E "^[^#]*ports:" docker-compose.yml -A 1 | grep -oP '\d+(?=:8080)' | head -1)
    if [ -z "$PORT" ]; then
        PORT=5656
    fi

    if curl -s -f "http://localhost:${PORT}/v1/models" > /dev/null; then
        print_success "健康检查通过"
    else
        print_warning "健康检查失败，服务可能还在启动中"
        print_info "请稍后手动检查：curl http://localhost:${PORT}/v1/models"
    fi

    # 9. 显示服务信息
    echo ""
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    print_success "kiro2api 服务已成功启动！"
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo ""
    echo "📍 服务地址："
    echo "   http://localhost:${PORT}"
    echo ""
    echo "🔍 常用命令："
    echo "   查看日志：docker compose logs -f kiro2api"
    echo "   查看状态：docker compose ps"
    echo "   重启服务：docker compose restart kiro2api"
    echo "   停止服务：docker compose down"
    echo ""
    echo "🧪 测试 API："
    echo "   curl http://localhost:${PORT}/v1/models"
    echo ""
    echo "📚 更多信息："
    echo "   查看文档：cat DOCKER.md"
    echo ""
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo ""

    # 10. 询问是否查看日志
    read -p "是否查看实时日志？(y/n) " -n 1 -r
    echo ""
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        docker compose logs -f kiro2api
    fi
}

# 执行主函数
main "$@"
