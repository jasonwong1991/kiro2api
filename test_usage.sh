#!/bin/bash

# 测试非流式响应
echo "测试非流式响应的 usage 信息..."
curl -s -X POST http://localhost:8080/v1/messages \
  -H "Authorization: Bearer 123456" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-sonnet-4-20250514",
    "max_tokens": 100,
    "messages": [
      {"role": "user", "content": "Say hello"}
    ],
    "stream": false
  }' | jq '.usage'

echo ""
echo "测试流式响应的 usage 信息..."
curl -s -X POST http://localhost:8080/v1/messages \
  -H "Authorization: Bearer 123456" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-sonnet-4-20250514",
    "max_tokens": 100,
    "messages": [
      {"role": "user", "content": "Say hello"}
    ],
    "stream": true
  }' | grep -E "message_start|message_delta" | head -5
