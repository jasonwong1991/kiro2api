#!/bin/bash

# 脚本功能：备份 amazonq_accounts.json，重置为空数组，然后执行注册脚本

SOURCE_FILE="amazonq_accounts.json"
BASE_NAME="amazonq_accounts copy"

# 查找下一个可用的编号
next_num=1
while [[ -f "${BASE_NAME} ${next_num}.json" ]]; do
    ((next_num++))
done

BACKUP_FILE="${BASE_NAME} ${next_num}.json"

# 1. 复制备份
cp "$SOURCE_FILE" "$BACKUP_FILE"
if [[ $? -eq 0 ]]; then
    echo "✅ 备份完成: $BACKUP_FILE"
else
    echo "❌ 备份失败"
    exit 1
fi

# 2. 重置为空数组
echo '[]' > "$SOURCE_FILE"
if [[ $? -eq 0 ]]; then
    echo "✅ 重置完成: $SOURCE_FILE 已设为空数组"
else
    echo "❌ 重置失败"
    exit 1
fi

# 3. 执行 Python 脚本
echo "🚀 开始执行注册脚本..."
python main_signup_temp_email.py -w 10 -n 20000 -p http://127.0.0.1:10808
