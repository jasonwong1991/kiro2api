#!/usr/bin/env python3
# -*- coding: utf-8 -*-

import json
import time
import uuid
import base64
import logging
import random
import os
import secrets
import hashlib
import datetime
import urllib.parse
import string
import re
import argparse
import multiprocessing
from urllib.parse import urlparse, parse_qs
from pathlib import Path
from threading import Lock

from fingerprint.fingerprint import create_session, generate_random_fingerprint
from fingerprint.aws_fingerprint import generate_fingerprint
from JWE import encrypt_password
from email_gen.generator import gen_email_prefix
from email_adapter import get_email, get_code

logging.basicConfig(
    level=logging.INFO,
    format='%(asctime)s [%(levelname)s] %(message)s',
    handlers=[
        logging.FileHandler('registration.log', encoding='utf-8'),
        logging.StreamHandler()
    ]
)
logger = logging.getLogger(__name__)


class _ConsoleVerbosityFilter(logging.Filter):
    def __init__(self, verbose: bool):
        super().__init__()
        self.verbose = verbose

        # 仅用于控制台输出的降噪过滤器（文件日志不应使用该过滤器）
        self._blocked_substrings = (
            "请求 URL",
            "请求 /",
            "final_url",
            "redirectUrl",
            "redirect_url",
            "authCode:",
            "state:",
            "csrfToken",
            "loginCsrfToken",
            "workflowStateHandle:",
            "whoAmI body:",
            "body:",
            "redirects:",
            "portal page:",
            "portal final_url:",
            "portal redirectUrl:",
            "signin redirect",
            "✓ set loginCsrfToken cookie",
        )

    def filter(self, record: logging.LogRecord) -> bool:
        if self.verbose:
            return True
        if record.levelno >= logging.WARNING:
            return True
        try:
            msg = record.getMessage()
        except Exception:
            return True
        return not any(s in msg for s in self._blocked_substrings)


def load_env():
    """从 .env 文件加载配置"""
    env_path = Path(__file__).parent / ".env"
    config = {}
    if env_path.exists():
        with open(env_path, 'r', encoding='utf-8') as f:
            for line in f:
                line = line.strip()
                if line and not line.startswith('#') and '=' in line:
                    k, v = line.split('=', 1)
                    config[k.strip()] = v.strip()
    return config


def get_proxy_from_env():
    """从环境变量获取代理配置"""
    env = load_env()

    # 优先使用完整的代理URL
    proxy_url = env.get('PROXY_URL', '')
    if proxy_url:
        return proxy_url

    # 否则使用分开的配置
    proxy_str = env.get('PROXY', '')
    if not proxy_str:
        return None

    proxy_type = env.get('PROXY_TYPE', 'http')
    parts = proxy_str.split(':')

    if len(parts) == 4:
        host, port, user, pwd = parts
        return f"{proxy_type}://{user}:{pwd}@{host}:{port}"
    elif len(parts) == 2:
        return f"{proxy_type}://{parts[0]}:{parts[1]}"

    return None


def parse_proxy(proxy_str: str) -> str:
    """
    解析代理字符串，支持多种格式:
    - 完整URL: socks5://user:pass@host:port, http://user:pass@host:port
    - 简单格式: host:port
    - 认证格式: host:port:user:pass
    - 用户@主机格式: user:pass@host:port
    """
    if not proxy_str:
        return None

    # 如果已经是完整的代理URL格式，直接返回
    if proxy_str.startswith('http://') or proxy_str.startswith('https://') or \
       proxy_str.startswith('socks5://') or proxy_str.startswith('socks4://'):
        return proxy_str

    # 检查是否是 user:pass@host:port 格式
    if '@' in proxy_str:
        # 格式: user:pass@host:port
        auth_part, host_part = proxy_str.rsplit('@', 1)
        if ':' in host_part:
            return f"http://{auth_part}@{host_part}"
        else:
            logger.warning(f"无法识别的代理格式: {proxy_str}")
            return None

    parts = proxy_str.split(':')
    if len(parts) == 4:
        # 格式: host:port:user:pass
        host, port, username, password = parts
        return f"http://{username}:{password}@{host}:{port}"
    elif len(parts) == 2:
        # 格式: host:port
        host, port = parts
        return f"http://{host}:{port}"
    else:
        logger.warning(f"无法识别的代理格式: {proxy_str}")
        return None


def generate_code_verifier():
    """生成 PKCE code_verifier (32字节随机数的base64url编码)"""
    random_bytes = secrets.token_bytes(32)
    return base64.urlsafe_b64encode(random_bytes).rstrip(b'=').decode('utf-8')


def generate_code_challenge(code_verifier):
    """生成 PKCE code_challenge (code_verifier的SHA256哈希的base64url编码)"""
    digest = hashlib.sha256(code_verifier.encode('utf-8')).digest()
    return base64.urlsafe_b64encode(digest).rstrip(b'=').decode('utf-8')


class KiroRegistration:
    """完整注册流程 - 使用临时邮箱"""

    # API 端点
    OIDC_BASE = "https://oidc.us-east-1.amazonaws.com"
    SSO_PORTAL = "https://portal.sso.us-east-1.amazonaws.com"
    SIGNIN_BASE = "https://us-east-1.signin.aws"
    PROFILE_BASE = "https://profile.aws.amazon.com"

    def __init__(self, proxy: str = None, verbose: bool = True):
        self.proxy = parse_proxy(proxy)
        self.verbose = verbose

        # 仅对控制台输出降噪：registration.log 仍保留全部 INFO 细节
        root_logger = logging.getLogger()
        for handler in root_logger.handlers:
            # FileHandler 也是 StreamHandler 的子类，这里显式排除，避免文件日志被过滤
            if type(handler) is logging.StreamHandler:
                if not any(isinstance(f, _ConsoleVerbosityFilter) for f in getattr(handler, "filters", [])):
                    handler.addFilter(_ConsoleVerbosityFilter(verbose=verbose))

        # 生成随机浏览器指纹
        self.browser_fingerprint = generate_random_fingerprint()

        # 创建 HTTP Session
        self.session, _ = create_session(
            proxy=self.proxy,
            use_curl_cffi=False,
            fingerprint=self.browser_fingerprint,
            verbose=verbose
        )

        # 生成 ubid
        self.ubid = self._generate_ubid()
        self.visitor_id = str(uuid.uuid4())

        # OIDC 相关
        self.client_id = None
        self.client_secret = None
        self.device_code = None
        self.user_code = None
        self.verification_uri = None
        self.device_expires_in = None
        self.device_interval = None

        # Workflow 相关
        self.directory_id = "d-9067642ac7"
        self.workflow_state_handle = None
        self.workflow_id = None
        self.workflow_state = None

        # 注册相关
        self.registration_code = None
        self.sign_in_state = None
        self.auth_code = None  # workflowResultHandle
        self.redirect_state = None
        self.login_workflow_state_handle = None
        self.wdc_csrf_token = None
        self.sso_state = None

        # Token 相关
        self.sso_token = None
        self.access_token = None
        self.refresh_token = None
        self.expires_in = None
        self.portal_bearer = None  # x-amz-sso_authn
        self.portal_login_csrf_token = None  # portal /login csrfToken (also used as x-amz-sso-csrf-token)

        # 密码加密相关
        self.public_key_jwk = None
        self.issuer = None
        self.audience = None

        logger.info("=" * 60)
        logger.info("=" * 60)
        logger.info(f"浏览器: {self.browser_fingerprint['platform']} / Chrome {self.browser_fingerprint['chrome_version']}")
        logger.info(f"会话ID: {self.ubid}")
        logger.info("=" * 60)

    def _generate_ubid(self) -> str:
        """生成 ubid，格式: XXX-XXXXXXX-XXXXXXX (纯数字)"""
        return f"{random.randint(100,999)}-{random.randint(1000000,9999999)}-{random.randint(1000000,9999999)}"

    def _generate_auth_headers(self) -> dict:
        """生成 OIDC 认证请求头"""
        sdk_version = f"1.3.{random.randint(5,9)}"
        rust_version = f"1.{random.randint(83,88)}.0"
        return {
            "content-type": "application/json",
            "user-agent": f"aws-sdk-rust/{sdk_version} os/windows lang/rust/{rust_version}",
            "amz-sdk-request": "attempt=1; max=3",
            "amz-sdk-invocation-id": str(uuid.uuid4()),
        }

    def _generate_fingerprint(self, **kwargs) -> str:
        """生成 AWS fingerprint"""
        return generate_fingerprint(
            browser_fingerprint=self.browser_fingerprint,
            ubid=self.ubid,
            **kwargs
        )
    def step1_register_oidc_client(self) -> bool:
        """Step 1:  OIDC 客户端"""
        logger.info("\n[Step 1]  OIDC 客户端...")

        try:
            headers = self._generate_auth_headers()
            payload = {
                "clientName": "Amazon Q Developer for command line",
                "clientType": "public",
                "scopes": [
                    "codewhisperer:completions",
                    "codewhisperer:analysis",
                    "codewhisperer:conversations",
                    "codewhisperer:transformations",
                    "codewhisperer:taskassist",
                ],
                "grantTypes": ["urn:ietf:params:oauth:grant-type:device_code", "refresh_token"],
                "issuerUrl": "https://view.awsapps.com/start",
            }

            resp = self.session.post(
                f"{self.OIDC_BASE}/client/register",
                headers=headers,
                json=payload,
                timeout=30
            )

            if resp.status_code != 200:
                logger.error(f"  失败: HTTP {resp.status_code}")
                return False

            data = resp.json()
            self.client_id = data["clientId"]
            self.client_secret = data["clientSecret"]

            logger.info(f"  ✓ Client ID: {self.client_id[:20]}...")
            logger.info(f"  ✓ Client Secret: {self.client_secret[:20]}...")
            return True

        except Exception as e:
            logger.error(f"  Step 1 失败: {e}")
            return False

    def step2_device_authorization(self) -> bool:
        """Step 2: 设备授权"""
        logger.info("\n[Step 2] 获取设备授权...")

        try:
            headers = self._generate_auth_headers()
            payload = {
                "clientId": self.client_id,
                "clientSecret": self.client_secret,
                "startUrl": "https://view.awsapps.com/start",
            }

            resp = self.session.post(
                f"{self.OIDC_BASE}/device_authorization",
                headers=headers,
                json=payload,
                timeout=30
            )

            if resp.status_code != 200:
                logger.error(f"  授权失败: HTTP {resp.status_code}")
                return False

            data = resp.json()
            self.device_code = data['deviceCode']
            self.user_code = data['userCode']
            self.verification_uri = data.get('verificationUriComplete') or data.get('verificationUri')
            self.device_expires_in = data.get('expiresIn')
            self.device_interval = data.get('interval')

            logger.info(f"  ✓ User Code: {self.user_code}")
            logger.info(f"  ✓ 授权链接: {self.verification_uri}")
            return True

        except Exception as e:
            logger.error(f"  Step 2 失败: {e}")
            return False
    def step3_wait_device_authorization_and_get_tokens(self, timeout_seconds: int = None) -> bool:
        """Step 3: 设备授权流程轮询获取 access_token/refresh_token（不依赖 /auth/sso-token）"""
        logger.info("\n[Step 3] 设备授权轮询获取 Token...")

        try:
            if not self.client_id or not self.client_secret:
                logger.error("  缺少 client_id/client_secret")
                return False
            if not self.device_code:
                logger.error("  缺少 device_code")
                return False
            if not self.verification_uri:
                logger.error("  缺少 verification_uri")
                return False

            interval = int(self.device_interval or 2)
            expires_in = int(self.device_expires_in or 600)
            timeout = int(timeout_seconds or min(expires_in, 600))

            logger.info(f"  请在浏览器打开并完成授权: {self.verification_uri}")
            logger.info(f"  轮询超时: {timeout}s, 初始间隔: {interval}s")

            url = f"{self.OIDC_BASE}/token"
            headers = self._generate_auth_headers()
            payload = {
                "clientId": self.client_id,
                "clientSecret": self.client_secret,
                "deviceCode": self.device_code,
                "grantType": "urn:ietf:params:oauth:grant-type:device_code",
            }

            deadline = time.time() + timeout
            while time.time() < deadline:
                resp = self.session.post(url, headers=headers, json=payload, timeout=30)

                if resp.status_code == 200:
                    data = resp.json()
                    self.access_token = data.get("accessToken")
                    self.refresh_token = data.get("refreshToken")
                    self.expires_in = data.get("expiresIn")
                    self.profileArn = data.get("profileArn")

                    if self.access_token and self.refresh_token:
                        logger.info(f"  ? Access Token: {self.access_token[:30]}...")
                        logger.info(f"  ? Refresh Token: {self.refresh_token[:30]}...")
                        return True

                    logger.error("  响应未包含 accessToken/refreshToken")
                    logger.error(f"  响应: {json.dumps(data)[:500]}")
                    return False

                if resp.status_code == 400:
                    try:
                        error_data = resp.json()
                    except Exception:
                        logger.error(f"  失败: HTTP 400 - {(getattr(resp, 'text', '') or '')[:300]}")
                        return False

                    error = error_data.get('error', '')
                    if error == 'authorization_pending':
                        time.sleep(interval)
                        continue
                    if error == 'slow_down':
                        interval = min(interval + 5, 30)
                        time.sleep(interval)
                        continue

                    logger.error(f"  Token 获取失败: {error or error_data}")
                    return False

                logger.error(f"  Token 轮询失败: HTTP {resp.status_code} - {(getattr(resp, 'text', '') or '')[:300]}")
                return False

            logger.error("  超时：未能获取 Token（请确认已在浏览器完成授权）")
            return False

        except Exception as e:
            logger.error(f"  Step 3 失败: {e}")
            return False

    def _get_cookie_value(self, name: str) -> str:
        if not name:
            return None
        jar = getattr(self.session, "cookies", None)
        if jar is None:
            return None
        # requests/curl_cffi cookie jar should be iterable
        try:
            for c in jar:
                if getattr(c, "name", None) == name:
                    return getattr(c, "value", None)
        except Exception:
            pass
        # fallback for jars that support get()
        try:
            return jar.get(name)
        except Exception:
            return None

    def _find_portal_bearer_token(self) -> str:
        """Try to find a portal bearer token from cookies (e.g. x-amz-sso_authn)."""
        # If we already captured it from an API response, prefer that.
        if getattr(self, "portal_bearer", None):
            return self.portal_bearer
        candidates = ["x-amz-sso_authn", "x-amz-sso-authn", "x-amz-sso_authn_c", "x-amz-sso-authn-c"]
        for key in candidates:
            val = self._get_cookie_value(key)
            if val:
                return val
        # heuristic search
        jar = getattr(self.session, "cookies", None)
        if jar is None:
            return None
        try:
            for c in jar:
                n = (getattr(c, "name", "") or "").lower()
                if "sso" in n and "authn" in n:
                    v = getattr(c, "value", None)
                    if v:
                        return v
        except Exception:
            pass
        return None

    def step3a_establish_portal_bearer_cookie(self) -> bool:
        """Step 3a: 尝试通过协议方式建立 portal 会话并拿到 x-amz-sso_authn cookie。

        目标：为 Step 3b 提供 bearer（无需打开浏览器）。
        """
        logger.info("\n[Step 3a] 尝试建立 portal bearer (x-amz-sso_authn)...")

        try:
            user_agent = (self.browser_fingerprint or {}).get("user_agent") or (
                "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 "
                "(KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36"
            )
            accept_language = (self.browser_fingerprint or {}).get("accept_language") or "en-US,en;q=0.9"

            # 先看看 portal 目录页是否可访问（JS 页面会在这里检查/设置 x-amz-sso_authn）
            portal_page_url = f"{self.SSO_PORTAL}/directory/{self.directory_id}/"
            try:
                resp = self.session.get(
                    portal_page_url,
                    headers={
                        "Accept": "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
                        "Accept-Language": accept_language,
                        "Cache-Control": "no-cache",
                        "Pragma": "no-cache",
                        "Sec-Fetch-Dest": "document",
                        "Sec-Fetch-Mode": "navigate",
                        "Sec-Fetch-Site": "none",
                        "Upgrade-Insecure-Requests": "1",
                        "User-Agent": user_agent,
                    },
                    timeout=30,
                    allow_redirects=True,
                )
                logger.info(f"  portal page: HTTP {resp.status_code}")
                final_url = str(getattr(resp, "url", "") or "")
                if final_url:
                    logger.info(f"  portal final_url: {final_url[:180]}...")
                    portal_page_url = final_url
            except Exception as e:
                logger.warning(f"  portal page 访问失败（忽略继续）：{e}")

            # 如果已经有 bearer，直接返回
            existing = self._find_portal_bearer_token()
            if existing:
                logger.info("  ✓ 已存在 portal bearer cookie")
                return True

            # 调用 /login 获取 redirectUrl + csrfToken，然后按 portal 的 JS 逻辑设置 loginCsrfToken cookie 并跳转
            login_url = f"{self.SSO_PORTAL}/login"
            login_params_candidates = [
                {"directory_id": self.directory_id, "redirect_url": portal_page_url},
                {"directory_id": "view", "redirect_url": portal_page_url},
            ]

            login_data = None
            for params in login_params_candidates:
                try:
                    r = self.session.get(
                        login_url,
                        params=params,
                        headers={
                            "Accept": "application/json, text/plain, */*",
                            "Accept-Language": accept_language,
                            "Origin": "https://view.awsapps.com",
                            "Referer": "https://view.awsapps.com/",
                            "User-Agent": user_agent,
                        },
                        timeout=30,
                    )
                    logger.info(f"  portal /login({params['directory_id']}): HTTP {r.status_code}")
                    if r.status_code == 200:
                        login_data = r.json()
                        break
                except Exception as e:
                    logger.warning(f"  portal /login({params['directory_id']}) 失败（忽略）：{e}")

            if not login_data:
                logger.warning("  portal /login 未获取到有效响应")
                return False

            redirect_url = login_data.get("redirectUrl")
            csrf_token = login_data.get("csrfToken")
            if csrf_token is not None:
                logger.info(f"  portal csrfToken: {csrf_token}")
                self.portal_login_csrf_token = csrf_token

            # 模拟 portal JS: 设置 loginCsrfToken cookie（domain 为 portal host，path 取 /directory/<id>/）
            try:
                portal_host = urlparse(portal_page_url).hostname or "portal.sso.us-east-1.amazonaws.com"
                portal_path = urlparse(portal_page_url).path or "/"
                cookie_path = "/"
                if portal_path.startswith("/directory/"):
                    parts = portal_path.split("/")
                    if len(parts) >= 3 and parts[2]:
                        cookie_path = f"/directory/{parts[2]}/"
                if csrf_token is not None:
                    self.session.cookies.set("loginCsrfToken", str(csrf_token), domain=portal_host, path=cookie_path)
                    logger.info(f"  ✓ set loginCsrfToken cookie (domain={portal_host}, path={cookie_path})")
            except Exception as e:
                logger.warning(f"  设置 loginCsrfToken cookie 失败（忽略继续）：{e}")

            if not redirect_url:
                logger.warning("  portal /login 未返回 redirectUrl")
                return False

            logger.info(f"  portal redirectUrl: {str(redirect_url)[:180]}...")

            # 纯协议补齐浏览器在 signin 页面执行的 /api/execute 流程
            # redirectUrl 形如：https://us-east-1.signin.aws/platform/<dir>/login?workflowStateHandle=...
            try:
                portal_workflow_handle = None
                try:
                    portal_workflow_handle = parse_qs(urlparse(str(redirect_url)).query).get("workflowStateHandle", [None])[0]
                except Exception:
                    portal_workflow_handle = None

                if portal_workflow_handle:
                    logger.info(f"  portal workflowStateHandle: {portal_workflow_handle[:50]}...")
                    self._complete_signin_login_workflow(
                        workflow_state_handle=portal_workflow_handle,
                        user_agent=user_agent,
                        accept_language=accept_language,
                    )
                else:
                    nav = self.session.get(
                        redirect_url,
                        headers={
                            "Accept": "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
                            "Accept-Language": accept_language,
                            "Cache-Control": "no-cache",
                            "Pragma": "no-cache",
                            "Upgrade-Insecure-Requests": "1",
                            "User-Agent": user_agent,
                        },
                        timeout=30,
                        allow_redirects=True,
                    )
                    nav_final = str(getattr(nav, "url", "") or "")
                    logger.info(f"  portal nav: HTTP {nav.status_code}")
                    if nav_final:
                        logger.info(f"  portal nav final_url: {nav_final[:180]}...")
            except Exception as e:
                logger.warning(f"  完成 signin workflow 失败（忽略继续）：{e}")

            bearer = self._find_portal_bearer_token()
            if bearer:
                logger.info("  ✓ 已获取 portal bearer cookie")
                return True

            logger.warning("  仍未获取到 x-amz-sso_authn（可能需要执行 JS 或该账号无 portal 权限）")
            return False

        except Exception as e:
            logger.error(f"  Step 3a 失败: {e}")
            return False

    def _complete_signin_login_workflow(
        self,
        workflow_state_handle: str,
        user_agent: str,
        accept_language: str,
        *,
        email: str | None = None,
        workflow_result_handle: str | None = None,
        state: str | None = None,
    ) -> bool:
        """纯协议完成 signin workflow（不执行 JS）。

        两种常见场景：
        - registration 完成后的 redirect：需要 workflowResultHandle + state 才能继续推进并拿到 view state(QVlB...)
        - portal /login 发起的 redirect：仅 workflowStateHandle 即可
        """
        if not workflow_state_handle:
            return False

        try:
            login_url = f"{self.SIGNIN_BASE}/platform/{self.directory_id}/login"
            login_params = {"workflowStateHandle": workflow_state_handle}
            if workflow_result_handle and state:
                login_params["workflowResultHandle"] = workflow_result_handle
                login_params["state"] = state

            referer = f"{login_url}?workflowStateHandle={workflow_state_handle}"
            if workflow_result_handle and state:
                referer += f"&state={state}&workflowResultHandle={workflow_result_handle}"

            self.session.get(
                login_url,
                params=login_params,
                headers={
                    "Accept": "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
                    "Accept-Language": accept_language,
                    "Cache-Control": "no-cache",
                    "Pragma": "no-cache",
                    "Upgrade-Insecure-Requests": "1",
                    "User-Agent": user_agent,
                },
                timeout=30,
                allow_redirects=True,
            )

            exec_url = f"{self.SIGNIN_BASE}/platform/{self.directory_id}/api/execute"
            current_handle = workflow_state_handle

            def _post_execute(step_id: str) -> dict:
                nonlocal current_handle
                request_id = str(uuid.uuid4())
                fingerprint = self._generate_fingerprint(referrer=f"{self.SIGNIN_BASE}/")

                inputs = [{"input_type": "FingerPrintRequestInput", "fingerPrint": fingerprint}]
                if email:
                    inputs.insert(0, {"input_type": "UserRequestInput", "username": email})

                payload: dict = {
                    "stepId": step_id,
                    "workflowStateHandle": current_handle,
                    "inputs": inputs,
                    "requestId": request_id,
                }

                # registration-complete 场景：缺少这俩经常直接 400（你现在遇到的就是这种）
                if workflow_result_handle and state:
                    payload["workflowResultHandle"] = workflow_result_handle
                    payload["state"] = state
                    if getattr(self, "visitor_id", None):
                        payload["visitorId"] = self.visitor_id

                headers = {
                    "Content-Type": "application/json; charset=UTF-8",
                    "Accept": "application/json, text/plain, */*",
                    "Accept-Language": accept_language,
                    "Cache-Control": "no-cache",
                    "Pragma": "no-cache",
                    "Origin": self.SIGNIN_BASE,
                    "Referer": referer,
                    "Sec-Fetch-Dest": "empty",
                    "Sec-Fetch-Mode": "cors",
                    "Sec-Fetch-Site": "same-origin",
                    "User-Agent": user_agent,
                    "x-amzn-requestid": request_id,
                }
                resp = self.session.post(exec_url, json=payload, headers=headers, timeout=30)
                step_display = step_id or '""'
                logger.info(f"  signin /api/execute({step_display}): HTTP {resp.status_code}")
                if resp.status_code != 200:
                    logger.info(f"  signin /api/execute body: {(getattr(resp, 'text', '') or '')[:300]}")
                    return None
                data = resp.json()
                if isinstance(data, dict) and data.get("workflowStateHandle"):
                    current_handle = data["workflowStateHandle"]
                return data

            data1 = _post_execute("")
            if not data1:
                return False

            # registration-complete 场景里，stepId="" 往往直接返回 view.awsapps.com/start/?state=QVlB...
            redirect1 = None
            if isinstance(data1, dict):
                redirect1 = (data1.get("redirect") or {}).get("url") or data1.get("redirectUrl")
            if redirect1:
                try:
                    full_redirect1 = urllib.parse.urljoin(self.SIGNIN_BASE + "/", redirect1)
                except Exception:
                    full_redirect1 = redirect1
                try:
                    parsed_redirect = urlparse(full_redirect1)
                    if parsed_redirect.query:
                        parsed_params = {}
                        for part in (parsed_redirect.query or "").split("&"):
                            if not part:
                                continue
                            key, _, value = part.partition("=")
                            key = urllib.parse.unquote(key)
                            value = urllib.parse.unquote(value)
                            parsed_params.setdefault(key, []).append(value)
                        state_val = (parsed_params.get("state") or [None])[0]
                        if isinstance(state_val, str) and state_val.startswith("QVlB") and len(state_val) > 50:
                            self.redirect_state = state_val
                            logger.info(f"  ✓ updated view state from signin redirectUrl(step=\"\"): {self.redirect_state[:50]}...")
                        wrh_val = (parsed_params.get("workflowResultHandle") or [None])[0]
                        if isinstance(wrh_val, str) and len(wrh_val) >= 32:
                            self.auth_code = wrh_val
                            logger.info(f"  ✓ updated authCode from signin redirectUrl(step=\"\"): {self.auth_code[:50]}...")
                        wdc_val = (parsed_params.get("wdc_csrf_token") or [None])[0]
                        if isinstance(wdc_val, str) and len(wdc_val) >= 16:
                            self.wdc_csrf_token = wdc_val
                            logger.info(f"  ✓ updated wdc_csrf_token from signin redirectUrl(step=\"\"): {self.wdc_csrf_token[:30]}...")
                except Exception:
                    pass
                if isinstance(getattr(self, "redirect_state", None), str) and self.redirect_state.startswith("QVlB"):
                    return True

            data2 = _post_execute("start")
            if not data2:
                return False

            redirect_url = None
            if isinstance(data2, dict):
                redirect_url = (data2.get("redirect") or {}).get("url") or data2.get("redirectUrl")

            if redirect_url:
                try:
                    full_redirect = urllib.parse.urljoin(self.SIGNIN_BASE + "/", redirect_url)
                except Exception:
                    full_redirect = redirect_url

                # Some flows redirect to `/start/` and drop query params; extract from redirect URL first.
                try:
                    parsed_redirect = urlparse(full_redirect)
                    if parsed_redirect.query:
                        parsed_params = {}
                        for part in (parsed_redirect.query or "").split("&"):
                            if not part:
                                continue
                            key, _, value = part.partition("=")
                            key = urllib.parse.unquote(key)
                            value = urllib.parse.unquote(value)
                            parsed_params.setdefault(key, []).append(value)
                        state_val = (parsed_params.get("state") or [None])[0]
                        if isinstance(state_val, str) and state_val.startswith("QVlB") and len(state_val) > 50:
                            self.redirect_state = state_val
                            logger.info(f"  ✓ updated view state from signin redirectUrl: {self.redirect_state[:50]}...")
                        wrh_val = (parsed_params.get("workflowResultHandle") or [None])[0]
                        if isinstance(wrh_val, str) and len(wrh_val) >= 32:
                            self.auth_code = wrh_val
                            logger.info(f"  ✓ updated authCode from signin redirectUrl: {self.auth_code[:50]}...")
                        wdc_val = (parsed_params.get("wdc_csrf_token") or [None])[0]
                        if isinstance(wdc_val, str) and len(wdc_val) >= 16:
                            self.wdc_csrf_token = wdc_val
                            logger.info(f"  ✓ updated wdc_csrf_token from signin redirectUrl: {self.wdc_csrf_token[:30]}...")
                except Exception:
                    pass

                nav = self.session.get(
                    full_redirect,
                    headers={
                        "Accept": "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
                        "Accept-Language": accept_language,
                        "Cache-Control": "no-cache",
                        "Pragma": "no-cache",
                        "Upgrade-Insecure-Requests": "1",
                        "User-Agent": user_agent,
                    },
                    timeout=30,
                    allow_redirects=True,
                )
                nav_final = str(getattr(nav, "url", "") or "")
                logger.info(f"  signin redirect nav: HTTP {nav.status_code}")
                if nav_final:
                    logger.info(f"  signin redirect final_url: {nav_final[:180]}...")
                    try:
                        parsed = urlparse(nav_final)
                        if parsed.query:
                            # preserve '+' in state
                            parsed_params = {}
                            for part in (parsed.query or "").split("&"):
                                if not part:
                                    continue
                                key, _, value = part.partition("=")
                                key = urllib.parse.unquote(key)
                                value = urllib.parse.unquote(value)
                                parsed_params.setdefault(key, []).append(value)
                            state_val = (parsed_params.get("state") or [None])[0]
                            if isinstance(state_val, str) and state_val.startswith("QVlB") and len(state_val) > 50:
                                self.redirect_state = state_val
                                logger.info(f"  ✓ updated view state from signin redirect: {self.redirect_state[:50]}...")
                            wrh_val = (parsed_params.get("workflowResultHandle") or [None])[0]
                            if isinstance(wrh_val, str) and len(wrh_val) >= 32:
                                self.auth_code = wrh_val
                                logger.info(f"  ✓ updated authCode from signin redirect: {self.auth_code[:50]}...")
                            wdc_val = (parsed_params.get("wdc_csrf_token") or [None])[0]
                            if isinstance(wdc_val, str) and len(wdc_val) >= 16:
                                self.wdc_csrf_token = wdc_val
                                logger.info(f"  ✓ updated wdc_csrf_token from signin redirect: {self.wdc_csrf_token[:30]}...")
                    except Exception:
                        pass

            return True

        except Exception:
            return False

    def step3b_silent_device_authorization_via_portal_bearer(self) -> bool:
        """Step 3b: 纯协议方式完成设备授权并获取 Token（不打开浏览器）。

        依赖条件：当前会话里已有可用的 portal bearer（通常来自 cookie: x-amz-sso_authn）。
        流程参考：`kiro-account-manager` 的 `import_from_sso_token`.
        """
        logger.info("\n[Step 3b] 纯协议完成设备授权 (portal bearer)...")

        try:
            if not self.client_id or not self.client_secret:
                logger.error("  缺少 client_id/client_secret")
                return False
            if not self.device_code or not self.user_code:
                logger.error("  缺少 device_code/user_code")
                return False

            bearer = self._find_portal_bearer_token()
            if not bearer:
                logger.warning("  未找到 portal bearer（cookie x-amz-sso_authn），无法纯协议完成设备授权")
                return False

            # Step A: whoAmI (验证 bearer)
            who_url = f"{self.SSO_PORTAL}/token/whoAmI"
            who = self.session.get(
                who_url,
                headers={
                    "Accept": "application/json",
                    "Authorization": f"Bearer {bearer}",
                    "Origin": "https://view.awsapps.com",
                    "Referer": "https://view.awsapps.com/",
                },
                timeout=30,
            )
            logger.info(f"  whoAmI: HTTP {who.status_code}")
            if who.status_code != 200:
                logger.info(f"  whoAmI body: {(getattr(who, 'text', '') or '')[:300]}")
                return False

            # Step B: /session/device -> device_session_token
            sess_url = f"{self.SSO_PORTAL}/session/device"
            sess = self.session.post(
                sess_url,
                headers={
                    "Accept": "application/json",
                    "Content-Type": "application/json;charset=UTF-8",
                    "Authorization": f"Bearer {bearer}",
                    "Origin": "https://view.awsapps.com",
                    "Referer": "https://view.awsapps.com/",
                },
                json={},
                timeout=30,
            )
            logger.info(f"  session/device: HTTP {sess.status_code}")
            if sess.status_code != 200:
                logger.info(f"  session/device body: {(getattr(sess, 'text', '') or '')[:300]}")
                return False

            device_session_token = None
            try:
                device_session_token = sess.json().get("token")
            except Exception:
                device_session_token = None
            if not device_session_token:
                logger.error("  session/device 未返回 token")
                return False

            # Step C: accept_user_code -> deviceContext
            accept_url = f"{self.OIDC_BASE}/device_authorization/accept_user_code"
            accept = self.session.post(
                accept_url,
                headers={
                    "Accept": "application/json, text/plain, */*",
                    "Content-Type": "application/json;charset=UTF-8",
                    "Origin": "https://view.awsapps.com",
                    "Referer": "https://view.awsapps.com/",
                },
                json={"userCode": self.user_code, "userSessionId": device_session_token},
                timeout=30,
            )
            logger.info(f"  accept_user_code: HTTP {accept.status_code}")
            if accept.status_code != 200:
                logger.info(f"  accept_user_code body: {(getattr(accept, 'text', '') or '')[:300]}")
                return False

            device_context = None
            try:
                device_context = accept.json().get("deviceContext")
            except Exception:
                device_context = None
            device_context_id = (device_context or {}).get("deviceContextId")
            if not device_context_id:
                logger.error("  accept_user_code 未返回 deviceContextId")
                return False

            # Step D: associate_token (approve)
            assoc_url = f"{self.OIDC_BASE}/device_authorization/associate_token"
            assoc_payload = {
                "deviceContext": {
                    "deviceContextId": device_context_id,
                    "clientId": (device_context or {}).get("clientId") or self.client_id,
                    "clientType": (device_context or {}).get("clientType") or "public",
                },
                "userSessionId": device_session_token,
            }
            assoc = self.session.post(
                assoc_url,
                headers={
                    "Accept": "application/json, text/plain, */*",
                    "Content-Type": "application/json;charset=UTF-8",
                    "Origin": "https://view.awsapps.com",
                    "Referer": "https://view.awsapps.com/",
                },
                json=assoc_payload,
                timeout=30,
            )
            logger.info(f"  associate_token: HTTP {assoc.status_code}")
            if assoc.status_code != 200:
                logger.info(f"  associate_token body: {(getattr(assoc, 'text', '') or '')[:300]}")
                return False

            # Step E: poll /token
            # 复用 Step 3 的轮询逻辑（这里无需打开浏览器，因为已完成 accept_user_code + associate_token）
            return self.step3_wait_device_authorization_and_get_tokens(timeout_seconds=120)

        except Exception as e:
            logger.error(f"  Step 3b 失败: {e}")
            return False

    # ==================== Phase 3: Portal 登录流程 ====================

    def step4_init_portal_login(self) -> bool:
        """Step 4: 初始化 Portal Login"""
        logger.info("\n[Step 4] 初始化 Portal Login...")

        try:
            params = {
                "directory_id": "view",
                "redirect_url": self.verification_uri
            }

            resp = self.session.get(
                f"{self.SSO_PORTAL}/login",
                params=params,
                timeout=30
            )

            if resp.status_code != 200:
                logger.error(f"  失败: HTTP {resp.status_code}")
                return False

            data = resp.json()
            redirect_url = data.get('redirectUrl')
            
            # 保存 csrfToken，用于后续 /auth/sso-token 请求
            self.login_csrf_token = data.get('csrfToken')
            if self.login_csrf_token:
                logger.info(f"  ✓ 获取 csrfToken: {self.login_csrf_token}")
                # 设置 loginCsrfToken cookie
                self.session.cookies.set('loginCsrfToken', self.login_csrf_token, domain='view.awsapps.com', path='/start/')

            if not redirect_url:
                logger.error("  未获取到重定向URL")
                return False

            logger.info(f"  ✓ 获取重定向URL: {redirect_url[:100]}...")

            # 访问重定向URL
            resp = self.session.get(redirect_url, timeout=30)
            final_url = str(resp.url) if hasattr(resp, 'url') else redirect_url

            # 提取 workflowStateHandle
            parsed = urlparse(final_url)
            params = parse_qs(parsed.query)
            self.workflow_state_handle = params.get('workflowStateHandle', [None])[0]

            # 提取 directoryId
            path_parts = parsed.path.split('/')
            for i, part in enumerate(path_parts):
                if part == 'platform' and i + 1 < len(path_parts):
                    self.directory_id = path_parts[i + 1]
                    break

            if not self.workflow_state_handle:
                logger.error("  未找到 workflowStateHandle")
                return False

            logger.info(f"  ✓ Directory ID: {self.directory_id}")
            logger.info(f"  ✓ Workflow Handle: {self.workflow_state_handle[:30]}...")
            return True

        except Exception as e:
            logger.error(f"  Step 4 失败: {e}")
            return False

    def step5_visit_signin_page(self) -> bool:
        """Step 5: 访问 Signin 页面，获取 Cookies"""
        logger.info("\n[Step 5] 访问 Signin 页面...")

        try:
            url = f"{self.SIGNIN_BASE}/platform/{self.directory_id}/login?workflowStateHandle={self.workflow_state_handle}"

            self.session.headers.update({
                'Referer': 'https://app.kiro.dev/'
            })

            resp = self.session.get(url, timeout=30)

            if resp.status_code != 200:
                logger.error(f"  失败: HTTP {resp.status_code}")
                return False

            # 获取 awsd2c-token
            logger.info("  获取 awsd2c-token...")
            token_resp = self.session.post(
                "https://vs.aws.amazon.com/token",
                headers={
                    'Content-Type': 'application/json;charset=UTF-8',
                    'Origin': self.SIGNIN_BASE,
                    'Referer': f'{self.SIGNIN_BASE}/'
                },
                json={},
                timeout=30
            )

            if token_resp.status_code == 200:
                # 复制 token 到 awsd2c-token-c
                if 'awsd2c-token' in self.session.cookies:
                    token_value = self.session.cookies['awsd2c-token']
                    self.session.cookies.set('awsd2c-token-c', token_value)
                    logger.info("  ✓ awsd2c-token 已设置")

            logger.info("  ✓ Signin 页面访问成功")
            return True

        except Exception as e:
            logger.error(f"  Step 5 失败: {e}")
            return False


    def step6_7_workflow_init(self) -> bool:
        """Step 6-7: Workflow 初始化（两次 POST）"""
        logger.info("\n[Step 6-7] Workflow 初始化...")

        try:
            url = f"{self.SIGNIN_BASE}/platform/{self.directory_id}/api/execute"
            fingerprint = self._generate_fingerprint()

            # 第一次 POST (stepId="")
            logger.info("  发送初始化请求 (stepId='')...")
            payload1 = {
                "stepId": "",
                "workflowStateHandle": self.workflow_state_handle,
                "inputs": [{
                    "input_type": "FingerPrintRequestInput",
                    "fingerPrint": fingerprint
                }],
                "requestId": str(uuid.uuid4())
            }

            resp = self.session.post(url, json=payload1, timeout=30)
            if resp.status_code == 200:
                data = resp.json()
                if 'workflowStateHandle' in data:
                    self.workflow_state_handle = data['workflowStateHandle']
                logger.info("  ✓ 第一次初始化完成")

            # 第二次 POST (stepId="start")
            logger.info("  发送 start 请求...")
            payload2 = {
                "stepId": "start",
                "workflowStateHandle": self.workflow_state_handle,
                "inputs": [{
                    "input_type": "FingerPrintRequestInput",
                    "fingerPrint": self._generate_fingerprint()
                }],
                "requestId": str(uuid.uuid4())
            }

            resp = self.session.post(url, json=payload2, timeout=30)
            if resp.status_code == 200:
                data = resp.json()
                if 'workflowStateHandle' in data:
                    self.workflow_state_handle = data['workflowStateHandle']
                logger.info("  ✓ 第二次初始化完成")

            return True

        except Exception as e:
            logger.error(f"  Step 6-7 失败: {e}")
            return False

    # ==================== Phase 4: 用户 ====================

    def step8_submit_email(self, email: str) -> bool:
        """Step 8: 提交邮箱 (SUBMIT)"""
        logger.info(f"\n[Step 8] 提交邮箱: {email}")

        try:
            # 设置 awsccc Cookie
            awsccc_data = {
                "essential": True,
                "performance": True,
                "functional": True,
                "advertising": True
            }
            awsccc_b64 = base64.b64encode(json.dumps(awsccc_data).encode()).decode()
            self.session.cookies.set('awsccc', awsccc_b64, domain='.signin.aws')

            url = f"{self.SIGNIN_BASE}/platform/{self.directory_id}/api/execute"
            fingerprint = self._generate_fingerprint()

            payload = {
                "stepId": "get-identity-user",
                "workflowStateHandle": self.workflow_state_handle,
                "actionId": "SUBMIT",
                "inputs": [
                    {"input_type": "UserRequestInput", "username": email},
                    {"input_type": "ApplicationTypeRequestInput", "applicationType": "SSO_INDIVIDUAL_ID"},
                    {
                        "input_type": "UserEventRequestInput",
                        "directoryId": self.directory_id,
                        "userName": email,
                        "userEvents": [{
                            "input_type": "UserEvent",
                            "eventType": "PAGE_SUBMIT",
                            "pageName": "IDENTIFICATION",
                            "timeSpentOnPage": random.randint(5000, 15000)
                        }]
                    },
                    {"input_type": "FingerPrintRequestInput", "fingerPrint": fingerprint}
                ],
                "visitorId": self.visitor_id,
                "requestId": str(uuid.uuid4())
            }

            resp = self.session.post(url, json=payload, timeout=30)

            if resp.status_code == 400:
                # 用户不存在，继续
                logger.info("  用户不存在，进入流程...")
                return self.step9_signup(email)
            elif resp.status_code == 200:
                logger.error("  用户已存在")
                return False
            else:
                logger.error(f"  提交失败: HTTP {resp.status_code}")
                return False

        except Exception as e:
            logger.error(f"  Step 8 失败: {e}")
            return False

    def step9_signup(self, email: str) -> bool:
        """Step 9: 进入 (SIGNUP)"""
        logger.info("\n[Step 9] 进入流程...")

        try:
            url = f"{self.SIGNIN_BASE}/platform/{self.directory_id}/api/execute"

            payload = {
                "stepId": "get-identity-user",
                "workflowStateHandle": self.workflow_state_handle,
                "actionId": "SIGNUP",
                "inputs": [
                    {"input_type": "UserRequestInput", "username": email},
                    {"input_type": "FingerPrintRequestInput", "fingerPrint": self._generate_fingerprint()}
                ],
                "visitorId": self.visitor_id,
                "requestId": str(uuid.uuid4())
            }

            resp = self.session.post(url, json=payload, timeout=30)

            if resp.status_code != 200:
                logger.error(f"  失败: HTTP {resp.status_code}")
                return False

            data = resp.json()

            if 'redirect' in data:
                redirect_url = data['redirect']['url']
                logger.info(f"  ✓ 重定向到页面")

                # 提取 workflowStateHandle
                parsed = urlparse(redirect_url)
                params = parse_qs(parsed.query)
                if 'workflowStateHandle' in params:
                    self.workflow_state_handle = params['workflowStateHandle'][0]

                # 访问页面
                self.session.get(redirect_url, timeout=30)
                return self.step10_signup_init(email)

            return False

        except Exception as e:
            logger.error(f"  Step 9 失败: {e}")
            return False

    def step10_signup_init(self, email: str) -> bool:
        """Step 10-10.5: Signup 页面初始化"""
        logger.info("\n[Step 10] Signup 页面初始化...")

        try:
            url = f"{self.SIGNIN_BASE}/platform/{self.directory_id}/signup/api/execute"

            # 第一次 POST (stepId="")
            payload1 = {
                "stepId": "",
                "workflowStateHandle": self.workflow_state_handle,
                "inputs": [{"input_type": "FingerPrintRequestInput", "fingerPrint": self._generate_fingerprint()}],
                "requestId": str(uuid.uuid4())
            }

            resp = self.session.post(url, json=payload1, timeout=30)
            if resp.status_code == 200:
                data = resp.json()
                if 'workflowStateHandle' in data:
                    self.workflow_state_handle = data['workflowStateHandle']

            # 第二次 POST (stepId="start") - 获取 workflowId
            payload2 = {
                "stepId": "start",
                "workflowStateHandle": self.workflow_state_handle,
                "inputs": [
                    {"input_type": "UserRequestInput", "username": email},
                    {"input_type": "FingerPrintRequestInput", "fingerPrint": self._generate_fingerprint()}
                ],
                "visitorId": self.visitor_id,
                "requestId": str(uuid.uuid4())
            }

            resp = self.session.post(url, json=payload2, timeout=30)

            if resp.status_code != 200:
                logger.error(f"  失败: HTTP {resp.status_code}")
                return False

            data = resp.json()

            if 'redirect' in data:
                redirect_url = data['redirect']['url']
                parsed = urlparse(redirect_url)

                # 从 query 或 fragment 提取 workflowID
                params = parse_qs(parsed.query)
                if 'workflowID' in params:
                    self.workflow_id = params['workflowID'][0]
                else:
                    fragment = parsed.fragment
                    if '?' in fragment:
                        fragment_params = parse_qs(fragment.split('?')[1])
                        if 'workflowID' in fragment_params:
                            self.workflow_id = fragment_params['workflowID'][0]

                logger.info(f"  ✓ workflowID: {self.workflow_id}")
                return True

            return False

        except Exception as e:
            logger.error(f"  Step 10 失败: {e}")
            return False

    # ==================== Phase 5: Profile 创建与验证 ====================

    def step10_5_visit_profile_page(self) -> bool:
        """Step 10.5: 访问 Profile 页面（设置必要的 cookies）"""
        logger.info("\n[Step 10.5] 访问 Profile 页面...")

        try:
            url = f"{self.PROFILE_BASE}/?workflowID={self.workflow_id}"

            # 设置请求头，模拟真实浏览器导航
            self.session.headers.update({
                'Accept': 'text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7',
                'Cache-Control': 'no-cache',
                'Pragma': 'no-cache',
                'Priority': 'u=0, i',
                'Sec-Fetch-Dest': 'document',
                'Sec-Fetch-Mode': 'navigate',
                'Sec-Fetch-Site': 'cross-site',
                'Upgrade-Insecure-Requests': '1',
                'Referer': f'{self.SIGNIN_BASE}/'
            })

            resp = self.session.get(url, timeout=30)

            if resp.status_code != 200:
                logger.error(f"  失败: HTTP {resp.status_code}")
                return False

            # 获取 vs.aws.amazon.com/token
            logger.info("  获取 vs token...")
            token_resp = self.session.post(
                "https://vs.aws.amazon.com/token",
                headers={
                    'Accept': '*/*',
                    'Content-Type': 'application/json',
                    'Origin': self.PROFILE_BASE,
                    'Referer': f'{self.PROFILE_BASE}/',
                    'Cache-Control': 'no-cache',
                    'Pragma': 'no-cache',
                    'Priority': 'u=1, i',
                    'Sec-Fetch-Dest': 'empty',
                    'Sec-Fetch-Mode': 'cors',
                    'Sec-Fetch-Site': 'same-site'
                },
                json={},
                timeout=30
            )

            if token_resp.status_code == 200:
                logger.info("  ✓ vs token 已获取")

            logger.info("  ✓ Profile 页面访问成功")
            return True

        except Exception as e:
            logger.error(f"  Step 10.5 失败: {e}")
            return False

    def step11_profile_start(self) -> bool:
        """Step 11: Profile /api/start"""
        logger.info("\n[Step 11] 启动 Profile 工作流...")

        try:
            url = f"{self.PROFILE_BASE}/api/start"
            fingerprint = self._generate_fingerprint(
                workflow_id=self.workflow_id,
                page_hash="#/signup/start",
                use_profile_template=True,
                referrer=f"{self.PROFILE_BASE}/?workflowID={self.workflow_id}"
            )

            event_timestamp = time.strftime("%Y-%m-%dT%H:%M:%S.000Z", time.gmtime())

            payload = {
                "workflowID": self.workflow_id,
                "browserData": {
                    "attributes": {
                        "fingerprint": fingerprint,
                        "eventTimestamp": event_timestamp,
                        "timeSpentOnPage": str(random.randint(3000, 6000)),
                        "eventType": "PageLoad",
                        "ubid": self.ubid,
                        "visitorId": self.visitor_id
                    },
                    "cookies": {}
                }
            }

            # 设置必要的请求头
            self.session.headers.update({
                'Accept': '*/*',
                'Content-Type': 'application/json;charset=UTF-8',
                'Origin': self.PROFILE_BASE,
                'Referer': f'{self.PROFILE_BASE}/?workflowID={self.workflow_id}',
                'Cache-Control': 'no-cache',
                'Pragma': 'no-cache',
                'Priority': 'u=1, i',
                'Sec-Fetch-Dest': 'empty',
                'Sec-Fetch-Mode': 'cors',
                'Sec-Fetch-Site': 'same-origin'
            })

            resp = self.session.post(url, json=payload, timeout=30)

            if resp.status_code != 200:
                logger.error(f"  失败: HTTP {resp.status_code}")
                return False

            data = resp.json()
            if 'workflowState' in data:
                self.workflow_state = data['workflowState']
                logger.info(f"  ✓ workflowState: {self.workflow_state[:30]}...")
                return True

            return False

        except Exception as e:
            logger.error(f"  Step 11 失败: {e}")
            return False

    def step12_send_otp(self, email: str) -> bool:
        """Step 12: 发送验证码"""
        logger.info(f"\n[Step 12] 发送验证码到: {email}")

        try:
            url = f"{self.PROFILE_BASE}/api/send-otp"

            # 生成 fingerprint，使用正确的 referrer（包含 workflowID）
            fingerprint = self._generate_fingerprint(
                workflow_id=self.workflow_id,
                page_hash="#/signup/enter-email",
                include_form=True,
                email_value=email,
                referrer=f"{self.PROFILE_BASE}/?workflowID={self.workflow_id}"
            )

            event_timestamp = time.strftime("%Y-%m-%dT%H:%M:%S.000Z", time.gmtime())

            payload = {
                "workflowState": self.workflow_state,
                "email": email,
                "browserData": {
                    "attributes": {
                        "fingerprint": fingerprint,
                        "eventTimestamp": event_timestamp,
                        "timeSpentOnPage": str(random.randint(4000, 8000)),
                        "pageName": "EMAIL_COLLECTION",
                        "eventType": "PageSubmit",
                        "ubid": self.ubid,
                        "visitorId": self.visitor_id
                    },
                    "cookies": {}
                }
            }

            self.session.headers.update({
                'Accept': '*/*',
                'Content-Type': 'application/json;charset=UTF-8',
                'Origin': self.PROFILE_BASE,
                'Referer': f'{self.PROFILE_BASE}/?workflowID={self.workflow_id}',
                'Cache-Control': 'no-cache',
                'Pragma': 'no-cache',
                'Priority': 'u=1, i',
                'Sec-Fetch-Dest': 'empty',
                'Sec-Fetch-Mode': 'cors',
                'Sec-Fetch-Site': 'same-origin'
            })

            resp = self.session.post(url, json=payload, timeout=30)

            if resp.status_code != 200:
                logger.error(f"  失败: HTTP {resp.status_code}")
                logger.error(f"  响应: {resp.text[:500]}")
                return False

            logger.info("  ✓ 验证码已发送")
            return True

        except Exception as e:
            logger.error(f"  Step 12 失败: {e}")
            return False

    def step14_create_identity(self, email: str, username: str, otp_code: str) -> bool:
        """Step 14: 创建身份"""
        logger.info(f"\n[Step 14] 创建身份: {username}")

        try:
            url = f"{self.PROFILE_BASE}/api/create-identity"
            fingerprint = self._generate_fingerprint(
                workflow_id=self.workflow_id,
                page_hash="#/signup/verify-otp",
                use_profile_template=True
            )

            event_timestamp = time.strftime("%Y-%m-%dT%H:%M:%S.000Z", time.gmtime())

            payload = {
                "workflowState": self.workflow_state,
                "userData": {
                    "email": email,
                    "fullName": username
                },
                "otpCode": otp_code,
                "browserData": {
                    "attributes": {
                        "fingerprint": fingerprint,
                        "eventTimestamp": event_timestamp,
                        "timeSpentOnPage": str(random.randint(4000, 8000)),
                        "pageName": "EMAIL_VERIFICATION",
                        "eventType": "EmailVerification",
                        "ubid": self.ubid,
                        "visitorId": self.visitor_id
                    },
                    "cookies": {}
                }
            }

            self.session.headers.update({
                'Referer': f'{self.PROFILE_BASE}/?workflowID={self.workflow_id}'
            })

            resp = self.session.post(url, json=payload, timeout=30)

            if resp.status_code != 200:
                logger.error(f"  失败: HTTP {resp.status_code}")
                logger.error(f"  响应: {resp.text[:500]}")
                return False

            data = resp.json()

            if 'registrationCode' in data:
                self.registration_code = data['registrationCode']
                logger.info(f"  ✓ registrationCode: {self.registration_code}")

            if 'signInState' in data:
                self.sign_in_state = data['signInState']
                logger.info(f"  ✓ signInState: {self.sign_in_state[:30]}...")

            return True

        except Exception as e:
            logger.error(f"  Step 14 失败: {e}")
            return False


    # ==================== Phase 6: 密码设置 ====================

    def step15_init_password_page(self) -> bool:
        """Step 15: 初始化密码设置页面"""
        logger.info("\n[Step 15] 初始化密码设置页面...")

        try:
            url = f"{self.SIGNIN_BASE}/platform/{self.directory_id}/signup/api/execute"
            fingerprint = self._generate_fingerprint(
                referrer=f"{self.SIGNIN_BASE}/"
            )

            payload = {
                "stepId": "",
                "state": self.sign_in_state,
                "inputs": [
                    {
                        "input_type": "UserRegistrationRequestInput",
                        "registrationCode": self.registration_code,
                        "state": self.sign_in_state
                    },
                    {"input_type": "FingerPrintRequestInput", "fingerPrint": fingerprint}
                ]
            }

            self.session.headers.update({
                'Content-Type': 'application/json;charset=UTF-8',
                'Referer': f'{self.SIGNIN_BASE}/platform/{self.directory_id}/signup?registrationCode={self.registration_code}&state={self.sign_in_state}'
            })

            resp = self.session.post(url, json=payload, timeout=30)

            if resp.status_code != 200:
                logger.error(f"  失败: HTTP {resp.status_code}")
                logger.error(f"  响应: {resp.text[:500]}")
                return False

            data = resp.json()

            # 更新 workflowStateHandle
            if 'workflowStateHandle' in data:
                self.workflow_state_handle = data['workflowStateHandle']
                logger.info(f"  ✓ workflowStateHandle 已更新")

            # 递归搜索公钥信息
            def find_key(d, key):
                if isinstance(d, dict):
                    if key in d:
                        return d[key]
                    for v in d.values():
                        result = find_key(v, key)
                        if result is not None:
                            return result
                elif isinstance(d, list):
                    for item in d:
                        result = find_key(item, key)
                        if result is not None:
                            return result
                return None

            # 提取公钥信息（用于密码加密）
            public_key = find_key(data, 'publicKey')
            if public_key:
                self.public_key_jwk = public_key
                logger.info(f"  ✓ 获取到公钥")
            else:
                logger.warning("  ⚠ 未获取到公钥")

            issuer = find_key(data, 'issuer')
            if issuer:
                self.issuer = issuer
                logger.info(f"  ✓ issuer: {self.issuer}")
            else:
                logger.warning("  ⚠ 未获取到 issuer")

            audience = find_key(data, 'audience')
            if audience:
                self.audience = audience
                logger.info(f"  ✓ audience: {self.audience}")
            else:
                logger.warning("  ⚠ 未获取到 audience")

            region = find_key(data, 'region')
            if region:
                self.region = region
                logger.info(f"  ✓ region: {self.region}")
            else:
                logger.warning("  ⚠ 未获取到 region")

            return True

        except Exception as e:
            logger.error(f"  Step 15 失败: {e}")
            return False

    def step16_set_password(self, email: str, password: str) -> bool:
        """Step 16: 设置密码

        发送密码后，需要等待响应返回 stepId: "end-of-user-registration-success"
        然后更新 workflowStateHandle，为 Step 17 做准备
        """
        logger.info("\n[Step 16] 设置密码...")

        try:
            # 检查必要的参数
            if not self.public_key_jwk:
                logger.error("  缺少公钥 (publicKey)")
                return False
            if not self.issuer:
                logger.error("  缺少 issuer")
                return False
            if not self.audience:
                logger.error("  缺少 audience")
                return False
            if not self.workflow_state_handle:
                logger.error("  缺少 workflowStateHandle")
                return False

            url = f"{self.SIGNIN_BASE}/platform/{self.directory_id}/signup/api/execute"
            fingerprint = self._generate_fingerprint(
                referrer=f"{self.SIGNIN_BASE}/"
            )

            # 加密密码
            logger.info("  加密密码...")
            logger.info(f"    明文密码: {password}")
            logger.info(f"    issuer: {self.issuer}")
            logger.info(f"    audience: {self.audience}")
            logger.info(f"    publicKey: {json.dumps(self.public_key_jwk)[:100]}...")

            encrypted_password = encrypt_password(
                password,
                self.public_key_jwk,
                self.issuer,
                self.audience
            )
            logger.info(f"  ✓ 密码加密完成: {encrypted_password[:50]}...")

            # 发送 metrics fingerprint
            self._send_metrics_fingerprint(fingerprint)

            payload = {
                "stepId": "get-new-password-for-password-creation",
                "workflowStateHandle": self.workflow_state_handle,
                "actionId": "SUBMIT",
                "inputs": [
                    {
                        "input_type": "PasswordRequestInput",
                        "password": encrypted_password,
                        "successfullyEncrypted": "SUCCESSFUL",
                        "errorLog": None
                    },
                    {
                        "input_type": "UserEventRequestInput",
                        "directoryId": self.directory_id,
                        "userName": email,
                        "userEvents": [{
                            "input_type": "UserEvent",
                            "eventType": "PAGE_SUBMIT",
                            "pageName": "CREDENTIAL_COLLECTION",
                            "timeSpentOnPage": random.randint(10000, 20000)
                        }]
                    },
                    {"input_type": "UserRequestInput", "username": email},
                    {"input_type": "FingerPrintRequestInput", "fingerPrint": fingerprint}
                ],
                "visitorId": self.visitor_id
            }

            logger.info(f"  发送请求到: {url}")
            resp = self.session.post(url, json=payload, timeout=30)

            if resp.status_code != 200:
                logger.error(f"  失败: HTTP {resp.status_code}")
                logger.error(f"  响应: {resp.text[:500]}")
                return False

            data = resp.json()

            # 检查响应中的 stepId
            step_id = data.get('stepId', '')
            logger.info(f"  响应 stepId: {step_id}")

            # 更新 workflowStateHandle（无论什么 stepId 都需要更新）
            if 'workflowStateHandle' in data:
                self.workflow_state_handle = data['workflowStateHandle']
                logger.info(f"  ✓ workflowStateHandle 已更新: {self.workflow_state_handle[:50]}...")

            # 如果响应是 end-of-user-registration-success，直接从 redirect 中提取 authCode
            if step_id == 'end-of-user-registration-success' and 'redirect' in data:
                redirect_url = data['redirect']['url']
                self.password_redirect_url = redirect_url
                logger.info(f"  ✓ 成功！重定向URL: {redirect_url}")

                parsed = urlparse(redirect_url)
                params = parse_qs(parsed.query)

                if 'workflowResultHandle' in params:
                    self.auth_code = params['workflowResultHandle'][0]
                    logger.info(f"  ✓ authCode (workflowResultHandle): {self.auth_code[:50]}...")

                if 'workflowStateHandle' in params:
                    self.login_workflow_state_handle = params['workflowStateHandle'][0]
                    logger.info(f"  ✓ login_workflowStateHandle: {self.login_workflow_state_handle[:50]}...")

                # 提取 state 参数（用于 Step 18 的 /auth/sso-token 请求）
                if 'state' in params:
                    signin_state = params['state'][0]
                    self.redirect_state = signin_state
                    logger.info(f"  ✓ redirect_state: {self.redirect_state[:50]}...")

                # /auth/sso-token 需要 view.awsapps.com/start/?state=QVlB... 的长 state（不是这里的短 state）
                # 尝试跟随重定向获取正确的 state
                def _parse_query_preserve_plus(query: str) -> dict:
                    # 不能用 parse_qs：它会把 '+' 当作空格，导致 state 失效
                    parsed_params = {}
                    for part in (query or "").split("&"):
                        if not part:
                            continue
                        key, _, value = part.partition("=")
                        key = urllib.parse.unquote(key)
                        value = urllib.parse.unquote(value)
                        parsed_params.setdefault(key, []).append(value)
                    return parsed_params

                view_state = None
                view_workflow_result_handle = None
                view_wdc_csrf_token = None

                try:
                    follow_resp = self.session.get(redirect_url, timeout=30, allow_redirects=True)
                    final_url = str(getattr(follow_resp, "url", "") or redirect_url)
                    logger.info(f"  final_url: {final_url}")

                    candidate_urls = [final_url]

                    for history_resp in (getattr(follow_resp, "history", None) or []):
                        try:
                            location = history_resp.headers.get("Location")
                        except Exception:
                            location = None
                        if location:
                            base_url = str(getattr(history_resp, "url", "") or "")
                            candidate_urls.append(urllib.parse.urljoin(base_url, location) if base_url else location)

                    body = getattr(follow_resp, "text", "") or ""
                    for match in re.finditer(r"https://view\.awsapps\.com/start/\?[^\"\s<>]+", body):
                        candidate_urls.append(match.group(0))

                    for candidate in candidate_urls:
                        parsed_candidate = urlparse(candidate)
                        if parsed_candidate.netloc.endswith("view.awsapps.com") and parsed_candidate.path.startswith("/start/"):
                            qs = _parse_query_preserve_plus(parsed_candidate.query)
                            view_state = (qs.get("state") or [None])[0]
                            view_workflow_result_handle = (qs.get("workflowResultHandle") or [None])[0]
                            view_wdc_csrf_token = (qs.get("wdc_csrf_token") or [None])[0]
                            if view_state:
                                break
                except Exception as e:
                    logger.warning(f"  view state extract failed: {e}")

                if view_state:
                    self.redirect_state = view_state
                    logger.info(f"  ✓ redirect_state (from view.awsapps.com): {self.redirect_state[:50]}...")
                    if view_workflow_result_handle:
                        self.auth_code = view_workflow_result_handle
                    if view_wdc_csrf_token:
                        self.wdc_csrf_token = view_wdc_csrf_token
                else:
                    self.redirect_state = signin_state
                    logger.warning("  view state not found; /auth/sso-token may return 400")

            logger.info("  ✓ 密码设置成功")
            return True

        except Exception as e:
            logger.error(f"  Step 16 失败: {e}")
            import traceback
            logger.error(traceback.format_exc())
            return False

    def step16b_test_kiro_account_usage(self) -> bool:
        """Step 16b: 测试是否能访问 app.kiro.dev/account/usage（诊断用）"""
        logger.info("\n[Step 16b] 测试 Kiro 账户页面 (app.kiro.dev/account/usage)...")

        try:
            user_agent = (self.browser_fingerprint or {}).get("user_agent") or (
                "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 "
                "(KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36"
            )
            accept_language = (self.browser_fingerprint or {}).get("accept_language") or "en-US,en;q=0.9"

            # 这里有意只跟随 app.kiro.dev 域内的重定向：
            # - 用于“进入一次账户页”验证注册后的登录态
            # - 避免跟随到 signin.aws/awsapps.com 影响后续 token/SSO 流程
            initial_url = "https://app.kiro.dev/account/usage"
            current_url = initial_url
            final_url = initial_url
            resp = None

            for _hop in range(0, 8):
                resp = self.session.get(
                    current_url,
                    headers={
                        "Accept": "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8",
                        "Accept-Language": accept_language,
                        "Cache-Control": "no-cache",
                        "Pragma": "no-cache",
                        "Referer": current_url if current_url else "https://app.kiro.dev/",
                        "Sec-Fetch-Dest": "document",
                        "Sec-Fetch-Mode": "navigate",
                        "Sec-Fetch-Site": "same-origin",
                        "Sec-Fetch-User": "?1",
                        "Upgrade-Insecure-Requests": "1",
                        "User-Agent": user_agent,
                    },
                    timeout=30,
                    allow_redirects=False,
                )

                final_url = str(getattr(resp, "url", "") or "") or current_url
                if resp.status_code not in (301, 302, 303, 307, 308):
                    break

                location = ""
                try:
                    location = resp.headers.get("Location") or ""
                except Exception:
                    location = ""

                if not location:
                    break

                next_url = urllib.parse.urljoin(current_url, location)
                parsed_next = urlparse(next_url)
                if parsed_next.netloc and parsed_next.netloc != "app.kiro.dev":
                    logger.warning(f"  重定向离开 app.kiro.dev，停止跟随: {parsed_next.netloc}")
                    final_url = next_url
                    break

                current_url = next_url

            if resp is None:
                logger.warning("  未获得响应")
                return False

            logger.info(f"  响应: HTTP {resp.status_code}")
            if final_url:
                logger.info(f"  final_url: {final_url[:180]}...")

            body = getattr(resp, "text", "") or ""
            m = re.search(r"<title[^>]*>(.*?)</title>", body, re.IGNORECASE | re.DOTALL)
            if m:
                title = re.sub(r"\\s+", " ", m.group(1)).strip()[:120]
                if title:
                    logger.info(f"  title: {title}")

            parsed = urlparse(final_url) if final_url else None
            if parsed and parsed.netloc == "app.kiro.dev" and parsed.path.startswith("/account/usage") and resp.status_code == 200:
                logger.info("  ✓ 已进入 app.kiro.dev/account/usage（至少服务器侧返回 200）")
                return True

            if "signin.aws" in final_url or "awsapps.com" in final_url:
                logger.warning("  看起来被重定向到 AWS 登录/SSO 页面（app.kiro.dev 侧未建立登录态）")
            elif resp.status_code in (401, 403):
                logger.warning("  app.kiro.dev 返回 401/403（可能需要浏览器 JS / Cloudflare / 额外登录步骤）")
            else:
                logger.warning("  未确认进入 account/usage（请看 final_url/title 判断）")

            return False

        except Exception as e:
            logger.error(f"  Step 16b 失败: {e}")
            return False

    def _try_fetch_portal_bearer_via_auth_sso_token(self, user_agent: str, accept_language: str) -> bool:
        """Try to fetch a bearer-like session token via /auth/sso-token.

        Some flows use this token as the value stored in the x-amz-sso_authn cookie.
        """
        try:
            if not self.auth_code:
                return False
            if not self.redirect_state:
                return False

            url = f"{self.SSO_PORTAL}/auth/sso-token"

            # Per captured browser traffic + notes: x-amz-sso-csrf-token should match loginCsrfToken (portal /login csrfToken).
            csrf_token = None
            try:
                csrf_token = self.session.cookies.get("loginCsrfToken")
            except Exception:
                csrf_token = None
            if csrf_token is None:
                csrf_token = getattr(self, "portal_login_csrf_token", None)

            if csrf_token is None:
                try:
                    login_url = f"{self.SSO_PORTAL}/login"
                    login_headers = {
                        "Accept": "application/json, text/plain, */*",
                        "Accept-Language": accept_language,
                        "Origin": "https://view.awsapps.com",
                        "Referer": "https://view.awsapps.com/",
                        "User-Agent": user_agent,
                    }
                    for redirect_url in ("https://view.awsapps.com/", "https://view.awsapps.com/start/"):
                        r = self.session.get(
                            login_url,
                            params={"directory_id": "view", "redirect_url": redirect_url},
                            headers=login_headers,
                            timeout=30,
                        )
                        if r.status_code == 200:
                            data = r.json()
                            csrf_token = data.get("csrfToken")
                            if csrf_token is not None:
                                self.portal_login_csrf_token = csrf_token
                                break
                except Exception:
                    pass

            if csrf_token is None:
                csrf_token = random.randint(-999999999, -1)

            headers = {
                "Content-Type": "application/x-www-form-urlencoded",
                "Accept": "application/json, text/plain, */*",
                "Accept-Language": accept_language,
                "Cache-Control": "no-cache",
                "Pragma": "no-cache",
                "Origin": "https://view.awsapps.com",
                "Referer": "https://view.awsapps.com/",
                "Sec-Fetch-Dest": "empty",
                "Sec-Fetch-Mode": "cors",
                "Sec-Fetch-Site": "cross-site",
                "User-Agent": user_agent,
                "x-amz-sso-csrf-token": str(csrf_token),
            }
            form = {"authCode": self.auth_code, "state": self.redirect_state, "orgId": "view"}
            logger.info(f"  auth/sso-token authCode: {self.auth_code[:36]}...")
            logger.info(f"  auth/sso-token state: {self.redirect_state[:30]}...(len={len(self.redirect_state)})")
            resp = None
            for attempt in range(1, 6):
                resp = self.session.post(url, data=form, headers=headers, timeout=30)
                logger.info(f"  auth/sso-token: HTTP {resp.status_code} (attempt {attempt}/5)")
                if resp.status_code == 200:
                    break
                body = (getattr(resp, "text", "") or "")[:400]
                if body:
                    logger.info(f"  auth/sso-token body: {body}")
                # Invalid params won't recover; 401 may recover if backend session binding lags.
                if resp.status_code in (400, 403):
                    return False
                time.sleep(1 + attempt)

            if not resp or resp.status_code != 200:
                return False

            data = resp.json()
            token = data.get("token")
            if not token:
                return False

            self.portal_bearer = token
            logger.info(f"  ✓ portal bearer from /auth/sso-token: {self.portal_bearer[:40]}...")

            # Optional compatibility: also expose it as a cookie value (some tooling expects this name).
            try:
                self.session.cookies.set(
                    "x-amz-sso_authn",
                    token,
                    domain="portal.sso.us-east-1.amazonaws.com",
                    path="/",
                )
            except Exception:
                pass

            # Sanity check: bearer should authorize /token/whoAmI.
            try:
                who_url = f"{self.SSO_PORTAL}/token/whoAmI"
                who_headers = {
                    "Accept": "application/json, text/plain, */*",
                    "Accept-Language": accept_language,
                    "Authorization": f"Bearer {token}",
                    "Origin": "https://view.awsapps.com",
                    "Referer": "https://view.awsapps.com/",
                    "User-Agent": user_agent,
                }
                who = self.session.get(who_url, headers=who_headers, timeout=30)
                logger.info(f"  whoAmI: HTTP {who.status_code}")
                if who.status_code != 200:
                    logger.info(f"  whoAmI body: {(getattr(who, 'text', '') or '')[:300]}")
            except Exception as e:
                logger.info(f"  whoAmI exception: {e}")

            return True

        except Exception as e:
            logger.info(f"  auth/sso-token exception: {e}")
            return False

    def step17_establish_portal_bearer_from_view_start(self, email: str) -> bool:
        """Step 17: 纯协议获取 portal bearer（x-amz-sso_authn）。

        目标：模拟浏览器在 view.awsapps.com/start 登录过程中建立 portal 会话，
        最终在 cookies 中出现 x-amz-sso_authn。
        """
        logger.info("\n[Step 17] 建立 portal 会话并获取 x-amz-sso_authn...")

        try:
            user_agent = (self.browser_fingerprint or {}).get("user_agent") or (
                "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 "
                "(KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36"
            )
            accept_language = (self.browser_fingerprint or {}).get("accept_language") or "en-US,en;q=0.9"

            if not self.auth_code:
                logger.error("  缺少 auth_code（workflowResultHandle）")
                return False
            if not self.login_workflow_state_handle:
                logger.error("  缺少 login_workflow_state_handle")
                return False
            if not self.redirect_state:
                logger.error("  缺少 redirect_state")
                return False

            def _looks_like_view_state(state: str) -> bool:
                return isinstance(state, str) and state.startswith("QVlB") and len(state) > 50

            def _parse_query_preserve_plus(query: str) -> dict:
                parsed_params = {}
                for part in (query or "").split("&"):
                    if not part:
                        continue
                    key, _, value = part.partition("=")
                    key = urllib.parse.unquote(key)
                    value = urllib.parse.unquote(value)
                    parsed_params.setdefault(key, []).append(value)
                return parsed_params

            # Step 17a: 跑完 signin workflow 才能拿到可用于 /auth/sso-token 的 view state / authCode
            logger.info("  Step 17a: 完成 signin workflow 获取 view state/authCode...")
            self._complete_signin_login_workflow(
                workflow_state_handle=self.login_workflow_state_handle,
                user_agent=user_agent,
                accept_language=accept_language,
                email=email,
                workflow_result_handle=self.auth_code,
                state=self.redirect_state,
            )
            if not _looks_like_view_state(self.redirect_state):
                logger.error("  Step 17a: 未获得有效 view state（QVlB...），无法继续")
                return False

            # 访问 view.awsapps.com/start（浏览器里这里会自动推进登录）
            logger.info("  Step 17b: 访问 view.awsapps.com/start/ ...")
            start_url = (
                "https://view.awsapps.com/start/?"
                f"state={urllib.parse.quote(self.redirect_state)}"
                f"&workflowResultHandle={urllib.parse.quote(self.auth_code)}"
            )
            if self.wdc_csrf_token:
                start_url += f"&wdc_csrf_token={urllib.parse.quote(self.wdc_csrf_token)}"

            try:
                start_resp = self.session.get(
                    start_url,
                    headers={
                        "Accept": "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8",
                        "Accept-Language": accept_language,
                        "Cache-Control": "no-cache",
                        "Pragma": "no-cache",
                        "Referer": f"{self.SIGNIN_BASE}/",
                        "Sec-Fetch-Dest": "document",
                        "Sec-Fetch-Mode": "navigate",
                        "Sec-Fetch-Site": "cross-site",
                        "Sec-Fetch-User": "?1",
                        "Upgrade-Insecure-Requests": "1",
                        "User-Agent": user_agent,
                    },
                    timeout=30,
                    allow_redirects=True,
                )
                logger.info(f"    start: HTTP {start_resp.status_code}")
                final_url = str(getattr(start_resp, "url", "") or "")
                if final_url:
                    logger.info(f"    start final_url: {final_url[:180]}...")
                    try:
                        parsed = urlparse(final_url)
                        qs = _parse_query_preserve_plus(parsed.query)
                        state_val = (qs.get("state") or [None])[0]
                        if _looks_like_view_state(state_val):
                            self.redirect_state = state_val
                        wrh_val = (qs.get("workflowResultHandle") or [None])[0]
                        if isinstance(wrh_val, str) and len(wrh_val) >= 32:
                            self.auth_code = wrh_val
                        wdc_val = (qs.get("wdc_csrf_token") or [None])[0]
                        if isinstance(wdc_val, str) and len(wdc_val) >= 16:
                            self.wdc_csrf_token = wdc_val
                        logger.info(f"    ✓ state(for sso-token): {self.redirect_state[:50]}...")
                        logger.info(f"    ✓ authCode(for sso-token): {self.auth_code[:50]}...")
                    except Exception:
                        pass
            except Exception as e:
                logger.warning(f"    start 访问失败（忽略继续）：{e}")

            # 尝试直接通过 /auth/sso-token 获取 bearer（某些场景下它就是 x-amz-sso_authn 的值）
            logger.info("  Step 17b.5: 尝试 /auth/sso-token 获取 bearer...")
            if self._try_fetch_portal_bearer_via_auth_sso_token(user_agent=user_agent, accept_language=accept_language):
                return True

            logger.warning("  /auth/sso-token 未返回 bearer，无法纯协议拿到 x-amz-sso_authn")
            return False

        except Exception as e:
            logger.error(f"  Step 17 失败: {e}")
            return False

    def _send_metrics_fingerprint(self, fingerprint: str) -> bool:
        """发送 metrics fingerprint"""
        try:
            url = f"{self.SIGNIN_BASE}/metrics/fingerprint"
            data = (
                f"name=IsFingerprintGenerated:Success"
                f"&value={fingerprint}"
                f"&operation=AWSSignin:FingerprintMetrics:get-new-password-for-password-creation"
            )

            headers = {
                'Content-Type': 'application/x-www-form-urlencoded;charset=UTF-8',
                'Origin': self.SIGNIN_BASE,
                'Referer': f'{self.SIGNIN_BASE}/platform/{self.directory_id}/signup?registrationCode={self.registration_code}&state={self.sign_in_state}'
            }

            self.session.post(url, data=data, headers=headers, timeout=30)
            return True
        except:
            return False

    def register(self, email: str, username: str, password: str, email_id: str = None, proxies: dict = None, email_provider: str = 'provider1') -> bool:
        """执行完整的注册流程

        Args:
            email: 邮箱地址
            username: 用户名
            password: 密码
            email_id: 临时邮箱 ID（用于获取验证码）
            proxies: 代理配置（用于邮箱 API）
            email_provider: 邮箱服务提供商
        """
        logger.info("\n" + "=" * 60)
        logger.info("开始完整注册流程")
        logger.info("=" * 60)
        logger.info(f"邮箱: {email}")
        logger.info(f"用户名: {username}")

        # Phase 1: OIDC 初始化
        if not self.step1_register_oidc_client():
            return False
        if not self.step2_device_authorization():
            return False

        # Phase 3: Portal 登录流程
        if not self.step4_init_portal_login():
            return False
        if not self.step5_visit_signin_page():
            return False
        if not self.step6_7_workflow_init():
            return False

        # Phase 4: 用户注册
        if not self.step8_submit_email(email):
            return False

        # Phase 5: Profile 创建与验证
        # 先访问 Profile 页面（设置必要的 cookies）
        if not self.step10_5_visit_profile_page():
            return False

        if not self.step11_profile_start():
            return False

        # 获取验证码（支持重试发送OTP）
        otp_code = None
        # 发送OTP
        if not self.step12_send_otp(email):
            logger.error("发送OTP失败")
            return False

        if email_id:
            # 从临时邮箱 API 获取验证码
            logger.info("等待验证码邮件...")
            max_wait_time = 120  # 最多等待120秒
            start_time = time.time()

            while time.time() - start_time < max_wait_time:
                otp_code = get_code(email_id, proxies=proxies, provider=email_provider)
                if otp_code:
                    logger.info(f"✓ 获取到验证码: {otp_code}")
                    break
                time.sleep(5)  # 每5秒检查一次

            if not otp_code:
                logger.error("从临时邮箱获取验证码失败")
                return False
        else:
            # 手动输入验证码
            otp_code = input("\n请输入邮箱收到的验证码: ").strip()

        if not self.step14_create_identity(email, username, otp_code):
            return False

        # Phase 6: 密码设置
        if not self.step15_init_password_page():
            return False
        if not self.step16_set_password(email, password):
            return False

        # 可选诊断：注册完成后先访问一次账户页面（失败也不阻断流程）
        try:
            self.step16b_test_kiro_account_usage()
        except Exception:
            pass

        # Phase 7: 获取 portal bearer（x-amz-sso_authn）并用它完成设备授权换取 token
        if not self.step17_establish_portal_bearer_from_view_start(email):
            return False
        if not self.portal_bearer:
            logger.error("⚠️ 未获取 x-amz-sso_authn，无法继续换取 refresh token")
            return False

        if not self.step3b_silent_device_authorization_via_portal_bearer():
            logger.error("⚠️ 使用 x-amz-sso_authn 换取 token 失败")
            return False

        logger.info("\n" + "=" * 60)
        logger.info("✅ 注册完成：已获取 refresh/access token")
        if self.refresh_token:
            logger.info("✅ 已获取 refresh_token")
        else:
            logger.info("⚠️ 未获取 refresh_token")
        logger.info("=" * 60)
        return True

    def get_account_info(self, email: str = None, username: str = None, password: str = None) -> dict:
        """获取账号信息（字段与 kx-kiro5/amazonq_accounts.json 保持一致）。"""
        return {
            "email": email,
            "password": password,
            "username": username,
            "provider": "BuilderId",
            "createdAt": datetime.datetime.now().isoformat(),
            "clientId": self.client_id,
            "clientSecret": self.client_secret,
            "refreshToken": self.refresh_token,
            "accessToken": self.access_token,
            "expiresIn": self.expires_in,
            "x-amz-sso_authn": self.portal_bearer,
        }


# ==================== 工具函数 ====================

def gen_username():
    """生成真实姓名"""
    # 男性名字
    male_first_names = [
        'James', 'John', 'Robert', 'Michael', 'William', 'David', 'Richard', 'Joseph', 'Thomas', 'Charles',
        'Christopher', 'Daniel', 'Matthew', 'Anthony', 'Mark', 'Donald', 'Steven', 'Paul', 'Andrew', 'Joshua',
        'Kenneth', 'Kevin', 'Brian', 'George', 'Edward', 'Ronald', 'Timothy', 'Jason', 'Jeffrey', 'Ryan',
        'Jacob', 'Gary', 'Nicholas', 'Eric', 'Jonathan', 'Stephen', 'Larry', 'Justin', 'Scott', 'Brandon',
        'Benjamin', 'Samuel', 'Alexander', 'Patrick', 'Jack', 'Dennis', 'Jerry', 'Tyler', 'Aaron', 'Henry'
    ]

    # 女性名字
    female_first_names = [
        'Mary', 'Patricia', 'Jennifer', 'Linda', 'Barbara', 'Elizabeth', 'Susan', 'Jessica', 'Sarah', 'Karen',
        'Nancy', 'Lisa', 'Betty', 'Margaret', 'Sandra', 'Ashley', 'Dorothy', 'Kimberly', 'Emily', 'Donna',
        'Michelle', 'Carol', 'Amanda', 'Melissa', 'Deborah', 'Stephanie', 'Rebecca', 'Laura', 'Sharon', 'Cynthia',
        'Kathleen', 'Amy', 'Shirley', 'Angela', 'Helen', 'Anna', 'Brenda', 'Pamela', 'Nicole', 'Emma',
        'Samantha', 'Katherine', 'Christine', 'Debra', 'Rachel', 'Catherine', 'Carolyn', 'Janet', 'Ruth', 'Maria'
    ]

    last_names = [
        'Smith', 'Johnson', 'Williams', 'Brown', 'Jones', 'Garcia', 'Miller', 'Davis', 'Rodriguez', 'Martinez',
        'Hernandez', 'Lopez', 'Gonzalez', 'Wilson', 'Anderson', 'Thomas', 'Taylor', 'Moore', 'Jackson', 'Martin',
        'Lee', 'Perez', 'Thompson', 'White', 'Harris', 'Sanchez', 'Clark', 'Ramirez', 'Lewis', 'Robinson',
        'Walker', 'Young', 'Allen', 'King', 'Wright', 'Scott', 'Torres', 'Nguyen', 'Hill', 'Flores',
        'Green', 'Adams', 'Nelson', 'Baker', 'Hall', 'Rivera', 'Campbell', 'Mitchell', 'Carter', 'Roberts',
        'Gomez', 'Phillips', 'Evans', 'Turner', 'Diaz', 'Parker', 'Cruz', 'Edwards', 'Collins', 'Reyes',
        'Stewart', 'Morris', 'Morales', 'Murphy', 'Cook', 'Rogers', 'Gutierrez', 'Ortiz', 'Morgan', 'Cooper'
    ]

    # 随机选择性别
    is_male = random.choice([True, False])
    first_names = male_first_names if is_male else female_first_names

    # 随机选择组合方式
    format_choice = random.randint(1, 4)

    if format_choice == 1:
        # FirstName LastName (最常见，60%概率)
        return f"{random.choice(first_names)} {random.choice(last_names)}"
    elif format_choice == 2:
        # FirstName MiddleInitial LastName
        middle_initial = random.choice('ABCDEFGHIJKLMNOPQRSTUVWXYZ')
        return f"{random.choice(first_names)} {middle_initial} {random.choice(last_names)}"
    elif format_choice == 3:
        # FirstName MiddleName LastName (同性别)
        first = random.choice(first_names)
        middle = random.choice(first_names)
        return f"{first} {middle} {random.choice(last_names)}"
    else:
        # FirstName LastName (无空格版本)
        return f"{random.choice(first_names)}{random.choice(last_names)}"


def gen_password():
    """生成随机密码"""
    return random.choice('ABCDEFGHIJKLMNOPQRSTUVWXYZ') + \
           ''.join(random.choice('abcdefghijklmnopqrstuvwxyz') for _ in range(6)) + \
           ''.join(random.choice('0123456789') for _ in range(4)) + '..'


def save_account(account_info: dict, filename: str = "amazonq_accounts.json"):
    """保存账号信息到 JSON 文件（支持多进程安全）"""
    import fcntl  # Unix 文件锁

    def _normalize_account_record(record: dict) -> dict:
        if not isinstance(record, dict):
            return record

        email = record.get("email")
        password = record.get("password")
        username = record.get("username")
        provider = record.get("provider") or "BuilderId"
        created_at = record.get("createdAt") or record.get("created_at")

        client_id = record.get("clientId") or record.get("client_id")
        client_secret = record.get("clientSecret") or record.get("client_secret")
        refresh_token = record.get("refreshToken") or record.get("refresh_token")
        access_token = record.get("accessToken") or record.get("access_token")
        expires_in = record.get("expiresIn") or record.get("expires_in")
        profileArn = record.get("profileArn") or ''
        sso_authn = record.get("x-amz-sso_authn")

        normalized = {
            "email": email,
            "password": password,
            "username": username,
            "provider": provider,
            "createdAt": created_at or datetime.datetime.now().isoformat(),
            "clientId": client_id,
            "clientSecret": client_secret,
            "refreshToken": refresh_token,
            "accessToken": access_token,
            "expiresIn": expires_in,
            "x-amz-sso_authn": sso_authn,
            "profileArn": profileArn
        }
        return normalized

    # 使用文件锁确保多进程安全
    max_retries = 10
    for attempt in range(max_retries):
        try:
            # 以追加模式打开文件（如果不存在则创建）
            with open(filename, 'a+', encoding='utf-8') as f:
                # 获取独占锁
                fcntl.flock(f.fileno(), fcntl.LOCK_EX)

                try:
                    # 移动到文件开头读取现有内容
                    f.seek(0)
                    content = f.read()

                    if content.strip():
                        accounts = json.loads(content)
                    else:
                        accounts = []

                    if not isinstance(accounts, list):
                        accounts = []

                    # 标准化现有记录
                    accounts = [_normalize_account_record(a) for a in accounts]

                    # 添加新记录
                    accounts.append(_normalize_account_record(account_info))

                    # 清空文件并写入更新后的内容
                    f.seek(0)
                    f.truncate()
                    json.dump(accounts, f, indent=2, ensure_ascii=False)

                    logger.info(f"账号已保存到: {filename}")
                    return True

                finally:
                    # 释放锁
                    fcntl.flock(f.fileno(), fcntl.LOCK_UN)

        except (IOError, OSError) as e:
            if attempt < max_retries - 1:
                time.sleep(0.1 * (attempt + 1))  # 指数退避
                continue
            else:
                logger.error(f"保存账号失败: {e}")
                return False
        except json.JSONDecodeError:
            # 文件损坏，备份并重新创建
            if attempt == 0:
                backup_name = f"{filename}.backup.{int(time.time())}"
                try:
                    os.rename(filename, backup_name)
                    logger.warning(f"JSON 文件损坏，已备份到: {backup_name}")
                except:
                    pass
            continue

    return False


# ==================== 单次注册函数（供多进程调用）====================

def register_single_account(args_tuple):
    """单次注册函数（供多进程调用）

    Args:
        args_tuple: (worker_id, proxy, output_file, verbose, email_provider)

    Returns:
        dict: 注册结果
    """
    worker_id, proxy, output_file, verbose, email_provider = args_tuple

    try:
        # 创建临时邮箱（不使用代理）
        print(f"[Worker {worker_id}] 创建临时邮箱...")
        email, email_id, domain = get_email(proxies=None, provider=email_provider)

        # AWS API 调用时才使用代理
        proxies = None
        if proxy:
            proxies = {'http': proxy, 'https': proxy}

        if not email or not email_id:
            print(f"[Worker {worker_id}] ❌ 无法创建临时邮箱")
            return {
                'success': False,
                'worker_id': worker_id,
                'error': '无法创建临时邮箱'
            }

        print(f"[Worker {worker_id}] ✓ 临时邮箱: {email}")

        # 生成用户名和密码
        username = gen_username()
        password = gen_password()

        print(f"[Worker {worker_id}] 用户名: {username}")

        # 创建注册实例
        registration = KiroRegistration(proxy=proxy, verbose=verbose)

        # 执行注册
        success = registration.register(email, username, password, email_id=email_id, proxies=proxies, email_provider=email_provider)

        if success:
            print(f"[Worker {worker_id}] ✅ 注册成功")

            # 保存账号
            account_info = registration.get_account_info(email=email, username=username, password=password)
            save_account(account_info, filename=output_file)

            return {
                'success': True,
                'worker_id': worker_id,
                'email': email,
                'username': username,
                'password': password,
                'client_id': registration.client_id,
                'refresh_token': registration.refresh_token[:30] + '...' if registration.refresh_token else None
            }
        else:
            print(f"[Worker {worker_id}] ❌ 注册失败")
            return {
                'success': False,
                'worker_id': worker_id,
                'email': email,
                'error': '注册流程失败'
            }

    except Exception as e:
        print(f"[Worker {worker_id}] ❌ 异常: {e}")
        import traceback
        traceback.print_exc()
        return {
            'success': False,
            'worker_id': worker_id,
            'error': str(e)
        }


# ==================== 主函数 ====================

def main():
    """主函数 - 支持命令行参数"""
    parser = argparse.ArgumentParser(
        description='AWS Builder ID 批量注册脚本 - 临时邮箱模式',
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog='''
示例:
  # 创建 1 个账号（默认）
  python3 main_signup_temp_email.py

  # 创建 10 个账号
  python3 main_signup_temp_email.py -n 10

  # 使用 4 个进程并发创建 20 个账号
  python3 main_signup_temp_email.py -n 20 -w 4

  # 使用代理
  python3 main_signup_temp_email.py -n 5 -p http://user:pass@proxy.com:8080

  # 指定输出文件
  python3 main_signup_temp_email.py -n 10 -o my_accounts.json

  # 显示详细日志
  python3 main_signup_temp_email.py -n 5 -v

  # 使用 email_utils2 邮箱服务
  python3 main_signup_temp_email.py -n 5 --email-provider provider2

  # 使用自动fallback模式
  python3 main_signup_temp_email.py -n 5 --email-provider auto
        '''
    )

    parser.add_argument(
        '-n', '--count',
        type=int,
        default=1,
        help='要创建的账号数量（默认: 1）'
    )

    parser.add_argument(
        '-w', '--workers',
        type=int,
        default=1,
        help='并发进程数（默认: 1）'
    )

    parser.add_argument(
        '-p', '--proxy',
        type=str,
        default=None,
        help='代理地址（格式: http://user:pass@host:port 或从 .env 读取）'
    )

    parser.add_argument(
        '-o', '--output',
        type=str,
        default='amazonq_accounts.json',
        help='输出文件名（默认: amazonq_accounts.json）'
    )

    parser.add_argument(
        '-v', '--verbose',
        action='store_true',
        help='显示详细日志'
    )

    parser.add_argument(
        '--email-provider',
        type=str,
        default='provider1',
        choices=['provider1', 'provider2', 'auto'],
        help='邮箱服务提供商（默认: provider1=email_utils, provider2=email_utils2, auto=自动fallback）'
    )

    args = parser.parse_args()

    # 打印配置
    print("=" * 60)
    print("AWS Builder ID 批量注册脚本 - 临时邮箱模式")
    print("=" * 60)
    print(f"创建数量: {args.count}")
    print(f"并发进程: {args.workers}")
    print(f"输出文件: {args.output}")
    print(f"邮箱服务: {args.email_provider}")

    # 获取代理
    proxy = args.proxy
    if not proxy:
        proxy = get_proxy_from_env()

    if proxy:
        # 隐藏密码部分
        display_proxy = proxy
        if '@' in proxy:
            parts = proxy.split('@')
            if len(parts) == 2:
                display_proxy = f"***@{parts[1]}"
        print(f"使用代理: {display_proxy}")
    else:
        print("未使用代理")

    print("=" * 60)

    # 准备任务参数
    tasks = [
        (i + 1, proxy, args.output, args.verbose, args.email_provider)
        for i in range(args.count)
    ]

    # 记录开始时间
    start_time = time.time()

    # 执行注册
    if args.workers > 1:
        # 多进程模式
        print(f"\n使用 {args.workers} 个进程并发创建 {args.count} 个账号...\n")

        with multiprocessing.Pool(processes=args.workers) as pool:
            results = pool.map(register_single_account, tasks)
    else:
        # 单进程模式
        print(f"\n开始创建 {args.count} 个账号...\n")
        results = [register_single_account(task) for task in tasks]

    # 统计结果
    success_count = sum(1 for r in results if r['success'])
    failed_count = len(results) - success_count

    elapsed_time = time.time() - start_time

    # 打印结果
    print("\n" + "=" * 60)
    print("注册完成")
    print("=" * 60)
    print(f"总数: {args.count}")
    print(f"成功: {success_count}")
    print(f"失败: {failed_count}")
    print(f"耗时: {elapsed_time:.1f} 秒")
    print(f"平均: {elapsed_time / args.count:.1f} 秒/账号")
    print("=" * 60)

    # 打印成功的账号
    if success_count > 0:
        print("\n✅ 成功创建的账号:")
        for r in results:
            if r['success']:
                print(f"  [{r['worker_id']}] {r['email']} | {r['username']}")

    # 打印失败的账号
    if failed_count > 0:
        print("\n❌ 失败的任务:")
        for r in results:
            if not r['success']:
                error_msg = r.get('error', '未知错误')
                email = r.get('email', 'N/A')
                print(f"  [{r['worker_id']}] {email} - {error_msg}")

    print(f"\n账号已保存到: {args.output}")
    print("详细日志: registration.log")


if __name__ == '__main__':
    # 设置多进程启动方法（macOS 需要）
    multiprocessing.set_start_method('spawn', force=True)
    main()
