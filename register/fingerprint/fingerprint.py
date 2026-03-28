#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
浏览器指纹生成与 Session 初始化模块

提供可复用的浏览器指纹生成和 HTTP Session 初始化功能，
可用于 AWS 等需要模拟真实浏览器的自动化程序。

使用示例:
    from fingerprint import create_session, generate_random_fingerprint

    # 方式1: 快速创建带指纹的 session
    session, fingerprint = create_session(proxy='http://127.0.0.1:7890')

    # 方式2: 分步操作
    fingerprint = generate_random_fingerprint()
    session = create_session_with_fingerprint(fingerprint)
"""

import random
import string
import time
from typing import Dict, Optional, Tuple, Any

# 尝试导入 curl_cffi（绕过 Cloudflare）
try:
    from curl_cffi import requests as curl_requests
    HAS_CURL_CFFI = True
except ImportError:
    curl_requests = None
    HAS_CURL_CFFI = False

import requests as std_requests


# ==================== 指纹生成 ====================

def generate_random_fingerprint() -> Dict[str, Any]:
    """
    生成随机浏览器指纹，模拟真实用户

    Returns:
        包含以下字段的字典:
        - user_agent: 完整的 User-Agent 字符串
        - screen_resolution: 屏幕分辨率 (如 '1920x1080')
        - timezone: 时区偏移 (如 '-480')
        - accept_language: Accept-Language 头
        - platform: 平台标识 (如 'Win32', 'MacIntel')
        - chrome_version: Chrome 主版本号
        - canvas_noise: Canvas 指纹噪音
        - webgl_vendor: WebGL 厂商
        - webgl_renderer: WebGL 渲染器
        - hardware_concurrency: CPU 核心数
        - device_memory: 设备内存 (GB)
    """

    # 随机浏览器版本（使用较新但不是最新的版本）
    chrome_versions = ['130', '131', '132', '133', '134', '135', '136', '137', '138', '139', '140', '141', '142', '143', '144', '145']
    chrome_ver = random.choice(chrome_versions)
    chrome_full = f'{chrome_ver}.0.{random.randint(6800, 7300)}.{random.randint(80, 250)}'

    # 随机操作系统（加权选择，强烈倾向于 Windows 10）
    # 根据真实 HAR 数据，AWS fingerprint 主要使用 Windows
    os_choices = [
        ('Windows NT 10.0; Win64; x64', 'Win32', 0.75),  # 75% Windows 10
        ('Windows NT 11.0; Win64; x64', 'Win32', 0.15),  # 15% Windows 11
        (f'Macintosh; Intel Mac OS X 10_15_{random.randint(5, 7)}', 'MacIntel', 0.03),  # 3% Mac 10.15
        (f'Macintosh; Intel Mac OS X 11_{random.randint(0, 7)}', 'MacIntel', 0.02),  # 2% Mac 11
        (f'Macintosh; Intel Mac OS X 12_{random.randint(0, 7)}', 'MacIntel', 0.02),  # 2% Mac 12
        (f'Macintosh; Intel Mac OS X 13_{random.randint(0, 6)}', 'MacIntel', 0.02),  # 2% Mac 13
        (f'Macintosh; Intel Mac OS X 14_{random.randint(0, 5)}', 'MacIntel', 0.01),  # 1% Mac 14
    ]

    selected_os, selected_platform = _weighted_choice(os_choices)

    # 随机 User-Agent
    user_agent = (
        f'Mozilla/5.0 ({selected_os}) '
        f'AppleWebKit/537.36 (KHTML, like Gecko) '
        f'Chrome/{chrome_full} Safari/537.36'
    )

    # 随机屏幕分辨率（常见分辨率）
    resolutions = [
        ('1920x1080', 0.35),   # 35% - 最常见的 1080p
        ('2560x1440', 0.20),   # 20% - 2K
        ('1366x768', 0.10),    # 10% - 笔记本常见
        ('1440x900', 0.08),    # 8% - Mac 常见
        ('1536x864', 0.07),    # 7% - 其他
        ('1600x900', 0.06),    # 6%
        ('1280x720', 0.05),    # 5%
        ('3840x2160', 0.04),   # 4% - 4K
        ('2880x1800', 0.03),   # 3% - MacBook Pro Retina
        ('1680x1050', 0.02),   # 2%
    ]
    screen_resolution = _weighted_choice_single(resolutions)

    # 随机时区（主要英语国家和其他地区）
    timezones = [
        ('-480', 0.18),  # PST (美西)
        ('-300', 0.25),  # EST (美东)
        ('-360', 0.15),  # CST (美中)
        ('-420', 0.12),  # MST (美山地)
        ('0', 0.10),     # GMT (英国)
        ('-240', 0.05),  # AST (大西洋)
        ('-600', 0.04),  # HST (夏威夷)
        ('60', 0.03),    # CET (中欧)
        ('120', 0.02),   # EET (东欧)
        ('480', 0.02),   # CST (中国)
        ('540', 0.02),   # JST (日本)
        ('600', 0.02),   # AEST (澳大利亚东部)
    ]
    timezone = _weighted_choice_single(timezones)

    # 随机语言（主要英语，增加多样性）
    languages = [
        ('en-US,en;q=0.9', 0.45),
        ('en-GB,en;q=0.9', 0.15),
        ('en-US,en;q=0.9,zh-CN;q=0.8', 0.10),
        ('en-US,en;q=0.9,ja;q=0.8', 0.05),
        ('en-US,en;q=0.9,es;q=0.8', 0.08),
        ('en-US,en;q=0.9,fr;q=0.8', 0.05),
        ('en-US,en;q=0.9,de;q=0.8', 0.04),
        ('en-GB,en;q=0.9,fr;q=0.8', 0.03),
        ('en-AU,en;q=0.9', 0.02),
        ('en-CA,en;q=0.9', 0.02),
        ('en-US,en;q=0.9,ko;q=0.8', 0.01),
    ]
    accept_language = _weighted_choice_single(languages)

    # Canvas 指纹噪音
    canvas_noise = ''.join(random.choices(string.hexdigits.lower(), k=32))

    # 根据平台选择合适的 GPU（确保平台和 GPU 匹配）
    if 'Mac' in selected_platform:
        # Mac 使用 Intel 或 Apple GPU
        webgl_vendors = [
            ('Intel Inc.', 'Intel(R) Iris(TM) Plus Graphics 640'),
            ('Intel Inc.', 'Intel(R) Iris(TM) Plus Graphics 655'),
            ('Intel Inc.', 'Intel(R) Iris(R) Xe Graphics'),
            ('Intel Inc.', 'Intel(R) UHD Graphics 630'),
            ('Intel Inc.', 'Intel(R) UHD Graphics 617'),
            ('Intel Inc.', 'Intel(R) HD Graphics 620'),
            ('Apple', 'Apple M1'),
            ('Apple', 'Apple M2'),
            ('Apple', 'Apple M3'),
        ]
    else:
        # Windows 使用 NVIDIA/AMD/Intel
        webgl_vendors = [
            # NVIDIA GPU - 高端 (20%)
            ('NVIDIA Corporation', 'NVIDIA GeForce RTX 4090'),
            ('NVIDIA Corporation', 'NVIDIA GeForce RTX 4080'),
            ('NVIDIA Corporation', 'NVIDIA GeForce RTX 4070'),
            ('NVIDIA Corporation', 'NVIDIA GeForce RTX 3090'),
            ('NVIDIA Corporation', 'NVIDIA GeForce RTX 3080'),
            ('NVIDIA Corporation', 'NVIDIA GeForce RTX 3070'),
            ('NVIDIA Corporation', 'NVIDIA GeForce RTX 3060'),
            # NVIDIA GPU - 中端 (20%)
            ('NVIDIA Corporation', 'NVIDIA GeForce GTX 1660 Ti'),
            ('NVIDIA Corporation', 'NVIDIA GeForce GTX 1660'),
            ('NVIDIA Corporation', 'NVIDIA GeForce GTX 1650'),
            ('NVIDIA Corporation', 'NVIDIA GeForce GTX 1050 Ti'),
            ('NVIDIA Corporation', 'NVIDIA GeForce GTX 1050'),
            # NVIDIA GPU - 低端/老型号 (来自真实样本)
            ('NVIDIA Corporation', 'NVIDIA GeForce GT 710'),
            ('NVIDIA Corporation', 'NVIDIA GeForce GT 730'),
            ('NVIDIA Corporation', 'NVIDIA GeForce GT 1030'),
            ('NVIDIA Corporation', 'NVIDIA GeForce G100'),
            ('NVIDIA Corporation', 'NVIDIA GeForce 210'),
            # AMD GPU (20%)
            ('AMD', 'AMD Radeon RX 7900 XTX'),
            ('AMD', 'AMD Radeon RX 6900 XT'),
            ('AMD', 'AMD Radeon RX 6700 XT'),
            ('AMD', 'AMD Radeon RX 6600 XT'),
            ('AMD', 'AMD Radeon RX 580'),
            ('AMD', 'AMD Radeon RX 570'),
            ('AMD', 'AMD Radeon RX 560'),
            ('AMD', 'AMD Radeon R7 240'),
            # Intel GPU (25%)
            ('Intel Inc.', 'Intel(R) UHD Graphics 630'),
            ('Intel Inc.', 'Intel(R) UHD Graphics 730'),
            ('Intel Inc.', 'Intel(R) UHD Graphics 770'),
            ('Intel Inc.', 'Intel(R) UHD Graphics 620'),
            ('Intel Inc.', 'Intel(R) HD Graphics 630'),
            ('Intel Inc.', 'Intel(R) HD Graphics 530'),
            ('Intel Inc.', 'Intel(R) Iris(R) Xe Graphics'),
        ]
    webgl_vendor, webgl_renderer = random.choice(webgl_vendors)

    # 硬件并发（CPU核心数）- 增加更多选项
    hardware_concurrency = random.choice([2, 4, 6, 8, 10, 12, 16, 20, 24, 32])

    # 设备内存（GB）- 增加更多选项
    device_memory = random.choice([4, 8, 12, 16, 24, 32, 64])

    return {
        'user_agent': user_agent,
        'screen_resolution': screen_resolution,
        'timezone': timezone,
        'accept_language': accept_language,
        'platform': selected_platform,
        'chrome_version': chrome_ver,
        'canvas_noise': canvas_noise,
        'webgl_vendor': webgl_vendor,
        'webgl_renderer': webgl_renderer,
        'hardware_concurrency': hardware_concurrency,
        'device_memory': device_memory,
    }


def _weighted_choice(choices: list) -> Tuple:
    """加权随机选择（返回除权重外的所有元素）"""
    rand = random.random()
    cumulative = 0
    for *values, weight in choices:
        cumulative += weight
        if rand < cumulative:
            return tuple(values) if len(values) > 1 else values[0]
    return tuple(choices[-1][:-1]) if len(choices[-1]) > 2 else choices[-1][0]


def _weighted_choice_single(choices: list):
    """加权随机选择（返回单个值）"""
    rand = random.random()
    cumulative = 0
    for value, weight in choices:
        cumulative += weight
        if rand < cumulative:
            return value
    return choices[-1][0]


# ==================== Session 创建 ====================

def create_session(
    proxy: str = None,
    use_curl_cffi: bool = True,
    impersonate: str = "chrome120",
    fingerprint: Dict = None,
    verbose: bool = False
) -> Tuple[Any, Dict]:
    """
    创建带有浏览器指纹的 HTTP Session

    Args:
        proxy: 代理地址，格式 'http://ip:port' 或 'http://user:pass@ip:port'
        use_curl_cffi: 是否使用 curl_cffi（可绕过 Cloudflare）
        impersonate: curl_cffi 模拟的浏览器版本
        fingerprint: 自定义指纹，如果为 None 则自动生成
        verbose: 是否输出详细日志

    Returns:
        (session, fingerprint) 元组
        - session: requests.Session 或 curl_cffi.requests.Session
        - fingerprint: 使用的指纹字典

    Example:
        session, fp = create_session(proxy='http://127.0.0.1:7890')
        response = session.get('https://example.com')
    """
    # 生成或使用提供的指纹
    if fingerprint is None:
        fingerprint = generate_random_fingerprint()

    if verbose:
        print('[指纹] 生成随机浏览器指纹')
        print(f'  Chrome版本: {fingerprint["chrome_version"]}')
        print(f'  平台: {fingerprint["platform"]}')
        print(f'  分辨率: {fingerprint["screen_resolution"]}')
        print(f'  语言: {fingerprint["accept_language"]}')
        print(f'  时区: UTC{fingerprint["timezone"]}')
        print(f'  GPU: {fingerprint["webgl_vendor"]} / {fingerprint["webgl_renderer"]}')

    # 创建 session
    session = None

    if use_curl_cffi and HAS_CURL_CFFI:
        try:
            session = curl_requests.Session(impersonate=impersonate)
            if verbose:
                print(f'[curl_cffi] 已初始化，模拟 {impersonate}')
        except Exception as e:
            if verbose:
                print(f'[curl_cffi] 初始化失败: {e}，使用标准 requests')

    if session is None:
        session = std_requests.Session()
        if verbose:
            print('[requests] 使用标准 requests 库')

    # 应用指纹到 session headers
    session.headers.update({
        'User-Agent': fingerprint['user_agent'],
        'Accept-Language': fingerprint['accept_language'],
        'Accept': 'text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8',
        'Accept-Encoding': 'gzip, deflate, br',
        'Connection': 'keep-alive',
        'Upgrade-Insecure-Requests': '1',
        'sec-ch-ua': f'"Not;A=Brand";v="99", "Google Chrome";v="{fingerprint["chrome_version"]}", "Chromium";v="{fingerprint["chrome_version"]}"',
        'sec-ch-ua-mobile': '?0',
        'sec-ch-ua-platform': '"Windows"' if 'Win' in fingerprint["platform"] else ('"macOS"' if 'Mac' in fingerprint["platform"] else f'"{fingerprint["platform"]}"'),
        'Sec-Fetch-Dest': 'document',
        'Sec-Fetch-Mode': 'navigate',
        'Sec-Fetch-Site': 'none',
        'Sec-Fetch-User': '?1',
    })

    # 设置代理（明确禁用环境变量代理）
    if proxy:
        # curl_cffi 需要特殊处理 socks5 代理
        if HAS_CURL_CFFI and hasattr(session, '_impersonate'):
            # curl_cffi session - 直接设置 proxy 属性
            session.proxies = {
                'http': proxy,
                'https': proxy,
                'all': proxy
            }
        elif hasattr(session, 'proxies'):
            # 标准 requests session
            session.proxies = {'http': proxy, 'https': proxy}
        if verbose:
            print(f'[代理] 已设置: {proxy}')
    else:
        # 明确禁用代理（防止从环境变量读取）
        session.proxies = {'http': None, 'https': None}

    return session, fingerprint


def create_session_with_fingerprint(
    fingerprint: Dict,
    proxy: str = None,
    use_curl_cffi: bool = True
) -> Any:
    """
    使用指定指纹创建 Session（不返回指纹）

    Args:
        fingerprint: 指纹字典（来自 generate_random_fingerprint）
        proxy: 代理地址
        use_curl_cffi: 是否使用 curl_cffi

    Returns:
        session 对象
    """
    session, _ = create_session(
        proxy=proxy,
        use_curl_cffi=use_curl_cffi,
        fingerprint=fingerprint
    )
    return session


# ==================== 工具函数 ====================

def random_delay(min_seconds: float = 1.0, max_seconds: float = 5.0):
    """
    添加随机延迟，模拟真实用户行为

    Args:
        min_seconds: 最小延迟（秒）
        max_seconds: 最大延迟（秒）
    """
    delay = random.uniform(min_seconds, max_seconds)
    time.sleep(delay)


def get_proxy_ip(api_url: str, timeout: int = 30, verbose: bool = False) -> Optional[str]:
    """
    从代理 API 获取代理 IP

    Args:
        api_url: 代理 API URL（返回 ip:port 格式）
        timeout: 请求超时时间
        verbose: 是否输出日志

    Returns:
        'http://ip:port' 格式的代理地址，失败返回 None
    """
    try:
        if verbose:
            print('[代理] 获取代理IP...')

        resp = std_requests.get(api_url, timeout=timeout)

        if resp.status_code == 200:
            result = resp.text.strip()

            # 检查是否是错误
            if result.startswith('error'):
                if verbose:
                    print(f'[代理] 获取失败: {result}')
                return None

            # 格式: IP:port
            if ':' in result:
                proxy_url = f'http://{result}'
                if verbose:
                    print(f'[代理] 成功: {result}')
                return proxy_url
            else:
                if verbose:
                    print(f'[代理] 格式错误: {result}')
                return None
        else:
            if verbose:
                print(f'[代理] 请求失败: {resp.status_code}')
            return None

    except Exception as e:
        if verbose:
            print(f'[代理] 异常: {e}')
        return None


def print_fingerprint(fingerprint: Dict):
    """打印指纹信息（调试用）"""
    print('=' * 50)
    print('浏览器指纹信息:')
    print('=' * 50)
    print(f'  User-Agent: {fingerprint["user_agent"][:60]}...')
    print(f'  Chrome版本: {fingerprint["chrome_version"]}')
    print(f'  平台: {fingerprint["platform"]}')
    print(f'  分辨率: {fingerprint["screen_resolution"]}')
    print(f'  语言: {fingerprint["accept_language"]}')
    print(f'  时区: UTC{fingerprint["timezone"]}')
    print(f'  WebGL厂商: {fingerprint["webgl_vendor"]}')
    print(f'  WebGL渲染器: {fingerprint["webgl_renderer"]}')
    print(f'  CPU核心: {fingerprint["hardware_concurrency"]}')
    print(f'  设备内存: {fingerprint["device_memory"]}GB')
    print(f'  Canvas噪音: {fingerprint["canvas_noise"][:16]}...')
    print('=' * 50)


# ==================== 检测函数 ====================

def check_curl_cffi() -> bool:
    """检查 curl_cffi 是否可用"""
    return HAS_CURL_CFFI


def get_session_info(session) -> Dict:
    """获取 session 信息"""
    return {
        'type': 'curl_cffi' if HAS_CURL_CFFI and hasattr(session, '_impersonate') else 'requests',
        'headers': dict(session.headers),
        'proxies': getattr(session, 'proxies', None),
    }


# ==================== 测试 ====================

if __name__ == '__main__':
    print('浏览器指纹生成模块测试\n')

    # 检查 curl_cffi
    print(f'curl_cffi 可用: {check_curl_cffi()}')
    print()

    # 生成指纹
    fp = generate_random_fingerprint()
    print_fingerprint(fp)
    print()

    # 创建 session
    session, _ = create_session(verbose=True)
    print()

    # 测试请求
    print('测试请求 httpbin.org...')
    try:
        resp = session.get('https://httpbin.org/headers', timeout=10)
        if resp.status_code == 200:
            print('请求成功!')
            headers = resp.json().get('headers', {})
            print(f'  服务器收到的 User-Agent: {headers.get("User-Agent", "N/A")[:50]}...')
        else:
            print(f'请求失败: {resp.status_code}')
    except Exception as e:
        print(f'请求异常: {e}')

    print('\n测试完成!')
