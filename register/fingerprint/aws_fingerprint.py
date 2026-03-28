#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
AWS FWCIM Fingerprint Generator v5 - 参考 FingerprintJS 实现

基于 FingerprintJS 的指纹收集方式，生成更真实的 AWS fingerprint
- 使用真实的 Math 运算结果
- 生成稳定的 Canvas hash
- 包含完整的 WebGL 信息
- 模拟真实的用户交互模式

使用示例:
    from fingerprint import generate_random_fingerprint
    from aws_fingerprint import generate_fingerprint

    # 生成浏览器指纹
    browser_fp = generate_random_fingerprint()

    # 生成匹配的 AWS fingerprint
    aws_fp = generate_fingerprint(browser_fingerprint=browser_fp)
"""

import json
import base64
import random
import time
import math as math_module
import hashlib
import struct
from typing import Dict, List, Any, Optional


# XXTEA 常量
DELTA = 2654435769  # 0x9E3779B9
UINT32_MASK = 0xFFFFFFFF

# AWS FWCIM 密钥
KEY_IDENTIFIER = "ECdITeCs"
KEY_MATERIAL = [1888420705, 2576816180, 2347232058, 874813317]

# 前缀生成函数
# AWS FWCIM 使用 CRC32 校验和作为前缀
# 流程：JSON -> UTF-8 encode -> CRC32 -> HEX -> 前缀
# 格式：HEX(CRC32(UTF8(JSON))) + '#' + UTF8(JSON)
# 然后对整个字符串进行 XXTEA 加密
import zlib

def generate_crc32_prefix(json_data: str) -> str:
    """生成 CRC32 前缀，格式：XXXXXXXX#

    AWS FWCIM 对 UTF-8 编码的 JSON 数据计算 CRC32
    返回 8 位大写十六进制字符串 + '#'

    注意：CRC32 计算的是 UTF-8 编码后的字节数据
    """
    # 对 UTF-8 编码的 JSON 数据计算 CRC32
    utf8_data = json_data.encode('utf-8')
    crc = zlib.crc32(utf8_data) & 0xFFFFFFFF
    # 转换为 8 位大写十六进制
    return format(crc, '08X') + '#'

# 真实 ubid 前缀模式（从真实数据中提取）
_LS_UBID_CACHE: Optional[str] = None


def _generate_ls_ubid_base() -> str:
    """Generate the base part of lsUbid, e.g. `X11-2366055-1685079` (Xnn格式).

    真实样本格式:
    - X11-2366055-1685079
    - X56-1665820-0107075
    """
    # 真实格式: Xnn-nnnnnnn-nnnnnnn (X + 2位数字 + 7位数字 + 7位数字)
    prefix = f"X{random.randint(10, 99)}"
    part1 = f"{random.randint(1000000, 9999999)}"
    part2 = f"{random.randint(0000000, 9999999):07d}"
    return f"{prefix}-{part1}-{part2}"


def _normalize_ls_ubid(value: str, timestamp_seconds: int) -> str:
    value = (value or "").strip()
    if not value:
        return ""
    if ":" in value:
        return value
    return f"{value}:{timestamp_seconds}"


def _get_or_create_ls_ubid(
    provided: Optional[str],
    *,
    timestamp_seconds: int,
) -> str:
    global _LS_UBID_CACHE

    if provided:
        ls_ubid = _normalize_ls_ubid(provided, timestamp_seconds)
        _LS_UBID_CACHE = ls_ubid
        return ls_ubid

    if _LS_UBID_CACHE:
        return _LS_UBID_CACHE

    _LS_UBID_CACHE = f"{_generate_ls_ubid_base()}:{timestamp_seconds}"
    return _LS_UBID_CACHE


def uint32(n: int) -> int:
    return n & UINT32_MASK


def string_to_uint32_array(s: str) -> List[int]:
    padded_len = math_module.ceil(len(s) / 4) * 4
    s = s.ljust(padded_len, '\x00')
    result = []
    for i in range(0, len(s), 4):
        val = (ord(s[i]) & 0xFF) + \
              ((ord(s[i + 1]) & 0xFF) << 8) + \
              ((ord(s[i + 2]) & 0xFF) << 16) + \
              ((ord(s[i + 3]) & 0xFF) << 24)
        result.append(val)
    return result


def uint32_array_to_bytes(arr: List[int]) -> bytes:
    result = bytearray()
    for val in arr:
        result.append(val & 0xFF)
        result.append((val >> 8) & 0xFF)
        result.append((val >> 16) & 0xFF)
        result.append((val >> 24) & 0xFF)
    return bytes(result)


def xxtea_encrypt(data: str, key: List[int]) -> bytes:
    """
    XXTEA 加密 - 完全按照 JavaScript 实现

    JavaScript 代码（反混淆后）:
    var r = Math.ceil(str.length / 4);
    var rounds = Math.floor(6 + 52/r);
    while (rounds-- > 0) {
        sum += DELTA;
        var e = (sum >>> 2) & 3;
        for (var p = 0; p < r; p++) {
            y = a[(p+1) % r];
            z = a[p] += ((z>>>5 ^ y<<2) + (y>>>3 ^ z<<4)) ^ (sum^y) + (key[p&3 ^ e] ^ z);
        }
    }
    """
    if not data:
        return b''

    # 1. 将字符串转换为 uint32 数组（JavaScript 风格）
    r = math_module.ceil(len(data) / 4)
    a = []

    for i in range(r):
        # 获取4个字节，不足的默认为 0
        b0 = ord(data[4*i]) if 4*i < len(data) else 0
        b1 = ord(data[4*i+1]) if 4*i+1 < len(data) else 0
        b2 = ord(data[4*i+2]) if 4*i+2 < len(data) else 0
        b3 = ord(data[4*i+3]) if 4*i+3 < len(data) else 0

        # 小端序组合
        val = (b0 & 0xFF) + \
              ((b1 & 0xFF) << 8) + \
              ((b2 & 0xFF) << 16) + \
              ((b3 & 0xFF) << 24)

        a.append(uint32(val))

    # 2. XXTEA 加密
    rounds = math_module.floor(6 + 52 / r)
    sum_val = 0
    y = a[0]
    z = a[r - 1]

    for _ in range(rounds):
        sum_val = uint32(sum_val + DELTA)
        e = (sum_val >> 2) & 3

        for p in range(r):
            y = a[(p + 1) % r]
            mx = uint32(
                uint32((z >> 5 ^ y << 2) + (y >> 3 ^ z << 4)) ^
                uint32((sum_val ^ y) + (key[(p & 3) ^ e] ^ z))
            )
            a[p] = uint32(a[p] + mx)
            z = a[p]

    # 3. 转换回字节串（JavaScript 风格）
    result = []
    for i in range(r):
        b0 = a[i] & 0xFF
        b1 = (a[i] >> 8) & 0xFF
        b2 = (a[i] >> 16) & 0xFF
        b3 = (a[i] >> 24) & 0xFF
        result.append(bytes([b0, b1, b2, b3]))

    return b''.join(result)


def xxtea_decrypt(data: bytes, key: List[int] = None) -> str:
    """
    XXTEA 解密

    Args:
        data: 加密的字节数据
        key: 密钥（默认使用 KEY_MATERIAL）

    Returns:
        解密后的字符串
    """
    if key is None:
        key = KEY_MATERIAL

    if not data or len(data) < 8:
        return ''

    # 1. 将字节转换为 uint32 数组
    r = len(data) // 4
    a = []
    for i in range(r):
        val = (data[4*i] & 0xFF) + \
              ((data[4*i+1] & 0xFF) << 8) + \
              ((data[4*i+2] & 0xFF) << 16) + \
              ((data[4*i+3] & 0xFF) << 24)
        a.append(uint32(val))

    # 2. XXTEA 解密
    rounds = math_module.floor(6 + 52 / r)
    sum_val = uint32(DELTA * rounds)
    y = a[0]
    z = a[r - 1]

    for _ in range(rounds):
        e = (sum_val >> 2) & 3

        for p in range(r - 1, -1, -1):
            z = a[(p - 1) % r]
            mx = uint32(
                uint32((z >> 5 ^ y << 2) + (y >> 3 ^ z << 4)) ^
                uint32((sum_val ^ y) + (key[(p & 3) ^ e] ^ z))
            )
            a[p] = uint32(a[p] - mx)
            y = a[p]

        sum_val = uint32(sum_val - DELTA)

    # 3. 将 uint32 数组转换回字节
    result = []
    for i in range(r):
        b0 = a[i] & 0xFF
        b1 = (a[i] >> 8) & 0xFF
        b2 = (a[i] >> 16) & 0xFF
        b3 = (a[i] >> 24) & 0xFF
        result.append(bytes([b0, b1, b2, b3]))

    decrypted_bytes = b''.join(result)

    # 4. 转换为字符串，去掉尾部的空字节
    try:
        decrypted_str = decrypted_bytes.decode('utf-8', errors='ignore')
        # 去掉尾部的 null 字符
        decrypted_str = decrypted_str.rstrip('\x00')
        return decrypted_str
    except:
        return decrypted_bytes.decode('latin-1').rstrip('\x00')


def base64url_encode(data: bytes) -> str:
    """标准 Base64 编码（不是 URL-safe）

    注意：AWS 使用标准 Base64 编码（+ 和 /），而不是 URL-safe 编码（- 和 _）
    """
    encoded = base64.b64encode(data).decode('ascii')
    # 不进行 URL-safe 替换，保持原始的 + 和 /
    return encoded


def _parse_screen_resolution(resolution: str) -> tuple:
    """解析屏幕分辨率字符串，返回 (width, height)"""
    try:
        parts = resolution.lower().split('x')
        return int(parts[0]), int(parts[1])
    except:
        return 1920, 1080


def _generate_canvas_hash(canvas_noise: str) -> int:
    """根据 canvas_noise 生成稳定的 canvas hash

    参考 FingerprintJS 的 canvas 指纹生成方式：
    - 使用 murmur3 风格的哈希算法
    - 返回 32 位有符号整数
    """
    # 使用 MD5 生成稳定的哈希，然后转换为 32 位整数
    md5_hash = hashlib.md5(canvas_noise.encode()).digest()
    # 取前 4 字节，转换为有符号 32 位整数
    hash_val = struct.unpack('<i', md5_hash[:4])[0]
    return hash_val


def _generate_audio_fingerprint(canvas_noise: str) -> float:
    """生成音频指纹值

    参考 FingerprintJS 的 audio 指纹：
    - 使用 AudioContext 和 OscillatorNode 生成
    - 返回一个浮点数，通常在 124.0 左右
    """
    # 使用 canvas_noise 作为种子生成稳定的音频指纹
    random.seed(canvas_noise + "_audio")
    # 真实值范围：124.04347527516074 附近
    audio_fp = 124.04347 + random.uniform(-0.00001, 0.00001)
    random.seed()
    return audio_fp


def _generate_histogram_bins(canvas_noise: str) -> List[int]:
    """根据 canvas_noise 生成稳定的 histogram bins

    从真实样本提取 - 两个样本的 histogramBins 几乎完全相同
    说明这是基于硬件/浏览器的固定值，而不是随机生成的
    """
    # 使用真实样本的 histogramBins 数据（来自 act1_decrypted.json）
    # 这是一个 256 元素的数组，代表 canvas 像素的灰度分布
    real_bins = [
        14574, 35, 54, 37, 42, 67, 29, 26, 39, 31, 38, 49, 43, 36, 33, 67,
        55, 21, 66, 24, 24, 25, 23, 39, 48, 27, 44, 12, 31, 18, 48, 46,
        35, 55, 26, 12, 39, 27, 23, 29, 33, 33, 16, 10, 34, 24, 11, 35,
        33, 39, 34, 37, 66, 10, 21, 23, 21, 19, 6, 17, 50, 43, 11, 14,
        19, 18, 32, 7, 14, 15, 8, 17, 17, 17, 40, 15, 11, 12, 43, 66,
        30, 18, 19, 14, 31, 12, 15, 16, 20, 54, 31, 27, 17, 18, 13, 17,
        36, 20, 24, 113, 78, 29, 547, 25, 9, 39, 31, 16, 14, 15, 11, 36,
        23, 16, 17, 16, 15, 31, 20, 23, 70, 40, 17, 12, 31, 29, 29, 20,
        23, 59, 32, 54, 22, 13, 16, 38, 32, 34, 72, 15, 24, 17, 12, 16,
        14, 12, 25, 21, 10, 12, 60, 19, 7, 92, 9, 52, 29, 20, 8, 31,
        10, 13, 19, 22, 51, 35, 6, 14, 9, 15, 14, 29, 17, 40, 13, 22,
        60, 80, 7, 10, 9, 46, 9, 25, 9, 7, 18, 10, 7, 55, 15, 19,
        7, 11, 61, 22, 14, 9, 65, 7, 18, 55, 37, 49, 67, 71, 85, 7,
        25, 15, 8, 15, 8, 29, 37, 30, 16, 30, 20, 33, 27, 20, 68, 83,
        33, 67, 20, 42, 53, 23, 21, 39, 13, 39, 19, 35, 35, 16, 20, 72,
        124, 43, 46, 33, 63, 36, 33, 79, 37, 32, 79, 55, 43, 63, 40, 13191
    ]
    return real_bins


def _generate_math_fingerprint() -> Dict[str, str]:
    """生成 Math 指纹

    参考 FingerprintJS 的 math 指纹：
    - 使用特定的数学运算来检测浏览器引擎差异
    - 不同浏览器的浮点数精度略有不同
    """
    # 这些是 Chrome/V8 引擎的标准值
    # FingerprintJS 使用的数学运算
    return {
        # Math.acos(0.5) 的结果
        "acos": "1.0471975511965979",
        # Math.acosh(Math.E) 的结果
        "acosh": "1.6574544541530771",
        # Math.asin(0.5) 的结果
        "asin": "0.5235987755982989",
        # Math.asinh(1) 的结果
        "asinh": "0.881373587019543",
        # Math.atanh(0.5) 的结果
        "atanh": "0.5493061443340548",
        # Math.atan(2) 的结果
        "atan": "1.1071487177940904",
        # Math.sin(Math.E) 的结果
        "sin": "0.41078129050290885",
        # Math.sinh(Math.E) 的结果
        "sinh": "7.544137102816975",
        # Math.cos(Math.E) 的结果
        "cos": "0.9117339147869651",
        # Math.cosh(Math.E) 的结果
        "cosh": "7.6101251386622884",
        # Math.tan(Math.E) 的结果
        "tan": "0.45054953406980763",
        # Math.tanh(Math.E) 的结果
        "tanh": "0.9913289158005998",
        # Math.exp(Math.E) 的结果
        "exp": "15.154262241479259",
        # Math.expm1(Math.E) 的结果
        "expm1": "14.154262241479259",
        # Math.log1p(Math.E) 的结果
        "log1p": "1.3132616875182228",
    }


def _calculate_form_checksum(email: str) -> str:
    """计算表单字段的 CRC32 校验和

    AWS 使用 CRC32 算法计算输入值的校验和，用于检测自动化
    返回格式：8位大写十六进制字符串（如 "E922BFBE"）
    """
    import zlib
    # CRC32 计算
    crc = zlib.crc32(email.encode('utf-8')) & 0xFFFFFFFF
    # 转换为8位大写十六进制
    return format(crc, '08X')


def _get_gpu_info(webgl_vendor: str, webgl_renderer: str, platform: str = 'Win32') -> tuple:
    """转换 WebGL 信息为 AWS fingerprint 格式

    注意：Mac 和 Windows 的 GPU 报告格式不同
    - Mac: vendor="Intel Inc.", model="Intel(R) Iris(TM) Plus Graphics 640"
    - Windows: vendor="Google Inc. (NVIDIA)", model="ANGLE (NVIDIA, ... Direct3D9Ex vs_3_0 ps_3_0, nvd3dumx.dll)"
    """

    # Mac 系统使用原始格式
    if 'Mac' in platform:
        return webgl_vendor, webgl_renderer

    # Windows 系统使用 ANGLE 格式，使用 Direct3D9Ex + 驱动文件名
    import random

    # 为不同 GPU 生成驱动文件名
    if "Intel" in webgl_vendor:
        vendor = "Google Inc. (Intel)"
        # Intel 驱动文件名
        driver_files = [
            "igdumdim32.dll",
            "igdumdim64.dll",
            "igd10iumd32.dll",
            "igd10iumd64.dll"
        ]
        driver = random.choice(driver_files)
        # 可选：添加版本号
        if random.random() < 0.5:
            driver += f"-{random.randint(20, 31)}.{random.randint(0, 100)}.{random.randint(0, 100)}.{random.randint(1000, 9999)}"
        model = f"ANGLE (Intel, {webgl_renderer} Direct3D9Ex vs_3_0 ps_3_0, {driver})"
    elif "NVIDIA" in webgl_vendor:
        vendor = "Google Inc. (NVIDIA)"
        # NVIDIA 驱动文件名
        driver = "nvd3dumx.dll"
        # 可选：添加版本号
        if random.random() < 0.5:
            driver += f"-{random.randint(20, 31)}.{random.randint(0, 100)}.{random.randint(0, 100)}.{random.randint(1000, 9999)}"
        model = f"ANGLE (NVIDIA, {webgl_renderer} Direct3D9Ex vs_3_0 ps_3_0, {driver})"
    elif "AMD" in webgl_vendor:
        vendor = "Google Inc. (AMD)"
        # AMD 驱动文件名
        driver_files = [
            "aticfx32.dll",
            "aticfx64.dll",
            "atiumdag.dll",
            "atiumd64.dll"
        ]
        driver = random.choice(driver_files)
        # 可选：添加版本号
        if random.random() < 0.5:
            driver += f"-{random.randint(20, 31)}.{random.randint(0, 100)}.{random.randint(0, 100)}.{random.randint(1000, 9999)}"
        model = f"ANGLE (AMD, {webgl_renderer} Direct3D9Ex vs_3_0 ps_3_0, {driver})"
    else:
        vendor = f"Google Inc. ({webgl_vendor})"
        driver = "d3d9.dll"
        model = f"ANGLE ({webgl_vendor}, {webgl_renderer} Direct3D9Ex vs_3_0 ps_3_0, {driver})"

    return vendor, model


def generate_fwcim_data(
    browser_fingerprint: Dict[str, Any] = None,
    # 以下参数用于向后兼容，如果提供了 browser_fingerprint 则忽略这些参数
    user_agent: str = None,
    screen_width: int = None,
    screen_height: int = None,
    avail_height: int = None,
    referrer: str = None,  # 如果为 None，会根据 workflow_id 自动设置
    location: str = None,  # 如果为 None，会根据 workflow_id 自动生成
    ubid: str = None,
    workflow_id: str = None,  # 用于生成正确的 location URL
    page_hash: str = None,  # URL 的 hash 部分，如 #/signup/verify-otp
    interaction_level: str = "minimal",  # 交互复杂度 ("minimal", "normal", "rich")
    include_form: bool = False,  # 是否包含 form 字段（send-otp 时需要）
    email_value: str = None,  # 邮箱值，用于计算 checksum
    use_profile_template: bool = False,  # 保留参数但不使用
) -> Dict[str, Any]:
    """
    生成符合真实结构的 FWCIM 数据（完全自己生成，不使用模板）

    Args:
        browser_fingerprint: fingerprint.py 生成的完整浏览器指纹字典
        workflow_id: workflowID，用于生成正确的 location URL（关键！）
        page_hash: URL 的 hash 部分，如 "#/signup/verify-otp"
        其他参数用于向后兼容
    """

    # 如果提供了 browser_fingerprint，从中提取所有需要的信息
    if browser_fingerprint:
        user_agent = browser_fingerprint.get('user_agent')
        screen_resolution = browser_fingerprint.get('screen_resolution', '1920x1080')
        screen_width, screen_height = _parse_screen_resolution(screen_resolution)
        avail_height = screen_height - 40  # 任务栏高度
        timezone = int(browser_fingerprint.get('timezone', '-480'))
        canvas_noise = browser_fingerprint.get('canvas_noise', '')
        webgl_vendor = browser_fingerprint.get('webgl_vendor', 'Intel Inc.')
        webgl_renderer = browser_fingerprint.get('webgl_renderer', 'Intel(R) UHD Graphics 630')
        hardware_concurrency = browser_fingerprint.get('hardware_concurrency', 8)
        device_memory = browser_fingerprint.get('device_memory', 8)
        platform = browser_fingerprint.get('platform', 'Win32')

        # 重要：所有真实样本都使用固定的屏幕分辨率 2048-1152-1104
        # 覆盖从browser_fingerprint提取的值
        screen_width = 2048
        screen_height = 1152
        avail_height = 1104
    else:
        # 向后兼容：使用默认值
        user_agent = user_agent or "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
        screen_width = screen_width or 1920
        screen_height = screen_height or 1080
        avail_height = avail_height or 1040
        timezone = -480
        canvas_noise = ''.join(random.choices('0123456789abcdef', k=32))
        webgl_vendor = 'Intel Inc.'
        webgl_renderer = 'Intel(R) UHD Graphics 630'
        hardware_concurrency = 8
        device_memory = 8
        platform = 'Win32'

    # 生成 ubid（如果未提供）- 使用真实格式：X48-XXXXXXX-XXXXXXX
    # lsUbid is generated after timing is known (and cached for session stability)

    # 生成 location URL（关键！必须包含正确的 workflowID）
    if location is None:
        if workflow_id:
            location = f"https://profile.aws.amazon.com/?workflowID={workflow_id}"
            if page_hash:
                location += page_hash
        else:
            location = "https://profile.aws.amazon.com/"

    # 设置 referrer（如果未提供，根据 workflow_id 自动设置）
    # 有 workflow_id 说明用户从 signin 页面跳转过来
    if referrer is None:
        if workflow_id:
            referrer = "https://us-east-1.signin.aws/"
        else:
            referrer = "https://profile.aws.amazon.com/"

    now = int(time.time() * 1000)
    # 更真实的时间跨度：用户通常在页面上花费 1-10 分钟
    start = now - random.randint(60000, 600000)  # 1-10 分钟前
    end = now

    screen_info = f"{screen_width}-{screen_height}-{avail_height}-24-*-*-*"
    plugins_str = f"PDF Viewer Chrome PDF Viewer Chromium PDF Viewer Microsoft Edge PDF Viewer WebKit built-in PDF ||{screen_info}"

    # 生成 performance timing (基于 navigationStart)
    nav_start = start - random.randint(3000, 5000)

    # --- Sample-aligned timing + lsUbid ---
    if page_hash is None and isinstance(location, str) and "#" in location:
        page_hash = location[location.index("#"):]

    # Real samples: enter-email page always includes form telemetry
    if page_hash == "#/signup/enter-email":
        include_form = True

    end = int(time.time() * 1000)

    if page_hash == "#/signup/verify-otp":
        active_duration_ms = random.randint(60000, 90000)
        post_load_delay_ms = random.randint(30000, 60000)
        load_duration_ms = random.randint(30000, 33000)
    elif page_hash == "#/signup/enter-email":
        active_duration_ms = random.randint(3500, 6000)
        post_load_delay_ms = random.randint(1200, 1600)
        load_duration_ms = random.randint(3300, 4700)
    else:
        active_duration_ms = random.randint(60000, 600000)
        post_load_delay_ms = random.randint(1200, 60000)
        load_duration_ms = random.randint(3300, 4700)

    start = end - active_duration_ms
    load_event_end = start - post_load_delay_ms
    nav_start = load_event_end - load_duration_ms

    ls_ubid_offset_ms = random.randint(0, 200) if page_hash == "#/signup/enter-email" else random.randint(0, 2500)
    ls_ubid_timestamp_seconds = (load_event_end + ls_ubid_offset_ms) // 1000
    ls_ubid = _get_or_create_ls_ubid(ubid, timestamp_seconds=ls_ubid_timestamp_seconds)

    # 获取 GPU 信息（传入 platform 以区分 Mac/Windows 格式）
    gpu_vendor, gpu_model = _get_gpu_info(webgl_vendor, webgl_renderer, platform)

    # WebGL 扩展列表（来自真实 fingerprint，包含所有 35 个扩展）
    webgl_extensions = [
        "ANGLE_instanced_arrays", "EXT_blend_minmax", "EXT_clip_control",
        "EXT_color_buffer_half_float", "EXT_depth_clamp", "EXT_disjoint_timer_query",
        "EXT_float_blend", "EXT_frag_depth", "EXT_polygon_offset_clamp",
        "EXT_shader_texture_lod", "EXT_texture_compression_bptc",
        "EXT_texture_compression_rgtc", "EXT_texture_filter_anisotropic",
        "EXT_texture_mirror_clamp_to_edge", "EXT_sRGB", "KHR_parallel_shader_compile",
        "OES_element_index_uint", "OES_fbo_render_mipmap", "OES_standard_derivatives",
        "OES_texture_float", "OES_texture_float_linear", "OES_texture_half_float",
        "OES_texture_half_float_linear", "OES_vertex_array_object",
        "WEBGL_blend_func_extended", "WEBGL_color_buffer_float",
        "WEBGL_compressed_texture_s3tc", "WEBGL_compressed_texture_s3tc_srgb",
        "WEBGL_debug_renderer_info", "WEBGL_debug_shaders", "WEBGL_depth_texture",
        "WEBGL_draw_buffers", "WEBGL_lose_context", "WEBGL_multi_draw",
        "WEBGL_polygon_mode"
    ]

    # 生成稳定的 canvas 数据（基于 canvas_noise）
    canvas_hash = _generate_canvas_hash(canvas_noise)
    histogram_bins = _generate_histogram_bins(canvas_noise)

    # 如果包含 form 字段，自动使用 rich 交互级别
    if include_form:
        interaction_level = "rich"

    # 生成交互数据（根据 interaction_level 调整复杂度）
    # minimal: 页面刚加载，用户还没有太多交互
    # normal: 用户浏览页面，有一些点击和鼠标移动
    # rich: 用户填写表单，有大量交互（适用于 send-otp）

    if interaction_level == "minimal":
        # 根据真实数据：verify-otp页面有2个点击，3个mouseCycles，1个keyCycle
        num_clicks = 2
        num_key_presses = 1
        num_copies = 0
        num_pastes = 0
        mouse_cycles = [random.randint(70, 90) for _ in range(3)]  # 3个mouseCycles
        key_cycles = [random.randint(80, 95)]  # 1个keyCycle
        key_intervals = []  # keyPresses=1时没有intervals
        mouse_positions = [
            f"{random.randint(600, 700)},{random.randint(270, 285)}",
            f"{random.randint(530, 560)},{random.randint(275, 290)}"
        ]
        # Make one mouseCycle smaller (sample often has a ~40-50 value)
        mouse_cycles = [random.randint(70, 90), random.randint(40, 55), random.randint(70, 90)]

    elif interaction_level == "normal":
        num_clicks = random.randint(2, 4)
        num_key_presses = random.randint(0, 5)
        num_copies = 0
        num_pastes = 0
        mouse_cycles = [random.randint(60, 150) for _ in range(random.randint(3, 6))]
        key_cycles = [random.randint(50, 150) for _ in range(num_key_presses)] if num_key_presses > 0 else []
        key_intervals = [random.randint(50, 300) for _ in range(num_key_presses - 1)] if num_key_presses > 1 else []
        mouse_positions = [f"{random.randint(100, 800)},{random.randint(100, 600)}" for _ in range(num_clicks)]

    else:  # rich - 用于 send-otp / enter-email
        # 根据真实数据 act1 和 act2：
        # act1: mouseClickPositions: ["116,253", "232,322", "226,365"]
        # act2: mouseClickPositions: ["125,371", "176,432", "200,483"]
        num_clicks = 3  # 真实数据：3次点击
        num_key_presses = 4  # 真实数据：4次按键（act1和act2都是4）
        num_copies = 1  # 真实数据：Interaction层面有1次复制
        num_pastes = 1  # 真实数据：1次粘贴
        # 真实数据 mouseCycles: [521, 111, 102] 和 [514, 98, 85]
        mouse_cycles = [random.randint(500, 530), random.randint(90, 120), random.randint(80, 110)]
        # 真实数据 keyCycles: [230, 127, 206, 114] 和 [271, 124, 211, 105]
        key_cycles = [random.randint(220, 280), random.randint(110, 140), random.randint(190, 220), random.randint(100, 130)]
        # 真实数据 keyPressTimeIntervals: [104, 401, 93] 和 [128, 424, 95]
        key_intervals = [random.randint(90, 140), random.randint(380, 440), random.randint(85, 110)]
        # 真实数据使用整数格式，范围符合按钮位置
        mouse_positions = [
            f"{random.randint(110, 130)},{random.randint(250, 380)}",
            f"{random.randint(170, 240)},{random.randint(320, 440)}",
            f"{random.randint(190, 230)},{random.randint(360, 490)}"
        ]

    # metrics 字段：记录 fingerprint 各部分生成耗时（毫秒）
    # 第一次生成时有耗时，后续生成时全为 0（数据已缓存）
    # 根据 page_hash 判断是否是第一次生成
    is_first_generation = page_hash in [None, "#/signup/start"]

    if is_first_generation:
        # 第一次生成：模拟真实的初始化耗时
        metrics = {
            "el": 0,
            "script": random.randint(1, 2),  # 脚本加载
            "h": 0,
            "batt": random.randint(6, 10),  # 电池信息
            "perf": 0,
            "auto": 0,
            "tz": 0,
            "fp2": random.randint(1, 3),  # fingerprint 生成
            "lsubid": random.randint(1, 2),  # ubid 处理
            "browser": 0,
            "capabilities": random.randint(4, 6),  # 浏览器能力检测
            "gpu": 0,
            "dnt": 0,
            "math": 0,
            "tts": 0,
            "input": random.randint(10, 15),  # 输入事件处理
            "canvas": random.randint(5, 8),  # canvas 指纹
            "captchainput": 0,
            "pow": 0
        }
    else:
        # 后续生成：全为 0（数据已缓存）
        metrics = {
            "el": 0, "script": 0, "h": 0, "batt": 0, "perf": 0,
            "auto": 0, "tz": 0, "fp2": 0, "lsubid": 0, "browser": 0,
            "capabilities": 0, "gpu": 0, "dnt": 0, "math": 0, "tts": 0,
            "input": 0, "canvas": 0, "captchainput": 0, "pow": 0
        }

    data = {
        "metrics": metrics,
        "start": start,
        "interaction": {
            "clicks": num_clicks,
            "touches": 0,
            "keyPresses": num_key_presses,
            "cuts": 0,
            "copies": num_copies,
            "pastes": num_pastes,
            "keyPressTimeIntervals": key_intervals,
            "mouseClickPositions": mouse_positions,
            "keyCycles": key_cycles,
            "mouseCycles": mouse_cycles,
            "touchCycles": []
        },
        "scripts": {
            # 使用真实的脚本文件名（方案1：固定哈希）
            "dynamicUrls": ["/dist/main/app_64791f709dbceeb174b0.min.js"],
            "inlineHashes": [],
            "elapsed": 1 if is_first_generation else 0,  # 修复：第一次生成时为 1，后续为 0
            "dynamicUrlCount": 1,
            "inlineHashesCount": 0
        },
        "history": {"length": random.randint(5, 7)},  # 真实数据：5-7之间变化
        "battery": {},
        "performance": {
            "timing": {
                # 添加随机偏移，模拟真实的网络延迟和加载时间
                "connectStart": nav_start + random.randint(5, 10),
                "secureConnectionStart": nav_start + random.randint(30, 45),
                "unloadEventEnd": 0,
                "domainLookupStart": nav_start + random.randint(4, 8),
                "domainLookupEnd": nav_start + random.randint(4, 8),
                "responseStart": nav_start + random.randint(900, 1200),
                "connectEnd": nav_start + random.randint(350, 500),
                "responseEnd": nav_start + random.randint(900, 1200),
                "requestStart": nav_start + random.randint(350, 500),
                "domLoading": nav_start + random.randint(1100, 1400),
                "redirectStart": 0,
                "loadEventEnd": nav_start + random.randint(4000, 4800),
                "domComplete": nav_start + random.randint(4000, 4800),
                "navigationStart": nav_start,
                "loadEventStart": nav_start + random.randint(4000, 4800),
                "domContentLoadedEventEnd": nav_start + random.randint(4000, 4800),
                "unloadEventStart": 0,
                "redirectEnd": 0,
                "domInteractive": nav_start + random.randint(3900, 4700),
                "fetchStart": nav_start + random.randint(4, 8),
                "domContentLoadedEventStart": nav_start + random.randint(3900, 4700)
            }
        },
        "automation": {
            "wd": {"properties": {"document": [], "window": [], "navigator": []}},
            "phantom": {"properties": {"window": []}}
        },
        "end": end,
        "timeZone": timezone // 60 if abs(timezone) > 24 else timezone,  # 转换为小时偏移
        "flashVersion": None,
        "plugins": plugins_str,
        "dupedPlugins": plugins_str,
        "screenInfo": screen_info,
        "lsUbid": f"{ubid}:{start // 1000}",  # 修复：添加时间戳（秒级），格式：X04-9961155-0558498:1766538297
        "referrer": referrer,
        "userAgent": user_agent,
        "location": location,
        "webDriver": False,
        "capabilities": {
            "css": {
                "textShadow": 1, "WebkitTextStroke": 1, "boxShadow": 1,
                "borderRadius": 1, "borderImage": 1, "opacity": 1,
                "transform": 1, "transition": 1
            },
            "js": {
                "audio": True, "geolocation": True, "localStorage": "supported",
                "touch": False, "video": True, "webWorker": True
            },
            "elapsed": 0
        },
        "gpu": {
            "vendor": gpu_vendor,
            "model": gpu_model,
            "extensions": webgl_extensions
        },
        "dnt": None,
        "math": {
            # 参考 FingerprintJS 的 math 指纹
            # 这些是 Chrome/V8 引擎在 Windows 上的标准值
            # AWS 使用 tan, sin, cos 三个值
            "tan": "-1.4214488238747245",
            "sin": "0.8178819121159085",
            "cos": "-0.5753861119575491"
        },
    }

    # 根据 page_hash 决定是否添加 form 字段
    # verify-otp 页面：有form字段，但没有交互（用户还没输入）
    # send-otp 页面：有form字段，且有交互（用户已输入邮箱）

    if page_hash == "#/signup/verify-otp":
        # verify-otp 页面：有form字段，但所有交互都是0
        form_field_id = f"formField{random.randint(20, 60)}-{start}-{random.randint(1000, 9999)}"
        data["form"] = {
            form_field_id: {
                "clicks": 0,
                "touches": 0,
                "keyPresses": 0,
                "cuts": 0,
                "copies": 0,
                "pastes": 0,
                "keyPressTimeIntervals": [],
                "mouseClickPositions": [],
                "keyCycles": [],
                "mouseCycles": [],
                "touchCycles": [],
                "width": 174,
                "height": 32,
                "totalFocusTime": random.randint(5, 10),  # 真实数据：verify-otp页面通常5-10ms
                "prefilled": False
            }
        }
    elif include_form:
        # send-otp 页面：有form字段，且有交互
        form_field_id = f"formField{random.randint(20, 40)}-{start}-{random.randint(1000, 9999)}"

        if email_value:
            # 用户已输入邮箱：有较多交互
            data["form"] = {
                form_field_id: {
                    "clicks": 2,  # 真实数据：2次点击
                    "touches": 0,
                    "keyPresses": 3,  # 真实数据：3次按键
                    "cuts": 0,
                    "copies": 0,
                    "pastes": 1,  # 真实数据：用户粘贴了邮箱
                    "keyPressTimeIntervals": [random.randint(180, 190), random.randint(1550, 1560)],  # 2个间隔
                    "mouseClickPositions": [
                        f"{random.uniform(100, 120):.14f},{random.uniform(8, 12):.14f}",
                        f"{random.uniform(60, 80):.14f},{random.uniform(25, 30):.14f}"
                    ],
                    "keyCycles": [random.randint(320, 330), random.randint(85, 90), random.randint(60, 65)],  # 3个周期
                    "mouseCycles": [random.randint(100, 110), random.randint(970, 980)],
                    "touchCycles": [],
                    "width": 174,
                    "height": 32,
                    "totalFocusTime": random.randint(5, 508),  # 真实数据：5-508ms
                    "checksum": _calculate_form_checksum(email_value),  # 计算邮箱的 CRC32 校验和
                    "autocomplete": False,
                    "prefilled": False
                }
            }
        else:
            # 用户未输入邮箱：只有少量交互（如点击输入框）
            data["form"] = {
                form_field_id: {
                    "clicks": 0,
                    "touches": 0,
                    "keyPresses": 0,
                    "cuts": 0,
                    "copies": 0,
                    "pastes": 0,
                    "keyPressTimeIntervals": [],
                    "mouseClickPositions": [],
                    "keyCycles": [],
                    "mouseCycles": [],
                    "touchCycles": [],
                    "width": 174,
                    "height": 32,
                    "totalFocusTime": random.randint(5, 508),  # 真实数据：5-508ms
                    "checksum": None,  # 未输入时为 null
                    "prefilled": False
                }
            }
        # 添加 timeToSubmit 字段（从页面加载到提交的时间）
        data["timeToSubmit"] = end - start  # 真实数据：9281ms
    else:
        # 其他页面：没有form字段
        data["form"] = {}

    # 添加其他字段
    data.update({
        "canvas": {
            "hash": canvas_hash,
            "emailHash": None,
            "histogramBins": histogram_bins
        },
        "token": {
            "isCompatible": True,
            "pageHasCaptcha": 0
        },
        "auth": {
            "form": {"method": "get"}
        },
        "errors": [],
        "version": "4.0.0"
    })

    # Normalize lsUbid + performance.timing (act sample aligned)
    data["lsUbid"] = ls_ubid

    fetch_offset = random.randint(4, 8)
    fetch_start = nav_start + fetch_offset
    connect_start = nav_start + fetch_offset + random.randint(0, 2)
    secure_connection_start = nav_start + random.randint(620, 720)
    connect_end = nav_start + random.randint(900, 1200)
    request_start = connect_end
    response_start = nav_start + random.randint(1400, 1700)
    if response_start < request_start:
        response_start = request_start + random.randint(0, 50)
    response_end = response_start + random.randint(0, 30)
    dom_loading = response_end + random.randint(4, 10)

    dom_interactive = load_event_end - random.randint(5, 8)
    dom_content_loaded_start = dom_interactive + random.randint(0, 1)

    data["performance"]["timing"] = {
        "connectStart": connect_start,
        "secureConnectionStart": secure_connection_start,
        "unloadEventEnd": 0,
        "domainLookupStart": fetch_start,
        "domainLookupEnd": fetch_start,
        "responseStart": response_start,
        "connectEnd": connect_end,
        "responseEnd": response_end,
        "requestStart": request_start,
        "domLoading": dom_loading,
        "redirectStart": 0,
        "loadEventEnd": load_event_end,
        "domComplete": load_event_end,
        "navigationStart": nav_start,
        "loadEventStart": load_event_end,
        "domContentLoadedEventEnd": load_event_end,
        "unloadEventStart": 0,
        "redirectEnd": 0,
        "domInteractive": dom_interactive,
        "fetchStart": fetch_start,
        "domContentLoadedEventStart": dom_content_loaded_start,
    }

    # Page-specific normalization (act sample aligned)
    if page_hash == "#/signup/enter-email":
        field_ts = start - random.randint(5, 15)
        form_field_id = f"formField29-{field_ts}-{random.randint(1000, 9999)}"
        checksum = _calculate_form_checksum(email_value) if email_value else "49D9BDB1"

        x_str = f"{random.randint(40, 120)}.59999084472656"
        y_str = random.choice(["8", "12.399993896484375"])

        data["form"] = {
            form_field_id: {
                "clicks": 1,
                "touches": 0,
                "keyPresses": 2,
                "cuts": 0,
                "copies": 0,
                "pastes": 1,
                "keyPressTimeIntervals": [random.randint(85, 110)],
                "mouseClickPositions": [f"{x_str},{y_str}"],
                "keyCycles": [random.randint(190, 220), random.randint(95, 120)],
                "mouseCycles": [random.randint(85, 120)],
                "touchCycles": [],
                "width": 174,
                "height": 32,
                "totalFocusTime": random.randint(350, 520),
                "checksum": checksum,
                "autocomplete": False,
                "prefilled": False,
            }
        }
        data["timeToSubmit"] = end - start
    elif page_hash == "#/signup/verify-otp":
        # Real samples typically don't include timeToSubmit on verify-otp
        data.pop("timeToSubmit", None)

    return data


def generate_fingerprint(
    browser_fingerprint: Dict[str, Any] = None,
    user_agent: str = None,
    workflow_id: str = None,
    page_hash: str = None,
    ubid: str = None,
    **kwargs
) -> str:
    """
    生成 AWS FWCIM fingerprint

    Args:
        browser_fingerprint: fingerprint.py 生成的完整浏览器指纹字典（推荐使用）
        user_agent: User-Agent 字符串（向后兼容，如果提供了 browser_fingerprint 则忽略）
        workflow_id: workflowID，用于生成正确的 location URL（关键！避免 TES 检测）
        page_hash: URL 的 hash 部分，如 "#/signup/verify-otp"
        ubid: 用户浏览器 ID，如果不提供会自动生成真实格式
        **kwargs: 其他参数传递给 generate_fwcim_data

    Returns:
        加密后的 fingerprint 字符串，格式: "ECdITeCs:xxxxx"

    Example:
        from fingerprint import generate_random_fingerprint

        # 生成浏览器指纹
        browser_fp = generate_random_fingerprint()

        # 生成匹配的 AWS fingerprint（带 workflowID）
        aws_fp = generate_fingerprint(
            browser_fingerprint=browser_fp,
            workflow_id="41364750-1057-4abb-9c8c-e7ceb49bf92e",
            page_hash="#/signup/verify-otp"
        )
    """

    # 如果提供了 browser_fingerprint，使用它
    if browser_fingerprint:
        data = generate_fwcim_data(
            browser_fingerprint=browser_fingerprint,
            workflow_id=workflow_id,
            page_hash=page_hash,
            ubid=ubid,
            **kwargs
        )
    else:
        # 向后兼容
        data = generate_fwcim_data(
            user_agent=user_agent,
            workflow_id=workflow_id,
            page_hash=page_hash,
            ubid=ubid,
            **kwargs
        )

    json_str = json.dumps(data, separators=(',', ':'))

    # AWS FWCIM 编码格式（从源码分析 - 第 70 模块）：
    # 流程：
    # 1. JSON -> jsonEncoder.encode() -> JSON string
    # 2. JSON string -> utf8Encoder.encode() -> UTF-8 bytes (作为字符串处理)
    # 3. UTF-8 bytes -> crc32.calculate() -> CRC32 值
    # 4. CRC32 值 -> hexEncoder.encode() -> 8位大写十六进制
    # 5. 格式：HEX + '#' + UTF8_STRING
    # 6. 整个字符串 -> XXTEA 加密 -> Base64 编码

    # 关键：UTF-8 编码后的字节需要作为字符串处理（每个字节作为一个字符）
    # 这是因为 JavaScript 的 String.fromCharCode 会将每个字节转换为字符

    # 将 JSON 字符串转换为 UTF-8 字节
    utf8_bytes = json_str.encode('utf-8')

    # 计算 CRC32（对 UTF-8 字节）
    crc = zlib.crc32(utf8_bytes) & 0xFFFFFFFF
    crc_hex = format(crc, '08X')

    # 将 UTF-8 字节转换为字符串（每个字节作为一个字符）
    # 使用 latin-1 编码可以保持字节值不变
    utf8_as_str = ''.join(chr(b) for b in utf8_bytes)

    # 组合：CRC32_HEX + '#' + UTF8_STRING
    full_data = crc_hex + '#' + utf8_as_str

    # XXTEA 加密
    encrypted = xxtea_encrypt(full_data, KEY_MATERIAL)

    # Base64 编码
    base64_str = base64url_encode(encrypted)

    return f"{KEY_IDENTIFIER}:{base64_str}"


if __name__ == "__main__":
    print("AWS FWCIM Fingerprint Generator v3")
    print("=" * 60)

    # 测试：模拟 fingerprint.py 生成的指纹
    test_browser_fp = {
        'user_agent': 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.6261.112 Safari/537.36',
        'screen_resolution': '1920x1080',
        'timezone': '-480',
        'accept_language': 'en-US,en;q=0.9',
        'platform': 'Win32',
        'chrome_version': '122',
        'canvas_noise': 'a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4',
        'webgl_vendor': 'Intel Inc.',
        'webgl_renderer': 'Intel(R) UHD Graphics 630',
        'hardware_concurrency': 8,
        'device_memory': 16,
    }

    print(f"\n浏览器指纹:")
    print(f"  User-Agent: {test_browser_fp['user_agent'][:60]}...")
    print(f"  屏幕分辨率: {test_browser_fp['screen_resolution']}")
    print(f"  平台: {test_browser_fp['platform']}")
    print(f"  GPU: {test_browser_fp['webgl_vendor']} / {test_browser_fp['webgl_renderer']}")

    fp = generate_fingerprint(browser_fingerprint=test_browser_fp)
    print(f"\n生成的 AWS fingerprint:")
    print(f"  Identifier: {fp.split(':')[0]}")
    print(f"  Encrypted 长度: {len(fp.split(':')[1])}")
    print(f"  总长度: {len(fp)}")

    # 验证数据结构
    data = generate_fwcim_data(browser_fingerprint=test_browser_fp)
    print(f"\n数据结构验证:")
    print(f"  版本: {data['version']}")
    print(f"  userAgent: {data['userAgent'][:50]}...")
    print(f"  screenInfo: {data['screenInfo']}")
    print(f"  timeZone: {data['timeZone']}")
    print(f"  webDriver: {data['webDriver']}")
    print(f"  canvas.hash: {data['canvas']['hash']}")
    print(f"  gpu.vendor: {data['gpu']['vendor']}")
    print(f"  gpu.model: {data['gpu']['model'][:50]}...")
