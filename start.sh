#!/bin/bash

# 加载 .env 文件中的环境变量
if [ -f .env ]; then
    echo "加载 .env 文件..."
    set -a
    source .env
    set +a
    echo "环境变量已加载"
else
    echo "警告: .env 文件不存在"
fi

# 启动服务
echo "启动 kiro2api..."
./kiro2api
