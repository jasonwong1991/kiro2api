import requests
import string
import random
import re
import time
import html
import logging
import urllib3
import os
from pathlib import Path

# Disable SSL warnings when verify=False is used
urllib3.disable_warnings(urllib3.exceptions.InsecureRequestWarning)

# 导出的公共函数
__all__ = [
    "get_email",
    "get_code",
    "generate_random_string",
    "get_zai_verification_url",
    "generate_random_name",
    "generate_random_password",
    "add_domain_to_blacklist",
    "load_blacklist",
    "reset_blacklist",
]

API_KEY = "mk_2FIOcIjgNHMQpoCDIHreozWY1hu8JzZm"  # mail.webwzw.tech的api

# 黑名单文件路径
BLACKLIST_FILE = "domain_blacklist1.txt"

logger = logging.getLogger(__name__)


def load_blacklist():
    """
    从文件加载黑名单域名

    Returns:
        set: 黑名单域名集合
    """
    if not Path(BLACKLIST_FILE).exists():
        return set()

    try:
        with open(BLACKLIST_FILE, "r", encoding="utf-8") as f:
            blacklist = set(line.strip() for line in f if line.strip())
        logger.info(f"已加载 {len(blacklist)} 个黑名单域名")
        return blacklist
    except Exception as e:
        logger.error(f"加载黑名单失败: {e}")
        return set()


def save_blacklist(blacklist):
    """
    原子性保存黑名单到文件

    Args:
        blacklist: 黑名单域名集合
    """
    try:
        # 使用临时文件 + 重命名实现原子写入
        temp_file = BLACKLIST_FILE + ".tmp"
        with open(temp_file, "w", encoding="utf-8") as f:
            for domain in sorted(blacklist):
                f.write(f"{domain}\n")

        # 原子性替换文件
        os.replace(temp_file, BLACKLIST_FILE)
        logger.info(f"黑名单已保存 ({len(blacklist)} 个域名)")
    except Exception as e:
        logger.error(f"保存黑名单失败: {e}")


def add_domain_to_blacklist(domain):
    """
    将域名添加到黑名单

    Args:
        domain: 要添加的域名
    """
    if not domain:
        logger.warning("域名为空，无法添加到黑名单")
        return

    blacklist = load_blacklist()

    if domain in blacklist:
        logger.info(f"域名 {domain} 已在黑名单中")
        return

    blacklist.add(domain)
    save_blacklist(blacklist)
    print(f"✓ 域名 {domain} 已加入黑名单")
    print(f"  黑名单进度: {len(blacklist)} 个域名")


def reset_blacklist():
    """
    重置黑名单（释放所有域名）
    """
    try:
        if Path(BLACKLIST_FILE).exists():
            os.remove(BLACKLIST_FILE)
        print(f"✓ 黑名单已重置，所有域名已释放")
        logger.info("黑名单已重置")
    except Exception as e:
        logger.error(f"重置黑名单失败: {e}")


def get_available_domains():
    """
    获取可用域名列表（返回所有域名，不过滤黑名单）

    Returns:
        list: 所有可用域名列表
    """
    all_domains = [
      "52996.de5.net",
      "b2b.qzz.io",
      "difa.de5.net",
      "html.us.ci",
      "iqos.de5.net",
      "jasonwong.dpdns.org",
      "jasonwong.eu.cc",
      "jugg.ccwu.cc",
      "lgd.de5.net",
      "mohayo.de5.net",
      "myxixi.dpdns.org",
      "neap.de5.net",
      "new.x10.mx",
      "qoq.de5.net",
      "qq.elementfx.com",
      "react.de5.net",
      "spe.ccwu.cc",
      "tifa.de5.net",
      "uvuv.de5.net",
      "vivii.de5.net",
      "vivi.us.ci",
      "w2w.de5.net",
      "w2w.qzz.io",
      "w2w.x10.mx",
      "webwzw.tech",
      "wong.cc.cd",
      "wong.fr.cr",
      "wongzw.de5.net",
      "wongzw.eu.cc",
      "wongzw.ggff.net",
      "wvwvw.de5.net",
      "wwvww.de5.net",
      "wyx.ccwu.cc",
      "wyxixi.eu.cc",
      "wzw1.dpdns.org",
      "wzw.de5.net",
      "wzw.elementfx.com",
      "wzw.pp.ua",
      "wzw.us.ci",
      "wzw.x10.mx",
      "wzwzw.ccwu.cc",
      "xixii.de5.net",
      "xixi.us.ci",
      "xl.us.ci",
      "xx.elementfx.com",
      "yyf.us.ci",
      "yyinc.de5.net",
      "zh.us.ci"
  ]

    # 直接返回所有域名，不再过滤黑名单
    return all_domains


def generate_random_string(length=10):
    characters = string.ascii_letters + string.digits
    random_string = "".join(random.choice(characters) for _ in range(length))
    return random_string


def get_email(email_name=None, proxies=None):
    """
    获取或创建临时邮箱（每次随机选择域名）

    Args:
        email_name: 指定的邮箱名（如 'wbGglCYA'）。如果不提供，则随机生成。
        proxies: 代理配置

    Returns:
        (email, email_id, domain) 元组，失败返回 (None, None, None)
    """
    url = "https://mail.webwzw.tech/api/emails/generate"
    headers = {"X-API-Key": API_KEY, "Content-Type": "application/json"}

    # 如果提供了邮箱名，使用它；否则从可用域名中随机选择
    if email_name:
        name = email_name.split("@")[0]
        domain = email_name.split("@")[1]
    else:
        # 获取所有可用域名并随机选择
        available_domains = get_available_domains()
        domain = random.choice(available_domains)
        name = generate_random_string(8)

    data = {"name": name, "expiryTime": 3600000, "domain": domain}

    try:
        # 强制不使用代理（即使环境变量中有代理设置）
        no_proxy = {"http": None, "https": None}
        response = requests.post(
            url, headers=headers, json=data, verify=False, proxies=no_proxy
        )
        response.raise_for_status()
        email_data = response.json()

        logger.info(f"成功获取临时邮箱: {email_data.get('email')} (使用域名: {domain})")

        return email_data.get("email"), email_data.get("id"), domain
    except requests.exceptions.RequestException as e:
        logger.error(f"获取邮箱时出错: {e}")
        return None, None, None


def get_code(id: str, proxies=None):
    """
    从邮箱中提取 AWS 验证码

    Args:
        id: 邮箱ID

    Returns:
        6位数字验证码，如果未找到返回 None
    """
    url = f"https://mail.webwzw.tech/api/emails/{id}"
    headers = {"X-API-Key": API_KEY}
    # 强制不使用代理
    no_proxy = {"http": None, "https": None}
    try:
        response = requests.get(url, headers=headers, verify=False, proxies=no_proxy)
        response.raise_for_status()
        emails_data = response.json()

        # 遍历所有邮件
        for email in emails_data.get("messages", []):
            subject = email.get("subject", "")

            # 检查是否是 AWS 验证邮件
            is_aws_email = (
                "AWS" in subject
                or "构建者 ID" in subject
                or "构建\x00者 ID" in subject  # 处理可能的编码问题
                or "验证" in subject
                or "verification" in subject.lower()
                or "verify" in subject.lower()
            )

            if is_aws_email:
                logger.info(f"找到 AWS 验证邮件，主题: {subject}")
                message_id = email.get("id")
                url_message = f"https://mail.webwzw.tech/api/emails/{id}/{message_id}"
                message_response = requests.get(
                    url_message, headers=headers, verify=False, proxies=no_proxy
                )
                message_response.raise_for_status()
                message_data = message_response.json()

                # 获取邮件内容
                html_content = message_data.get("message", {}).get("html", "")
                text_content = message_data.get("message", {}).get("content", "")

                # 方法1: 从 HTML 中提取验证码 <div class="code" style="...">038100</div>
                html_pattern = r'<div class="code"[^>]*>(\d{6})</div>'
                html_match = re.search(html_pattern, html_content)
                if html_match:
                    code = html_match.group(1)
                    logger.info(f"从 HTML 提取到验证码: {code}")
                    return code

                # 方法2: 从纯文本中提取验证码 "验证码：: 038100"
                text_pattern = r"验证码[：:]+\s*(\d{6})"
                text_match = re.search(text_pattern, text_content)
                if text_match:
                    code = text_match.group(1)
                    logger.info(f"从文本提取到验证码: {code}")
                    return code

                # 方法3: 通用6位数字提取（最后备选）
                generic_pattern = r"\b(\d{6})\b"
                generic_match = re.search(generic_pattern, text_content)
                if generic_match:
                    code = generic_match.group(1)
                    logger.info(f"从通用模式提取到验证码: {code}")
                    return code

                logger.warning(f"在 AWS 邮件中未找到验证码")
                logger.debug(f"文本内容片段: {text_content[:200]}...")
                return None

    except requests.exceptions.RequestException:
        # 忽略请求错误，可能是邮件还没到
        pass
    except Exception as e:
        logger.error(f"处理邮件时出现未知错误: {e}", exc_info=True)
    return None


def test_regex_extraction():
    """测试正则表达式提取验证码功能"""
    test_html = '<p class="code">936185</p>'
    code_pattern = r'<p class="code">(\d+)</p>'
    code_match = re.search(code_pattern, test_html)

    if code_match:
        extracted_code = code_match.group(1)
        print(f"测试成功：从 '{test_html}' 中提取到验证码: {extracted_code}")
        return extracted_code
    else:
        print("测试失败：未能提取到验证码")
        return None


def test_random_domain():
    """测试随机域名选择功能"""
    domains = ["webwzw.tech", "w2w.qzz.io", "b2b.qzz.io", "wzw1.dpdns.org"]
    print("测试随机域名选择功能：")

    for i in range(5):
        selected = random.choice(domains)
        print(f"第{i+1}次选择: {selected}")

    print(f"支持的域名列表: {domains}")
    return True


def get_zai_verification_url(email_id: str, timeout=90):
    """
    获取 z.ai 验证链接

    Args:
        email_id: 邮箱ID
        timeout: 超时时间（秒）

    Returns:
        验证链接URL，如果失败返回 None
    """
    url = f"https://mail.webwzw.tech/api/emails/{email_id}"
    headers = {"X-API-Key": API_KEY}

    start_time = time.time()
    attempt = 0

    logger.info(f"开始查询 z.ai 验证邮件，邮箱ID: {email_id}, 超时: {timeout}秒")

    while time.time() - start_time < timeout:
        attempt += 1
        elapsed = time.time() - start_time
        try:
            logger.info(f"第 {attempt} 次查询邮件 (已过 {elapsed:.1f}秒)")
            response = requests.get(url, headers=headers, verify=False)
            response.raise_for_status()
            emails_data = response.json()

            messages = emails_data.get("messages", [])
            logger.info(f"收到 {len(messages)} 封邮件")

            if messages:
                for idx, email in enumerate(messages, 1):
                    email_from = email.get("from", "")
                    email_subject = email.get("subject", "")
                    logger.info(
                        f"  邮件 {idx}: 发件人='{email_from}', 主题='{email_subject}'"
                    )

                    # 检查是否是 z.ai 验证邮件（通过发件人、主题或关键词识别）
                    is_zai_email = (
                        "z.ai" in email_from.lower()
                        or "z.ai" in email_subject.lower()
                        or "请验证您的电子邮箱" in email_subject
                        or "verify" in email_subject.lower()
                    )

                    if is_zai_email:
                        logger.info(f"  找到 z.ai 验证邮件，正在提取验证链接...")
                        message_id = email.get("id")
                        url_message = f"https://mail.webwzw.tech/api/emails/{email_id}/{message_id}"
                        message_response = requests.get(
                            url_message, headers=headers, verify=False
                        )
                        message_response.raise_for_status()
                        message_data = message_response.json()

                        html_content = message_data.get("message", {}).get("html", "")
                        text_content = message_data.get("message", {}).get("text", "")
                        content = html_content + text_content

                        logger.info(
                            f"  邮件内容长度: HTML={len(html_content)}, Text={len(text_content)}"
                        )

                        # 使用正则表达式提取验证链接
                        match = re.search(
                            r'(https://chat\.z\.ai/auth/verify_email\?[^\s<>"]+)',
                            content,
                        )
                        if match:
                            verification_url = html.unescape(match.group(1).strip())
                            logger.info(
                                f"✅ 成功获取 z.ai 验证链接: {verification_url}"
                            )
                            return verification_url
                        else:
                            logger.warning(f"  在 z.ai 邮件中未找到验证链接")
                            logger.debug(f"  邮件内容片段: {content[:500]}...")
        except requests.exceptions.RequestException as e:
            logger.warning(f"查询邮件时请求失败: {e}")
        except Exception as e:
            logger.error(f"处理邮件时出现错误: {e}", exc_info=True)

        time.sleep(2)

    logger.error(f"❌ 超时 {timeout} 秒未收到 z.ai 验证邮件 (共尝试 {attempt} 次)")
    return None


def generate_random_name() -> str:
    """
    生成随机的完整姓名（英文）

    Returns:
        随机生成的姓名 (FirstName LastName)
    """
    first_names = [
        "John",
        "Jane",
        "Michael",
        "Emily",
        "David",
        "Sarah",
        "Robert",
        "Lisa",
        "James",
        "Jennifer",
        "William",
        "Mary",
        "Richard",
        "Patricia",
        "Thomas",
        "Linda",
        "Charles",
        "Barbara",
        "Christopher",
        "Elizabeth",
        "Daniel",
        "Susan",
        "Matthew",
        "Jessica",
        "Anthony",
        "Karen",
        "Donald",
        "Nancy",
        "Mark",
        "Betty",
    ]

    last_names = [
        "Smith",
        "Johnson",
        "Williams",
        "Brown",
        "Jones",
        "Garcia",
        "Miller",
        "Davis",
        "Rodriguez",
        "Martinez",
        "Hernandez",
        "Lopez",
        "Wilson",
        "Anderson",
        "Thomas",
        "Taylor",
        "Moore",
        "Jackson",
        "Martin",
        "Lee",
        "Thompson",
        "White",
        "Harris",
        "Clark",
        "Lewis",
        "Robinson",
        "Walker",
        "Young",
        "Allen",
        "King",
    ]

    first_name = random.choice(first_names)
    last_name = random.choice(last_names)
    full_name = f"{first_name} {last_name}"

    logger.info(f"生成随机姓名: {full_name}")
    return full_name


def generate_random_password() -> str:
    """
    生成满足AWS要求的随机密码:
    - 最少9个字符
    - 包含大写字母、小写字母、数字和特殊字符

    Returns:
        生成的密码
    """
    # 确保至少包含每种必需的字符类型
    password_parts = [
        random.choice(string.ascii_uppercase),  # 至少一个大写字母
        random.choice(string.ascii_lowercase),  # 至少一个小写字母
        random.choice(string.digits),  # 至少一个数字
        random.choice("@$!%*?&"),  # 至少一个特殊字符
    ]

    # 填充剩余字符（总共9-12个字符）
    all_chars = string.ascii_letters + string.digits + "@$!%*?&"
    remaining_length = random.randint(5, 8)  # 9-12个字符
    password_parts.extend(random.choices(all_chars, k=remaining_length))

    # 打乱顺序以避免可预测的模式
    random.shuffle(password_parts)
    password = "".join(password_parts)

    logger.info(f"生成随机密码，长度: {len(password)}")
    return password


def test_aws_code_extraction():
    """测试 AWS 验证码提取功能"""
    print("\n=== 测试 AWS 验证码提取 ===\n")

    # 模拟实际 API 返回的数据
    test_message = {
        "subject": "验证您的 AWS 构建\x00者 ID 电子邮件地址",
        "content": "验证您的 AWS 构建者 ID 电子邮件地址\n\n您好！\n\n感谢您开始使用 AWS 构建者 ID！ AWS 构建者 ID 是构建者的新个人资料。我们想确保是您本人。请输入以下验证码。如果您不想创建 AWS 构建者 ID，请忽略此电子邮件。\n\n感谢您开始使用 AWS 构建者 ID！ AWS 构建者 ID 是构建者的新个人资料。我们想确保是您本人。请输入以下验证码。如果您不想创建 AWS 构建者 ID，请忽略此电子邮件。\n\n验证码：: 038100\n\n此验证码将在发送后 30 分钟过期。\n\n\nAWS 绝不会发送电子邮件要求您披露或验证您的密码、信用卡或银行账号。",
        "html": '<div class="code" style="color: #000; font-size: 36px; font-weight: bold; padding-bottom: 15px;">038100</div>',
    }

    subject = test_message["subject"]
    text_content = test_message["content"]
    html_content = test_message["html"]

    print(f"邮件主题: {subject}")
    print(f"文本内容长度: {len(text_content)}")
    print(f"HTML 内容长度: {len(html_content)}\n")

    # 测试主题检查
    is_aws_email = (
        "AWS" in subject
        or "构建者 ID" in subject
        or "构建\x00者 ID" in subject
        or "验证" in subject
        or "verification" in subject.lower()
        or "verify" in subject.lower()
    )
    print(f"✅ 主题检查通过: {is_aws_email}\n")

    # 测试方法1: HTML 提取
    html_pattern = r'<div class="code"[^>]*>(\d{6})</div>'
    html_match = re.search(html_pattern, html_content)
    if html_match:
        code = html_match.group(1)
        print(f"✅ 方法1 (HTML): 成功提取验证码 = {code}")
    else:
        print("❌ 方法1 (HTML): 未能提取验证码")

    # 测试方法2: 文本提取
    text_pattern = r"验证码[：:]+\s*(\d{6})"
    text_match = re.search(text_pattern, text_content)
    if text_match:
        code = text_match.group(1)
        print(f"✅ 方法2 (文本): 成功提取验证码 = {code}")
    else:
        print("❌ 方法2 (文本): 未能提取验证码")

    # 测试方法3: 通用提取
    generic_pattern = r"\b(\d{6})\b"
    generic_match = re.search(generic_pattern, text_content)
    if generic_match:
        code = generic_match.group(1)
        print(f"✅ 方法3 (通用): 成功提取验证码 = {code}")
    else:
        print("❌ 方法3 (通用): 未能提取验证码")

    print("\n=== 测试完成 ===\n")
    return True


if __name__ == "__main__":
    # 运行测试
    print("=== 正则表达式测试 ===")
    test_regex_extraction()
    print("\n=== 随机域名测试 ===")
    test_random_domain()
    print("\n=== AWS 验证码提取测试 ===")
    test_aws_code_extraction()
