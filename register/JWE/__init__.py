#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
JWE (JSON Web Encryption) 模块

用于 AWS 密码加密，采用 RSA-OAEP-256 + A256GCM 算法
"""

from .encryptor import encrypt_password, JWEEncryptor

__all__ = ['encrypt_password', 'JWEEncryptor']

