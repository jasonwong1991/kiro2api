#!/usr/bin/env python3
# -*- coding: utf-8 -*-

"""
邮箱服务适配器 - 统一 email_utils 和 email_utils2 的接口

支持通过参数切换不同的邮箱服务提供商:
- provider1: email_utils.py (requests-based, mail.webwzw.tech)
- provider2: email_utils2.py (httpx-based, mail.chatgpt.org.uk)
- auto: 自动fallback (优先 provider1, 失败时切换到 provider2)
"""

import logging
from typing import Tuple, Optional

logger = logging.getLogger(__name__)


def get_email(email_name=None, proxies=None, provider='provider1') -> Tuple[Optional[str], Optional[str], Optional[str]]:
    """
    统一的邮箱获取接口

    Args:
        email_name: 指定的邮箱名（可选）
        proxies: 代理配置
        provider: 邮箱服务提供商 ('provider1' | 'provider2' | 'auto')

    Returns:
        (email, email_id, domain) 元组，失败返回 (None, None, None)
    """
    if provider == 'provider1' or provider == 'auto':
        try:
            from email_utils import get_email as _get_email
            result = _get_email(email_name, proxies)
            logger.info(f"使用 provider1 (email_utils) 获取邮箱成功")
            return result
        except Exception as e:
            logger.warning(f"Provider1 获取邮箱失败: {e}")
            if provider == 'auto':
                logger.info("尝试切换到 provider2...")
                return _get_email_provider2(proxies)
            raise

    elif provider == 'provider2':
        return _get_email_provider2(proxies)

    else:
        raise ValueError(f"不支持的 provider: {provider}，请使用 'provider1', 'provider2' 或 'auto'")


def get_code(id: str, proxies=None, provider='provider1') -> Optional[str]:
    """
    统一的验证码获取接口

    Args:
        id: 邮箱ID或邮箱地址
        proxies: 代理配置
        provider: 邮箱服务提供商 ('provider1' | 'provider2' | 'auto')

    Returns:
        6位数字验证码，如果未找到返回 None
    """
    if provider == 'provider1' or provider == 'auto':
        try:
            from email_utils import get_code as _get_code
            result = _get_code(id, proxies)
            if result:
                logger.debug(f"使用 provider1 (email_utils) 获取验证码成功")
            return result
        except Exception as e:
            logger.warning(f"Provider1 获取验证码失败: {e}")
            if provider == 'auto':
                logger.info("尝试切换到 provider2...")
                return _get_code_provider2(id, proxies)
            raise

    elif provider == 'provider2':
        return _get_code_provider2(id, proxies)

    else:
        raise ValueError(f"不支持的 provider: {provider}，请使用 'provider1', 'provider2' 或 'auto'")


# ==================== Provider2 适配器实现 ====================

def _get_email_provider2(proxies=None) -> Tuple[Optional[str], Optional[str], Optional[str]]:
    """
    使用 email_utils2 (TempEmailManager) 获取邮箱

    处理 httpx.Client 生命周期和类接口转换
    """
    try:
        import httpx
        from email_utils2 import TempEmailManager

        # 创建 httpx.Client (处理 proxies)
        # httpx 使用 'proxy' (单数) 参数，而不是 'proxies' (复数)
        client_kwargs = {}
        if proxies:
            if isinstance(proxies, dict):
                # requests 格式: {'http': 'http://...', 'https': 'http://...'}
                # httpx 使用单个 proxy 参数，优先使用 https 代理
                proxy_url = proxies.get('https') or proxies.get('http')
                if proxy_url:
                    client_kwargs['proxy'] = proxy_url
            elif isinstance(proxies, str):
                # 直接使用字符串格式的代理
                client_kwargs['proxy'] = proxies

        with httpx.Client(**client_kwargs, timeout=30) as client:
            manager = TempEmailManager(task_id="adapter")
            manager.renew(client)

            # 返回兼容格式: (email, email_id, domain)
            email = manager.email
            domain = manager.current_domain
            # email_utils2 没有单独的 id，使用 email 作为标识
            logger.info(f"使用 provider2 (email_utils2) 获取邮箱成功: {email}")
            return (email, email, domain)

    except Exception as e:
        logger.error(f"Provider2 获取邮箱失败: {e}")
        return (None, None, None)


def _get_code_provider2(email_id: str, proxies=None) -> Optional[str]:
    """
    使用 email_utils2 (TempEmailManager) 获取验证码

    处理 httpx.Client 生命周期和类接口转换
    """
    try:
        import httpx
        from email_utils2 import TempEmailManager

        # 创建 httpx.Client (处理 proxies)
        # httpx 使用 'proxy' (单数) 参数，而不是 'proxies' (复数)
        client_kwargs = {}
        if proxies:
            if isinstance(proxies, dict):
                # requests 格式: {'http': 'http://...', 'https': 'http://...'}
                # httpx 使用单个 proxy 参数，优先使用 https 代理
                proxy_url = proxies.get('https') or proxies.get('http')
                if proxy_url:
                    client_kwargs['proxy'] = proxy_url
            elif isinstance(proxies, str):
                # 直接使用字符串格式的代理
                client_kwargs['proxy'] = proxies

        with httpx.Client(**client_kwargs, timeout=30) as client:
            manager = TempEmailManager(task_id="adapter")
            # email_utils2 使用 email 地址而不是 id
            manager.email = email_id
            code = manager.get_code(client, timeout=90)
            if code:
                logger.debug(f"使用 provider2 (email_utils2) 获取验证码成功")
            return code

    except Exception as e:
        logger.error(f"Provider2 获取验证码失败: {e}")
        return None


# ==================== 导出接口 ====================

__all__ = ['get_email', 'get_code']
