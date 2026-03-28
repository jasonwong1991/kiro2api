#!/usr/bin/env python3
"""Single account registration wrapper for Go registrar.

All logs go to stderr. On success, prints ONE JSON object to stdout.
Exit code 0 + valid JSON stdout = success.
Exit code 1 = registration failed (reason in stderr).
"""
import json
import sys
import os
import logging
import argparse
import datetime

# Redirect all logging to stderr so stdout is clean JSON only
logging.basicConfig(
    level=logging.INFO,
    format='%(asctime)s [%(levelname)s] %(message)s',
    handlers=[logging.StreamHandler(sys.stderr)]
)
logger = logging.getLogger(__name__)

# Add parent dir to path so we can import from the register package
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from fingerprint.fingerprint import create_session, generate_random_fingerprint
from fingerprint.aws_fingerprint import generate_fingerprint
from JWE import encrypt_password
from email_gen.generator import gen_email_prefix
from email_adapter import get_email, get_code
from main_signup_temp_email import KiroRegistration, gen_username, gen_password


def main():
    parser = argparse.ArgumentParser(description='Register one Kiro account')
    parser.add_argument('-p', '--proxy', default=None, help='Proxy URL')
    parser.add_argument('-e', '--email-provider', default='provider1', help='Email provider (provider1/provider2/auto)')
    args = parser.parse_args()

    proxy = args.proxy
    email_provider = args.email_provider

    # If no proxy from args, try .env
    if not proxy:
        from main_signup_temp_email import get_proxy_from_env
        proxy = get_proxy_from_env()

    try:
        # Create temp email
        proxies = None
        if proxy:
            proxies = {'http': proxy, 'https': proxy}

        logger.info("Creating temp email...")
        email, email_id, domain = get_email(proxies=proxies, provider=email_provider)

        if not email or not email_id:
            logger.error("Failed to create temp email")
            sys.exit(1)

        logger.info(f"Email: {email}")

        username = gen_username()
        password = gen_password()
        logger.info(f"Username: {username}")

        registration = KiroRegistration(proxy=proxy, verbose=False)
        success = registration.register(
            email, username, password,
            email_id=email_id, proxies=proxies,
            email_provider=email_provider
        )

        if not success:
            logger.error("Registration failed")
            sys.exit(1)

        # Build account info and output as JSON to stdout
        account = {
            "email": email,
            "password": password,
            "username": username,
            "clientId": registration.client_id,
            "clientSecret": registration.client_secret,
            "refreshToken": registration.refresh_token,
            "accessToken": registration.access_token,
            "profileArn": getattr(registration, 'profileArn', '') or '',
        }

        # stdout: clean JSON only
        print(json.dumps(account))
        logger.info(f"Success: {email}")

    except Exception as e:
        logger.error(f"Exception: {e}")
        sys.exit(1)


if __name__ == '__main__':
    main()
