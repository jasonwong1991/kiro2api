# AWS Builder ID 批量注册脚本 - 临时邮箱版本

## 文件说明

- `main_signup_temp_email.py` - 使用临时邮箱的批量注册脚本（新版本）
- `main_signup_outlook_v2.py` - 使用 Outlook 邮箱的注册脚本（原版本）

## 主要特性

### ✨ 新版本特性 (main_signup_temp_email.py)
- ✅ **批量创建**：支持一次创建多个账号
- ✅ **多进程并发**：支持多进程加速注册
- ✅ **命令行参数**：无需修改代码，通过参数控制
- ✅ **代理支持**：支持命令行指定代理或从 .env 读取
- ✅ **自动邮箱**：使用 `email_utils.py` 的临时邮箱 API
- ✅ **自动验证码**：自动获取验证码（通过 `get_code()` 函数）
- ✅ **多进程安全**：使用文件锁确保并发写入安全

### 📊 与 Outlook 版本的区别

| 特性 | 临时邮箱版本 | Outlook 版本 |
|------|-------------|-------------|
| 邮箱来源 | 临时邮箱 API | Outlook 邮箱列表 |
| 验证码获取 | `get_code()` 轮询 | Outlook API |
| 批量创建 | ✅ 支持 | ❌ 不支持 |
| 多进程 | ✅ 支持 | ❌ 不支持 |
| 命令行参数 | ✅ 支持 | ❌ 不支持 |
| 配置方式 | 命令行 + .env | .env 文件 |

## 安装依赖

```bash
pip install -r requirements.txt
```

## 使用方法

### 📝 命令行参数

```bash
python3 main_signup_temp_email.py [选项]

选项:
  -h, --help            显示帮助信息
  -n COUNT, --count COUNT
                        要创建的账号数量（默认: 1）
  -w WORKERS, --workers WORKERS
                        并发进程数（默认: 1）
  -p PROXY, --proxy PROXY
                        代理地址（格式: http://user:pass@host:port）
  -o OUTPUT, --output OUTPUT
                        输出文件名（默认: amazonq_accounts.json）
  -v, --verbose         显示详细日志
```

### 🚀 使用示例

#### 1. 创建单个账号（默认）
```bash
python3 main_signup_temp_email.py
```

#### 2. 批量创建 10 个账号
```bash
python3 main_signup_temp_email.py -n 10
```

#### 3. 使用 4 个进程并发创建 20 个账号
```bash
python3 main_signup_temp_email.py -n 20 -w 4
```

#### 4. 使用代理创建账号
```bash
# 方式 1: 命令行指定代理
python3 main_signup_temp_email.py -n 5 -p http://user:pass@proxy.com:8080

# 方式 2: 从 .env 文件读取代理（不指定 -p 参数）
python3 main_signup_temp_email.py -n 5
```

#### 5. 指定输出文件
```bash
python3 main_signup_temp_email.py -n 10 -o my_accounts.json
```

#### 6. 显示详细日志
```bash
python3 main_signup_temp_email.py -n 5 -v
```

#### 7. 组合使用
```bash
# 使用 8 个进程创建 50 个账号，使用代理，保存到自定义文件
python3 main_signup_temp_email.py -n 50 -w 8 -p http://user:pass@proxy.com:8080 -o batch1.json
```

## 配置代理

### 方式 1: 命令行参数（推荐）
```bash
python3 main_signup_temp_email.py -p http://user:pass@host:port
```

### 方式 2: .env 文件
在项目根目录创建 `.env` 文件：

```bash
# 方式 A: 完整代理 URL
PROXY_URL=http://user:pass@host:port

# 方式 B: 分开配置
PROXY=host:port:user:pass
PROXY_TYPE=http

# 方式 C: 简单格式（无认证）
PROXY=host:port
```

**支持的代理格式**：
- `http://user:pass@host:port`
- `socks5://user:pass@host:port`
- `host:port:user:pass`
- `host:port`

## 工作流程

1. **创建临时邮箱** - 自动调用 `email_utils.get_email()`
2. **生成账号信息** - 随机生成用户名和密码
3. **执行注册流程** - 完整的 AWS Builder ID 注册（21 步）
4. **自动获取验证码** - 通过 `email_utils.get_code()` 轮询邮箱（最多 120 秒）
5. **保存账号信息** - 写入指定的 JSON 文件

## 输出格式

成功注册后，账号信息会保存到 JSON 文件（默认 `amazonq_accounts.json`），格式如下：

```json
[
  {
    "email": "test@example.com",
    "password": "Password123..",
    "username": "TestUser",
    "provider": "BuilderId",
    "createdAt": "2024-01-01T12:00:00.000000",
    "clientId": "xxx",
    "clientSecret": "xxx",
    "refreshToken": "xxx",
    "accessToken": "xxx",
    "expiresIn": 3600,
    "x-amz-sso_authn": "xxx"
  }
]
```

## 日志文件

详细日志保存在 `registration.log` 文件中，包含：
- 每个步骤的详细信息
- API 请求和响应
- 错误信息和堆栈跟踪

## 性能建议

### 并发进程数选择

- **单代理**：建议 `workers = 1-2`（避免代理过载）
- **多代理/无代理**：建议 `workers = CPU核心数`
- **临时邮箱限制**：注意临时邮箱 API 的速率限制

### 示例性能

| 配置 | 创建数量 | 并发数 | 预计耗时 |
|------|---------|--------|---------|
| 单进程 | 10 | 1 | ~20-30 分钟 |
| 多进程 | 10 | 4 | ~5-8 分钟 |
| 多进程 | 50 | 8 | ~15-25 分钟 |

*注：实际耗时取决于网络速度、代理质量和临时邮箱 API 响应速度*

## 注意事项

1. **临时邮箱稳定性**：临时邮箱可能会失败，建议配置稳定的代理
2. **验证码超时**：验证码获取最多等待 120 秒，超时会自动重试（最多 3 次）
3. **多进程安全**：使用文件锁确保并发写入安全，无需担心数据冲突
4. **代理质量**：使用高质量代理可以提高成功率
5. **速率限制**：注意 AWS 和临时邮箱 API 的速率限制

## 故障排查

### 问题 1: 临时邮箱创建失败
**解决方案**：
- 检查网络连接
- 检查代理配置
- 尝试更换代理

### 问题 2: 验证码获取超时
**解决方案**：
- 脚本会自动重试 3 次
- 检查临时邮箱 API 是否正常
- 增加代理稳定性

### 问题 3: 多进程模式下文件损坏
**解决方案**：
- 脚本已使用文件锁保护
- 如果仍有问题，检查 `registration.log`
- 损坏的文件会自动备份为 `.backup` 文件

### 问题 4: macOS 多进程启动失败
**解决方案**：
- 脚本已自动设置 `spawn` 启动方法
- 确保 Python 版本 >= 3.7

## 与原版本对比

### 使用临时邮箱版本的优势
- ✅ 无需准备 Outlook 邮箱列表
- ✅ 支持批量创建和多进程
- ✅ 命令行参数更灵活
- ✅ 自动化程度更高

### 使用 Outlook 版本的优势
- ✅ 邮箱更稳定（如果有可用的 Outlook 账号）
- ✅ 验证码获取更快（直接通过 API）

## 许可证

本项目仅供学习和研究使用。
