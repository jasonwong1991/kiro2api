#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
JWE (JSON Web Encryption) 加密器

用于 AWS 密码加密，采用 RSA-OAEP-256 + A256GCM 算法

JWE Header 结构 (根据 AWS 规范):
{
  "alg": "RSA-OAEP-256",
  "kid": "<key-id>",
  "enc": "A256GCM",
  "cty": "enc",
  "typ": "application/aws+signin+jwe"
}

JWT Claims 结构:
{
  "iss": "<region>.<issuer>",
  "iat": <timestamp>,
  "nbf": <timestamp>,
  "jti": "<uuid>",
  "exp": <timestamp + 300>,
  "aud": "<region>.<audience>",
  "password": "<password>"
}

使用示例:
    from JWE import encrypt_password

    encrypted = encrypt_password(
        password="MyPassword123",
        public_key_jwk=server_public_key,
        issuer="signin.aws",
        audience="AWSPasswordService",
        region="us-east-1"
    )
"""

import json
import time
import os
import base64
import uuid as uuid_module


def base64url_encode(data: bytes) -> str:
    """Base64 URL 安全编码（无填充）"""
    return base64.urlsafe_b64encode(data).rstrip(b'=').decode('ascii')


def base64url_decode(data: str) -> bytes:
    """Base64 URL 安全解码"""
    padding = 4 - len(data) % 4
    if padding != 4:
        data += '=' * padding
    return base64.urlsafe_b64decode(data)


def bytes_to_int(b: bytes) -> int:
    """将字节转换为整数"""
    return int.from_bytes(b, byteorder='big')


class JWEEncryptor:
    """JWE 加密器类 - 符合 AWS signin 规范"""

    def __init__(self, public_key_jwk: dict = None):
        """
        初始化 JWE 加密器

        Args:
            public_key_jwk: RSA 公钥 (JWK 格式)，包含 kid, n, e, alg
        """
        self.public_key_jwk = public_key_jwk
        self._rsa_public_key = None
        self.kid = None

        if public_key_jwk:
            self._load_public_key(public_key_jwk)

    def _load_public_key(self, jwk: dict):
        """从 JWK 加载 RSA 公钥"""
        try:
            from cryptography.hazmat.primitives.asymmetric import rsa
            from cryptography.hazmat.backends import default_backend

            # 提取 key id
            self.kid = jwk.get('kid', '')

            # 解析 JWK 中的 n 和 e
            n = bytes_to_int(base64url_decode(jwk['n']))
            e = bytes_to_int(base64url_decode(jwk['e']))

            # 构建公钥
            self._rsa_public_key = rsa.RSAPublicNumbers(e, n).public_key(default_backend())

        except Exception as ex:
            raise ValueError(f"无法加载公钥: {ex}")

    def encrypt(
        self,
        password: str,
        issuer: str = None,
        audience: str = None,
        region: str = "us-east-1"
    ) -> str:
        """
        使用 JWE 加密密码 - 符合 AWS signin 规范

        Args:
            password: 要加密的密码
            issuer: JWT issuer (如 "signin.aws")
            audience: JWT audience (如 "AWSPasswordService")
            region: AWS 区域 (如 "us-east-1")

        Returns:
            JWE 加密后的字符串（紧凑序列化格式）
        """
        from cryptography.hazmat.primitives.asymmetric import padding
        from cryptography.hazmat.primitives import hashes
        from cryptography.hazmat.primitives.ciphers.aead import AESGCM

        # 1. 构建 JWT Claims (符合 AWS 规范)
        now = int(time.time())
        jwt_claims = {
            "iss": f"{region}.{issuer}" if issuer else f"{region}.signin.aws",
            "iat": now,
            "nbf": now,
            "jti": str(uuid_module.uuid4()),
            "exp": now + 300,  # 5分钟过期
            "aud": f"{region}.{audience}" if audience else f"{region}.AWSPasswordService",
            "password": password
        }

        # 2. 序列化并填充到 192 字节 (AWS 要求)
        claims_json = json.dumps(jwt_claims, separators=(',', ':'))
        padded_claims = claims_json.ljust(192, ' ')

        # 3. 生成随机 CEK (Content Encryption Key) - 256 bits for A256GCM
        cek = os.urandom(32)

        # 4. 生成随机 IV - 96 bits for GCM
        iv = os.urandom(12)

        # 5. JWE Header (符合 AWS 规范)
        jwe_header = {
            "alg": "RSA-OAEP-256",
            "kid": self.kid or "",
            "enc": "A256GCM",
            "cty": "enc",
            "typ": "application/aws+signin+jwe"
        }
        header_json = json.dumps(jwe_header, separators=(',', ':'))
        header_b64 = base64url_encode(header_json.encode('utf-8'))

        # 6. 使用 RSA-OAEP-256 加密 CEK
        if self._rsa_public_key:
            encrypted_key = self._rsa_public_key.encrypt(
                cek,
                padding.OAEP(
                    mgf=padding.MGF1(algorithm=hashes.SHA256()),
                    algorithm=hashes.SHA256(),
                    label=None
                )
            )
        else:
            # 如果没有公钥，生成占位符（仅用于测试）
            encrypted_key = os.urandom(256)

        encrypted_key_b64 = base64url_encode(encrypted_key)

        # 7. IV
        iv_b64 = base64url_encode(iv)

        # 8. 使用 A256GCM 加密 payload
        # AAD = ASCII(BASE64URL(JWE Header))
        aad = header_b64.encode('ascii')

        aesgcm = AESGCM(cek)
        ciphertext_and_tag = aesgcm.encrypt(iv, padded_claims.encode('utf-8'), aad)

        # GCM 输出 = ciphertext + tag (tag 是最后 16 字节)
        ciphertext = ciphertext_and_tag[:-16]
        tag = ciphertext_and_tag[-16:]

        ciphertext_b64 = base64url_encode(ciphertext)
        tag_b64 = base64url_encode(tag)

        # 9. 组装 JWE (紧凑序列化格式)
        # 格式: header.encrypted_key.iv.ciphertext.tag
        jwe = f"{header_b64}.{encrypted_key_b64}.{iv_b64}.{ciphertext_b64}.{tag_b64}"

        return jwe


def encrypt_password(
    password: str,
    public_key_jwk: dict = None,
    issuer: str = None,
    audience: str = None,
    region: str = "us-east-1"
) -> str:
    """
    使用 JWE 加密密码（便捷函数）- 符合 AWS signin 规范

    Args:
        password: 明文密码
        public_key_jwk: 服务器返回的公钥 (JWK 格式)，包含 kid, n, e, alg
        issuer: JWT issuer (如 "signin.aws")
        audience: JWT audience (如 "AWSPasswordService")
        region: AWS 区域 (如 "us-east-1")

    Returns:
        JWE 加密后的字符串
    """
    encryptor = JWEEncryptor(public_key_jwk)
    return encryptor.encrypt(password, issuer, audience, region)


if __name__ == "__main__":
    # 测试
    print("JWE 加密器测试 - AWS signin 规范")
    print("=" * 60)

    test_password = "TestPassword123.."

    try:
        encrypted = encrypt_password(test_password)
        print(f"原始密码: {test_password}")
        print(f"加密结果: {encrypted[:80]}...")
        print(f"加密长度: {len(encrypted)}")

        # 解析 JWE 结构
        parts = encrypted.split('.')
        print(f"\nJWE 结构:")
        print(f"  Header: {parts[0]}")

        # 解码 header 查看内容
        header_json = base64url_decode(parts[0]).decode('utf-8')
        print(f"  Header 解码: {header_json}")

        print(f"  Encrypted Key 长度: {len(parts[1])}")
        print(f"  IV: {parts[2]}")
        print(f"  Ciphertext 长度: {len(parts[3])}")
        print(f"  Tag: {parts[4]}")

    except Exception as e:
        print(f"测试失败: {e}")
        import traceback
        traceback.print_exc()

