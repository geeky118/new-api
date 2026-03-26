"""
Configuration loading helpers.
"""

import json
import os


def _parse_bool(value):
    if isinstance(value, bool):
        return value
    if value is None:
        return None
    return str(value).strip().lower() in {"1", "true", "yes", "y", "on"}


def _parse_int(value):
    if value is None:
        return None
    return int(str(value).strip())


def _parse_list(value):
    if value is None:
        return None
    if isinstance(value, list):
        return value
    text = str(value).strip()
    if not text:
        return []
    if text.startswith("["):
        try:
            parsed = json.loads(text)
            if isinstance(parsed, list):
                return parsed
        except Exception:
            pass
    return [item.strip() for item in text.split(",") if item.strip()]


def _load_dotenv_file(path):
    if not os.path.exists(path):
        return

    try:
        with open(path, "r", encoding="utf-8") as f:
            for raw_line in f:
                line = raw_line.strip()
                if not line or line.startswith("#") or "=" not in line:
                    continue
                key, value = line.split("=", 1)
                key = key.strip()
                value = value.strip().strip("'").strip('"')
                if key and key not in os.environ:
                    os.environ[key] = value
    except Exception as e:
        print(f"[WARN] failed to load {path}: {e}")


def load_config():
    """Load config.json and then override it with environment variables."""
    config = {
        "total_accounts": 3,
        "concurrent_workers": 1,
        "skymail_admin_email": "",
        "skymail_admin_password": "",
        "skymail_api_base": "",
        "skymail_api_token": "",
        "skymail_domains": [],
        "proxy": "",
        "output_file": "registered_accounts.txt",
        "accounts_file": "accounts.txt",
        "csv_file": "registered_accounts.csv",
        "enable_oauth": True,
        "oauth_required": True,
        "oauth_issuer": "https://auth.openai.com",
        "oauth_client_id": "app_EMoamEEZ73f0CkXaXp7hrann",
        "oauth_redirect_uri": "http://localhost:1455/auth/callback",
        "ak_file": "ak.txt",
        "rk_file": "rk.txt",
        "token_json_dir": "tokens",
        "upload_api_url": "",
        "upload_api_token": "",
    }

    base_dir = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    dotenv_candidates = (
        os.path.join(base_dir, ".env"),
        os.path.join(os.path.dirname(base_dir), ".env"),
        os.path.join(base_dir, ".env.local"),
    )
    for dotenv_path in dotenv_candidates:
        _load_dotenv_file(dotenv_path)

    config_path = os.path.join(base_dir, "config.json")
    if os.path.exists(config_path):
        try:
            with open(config_path, "r", encoding="utf-8") as f:
                file_config = json.load(f)
                config.update(file_config)
        except Exception as e:
            print(f"[WARN] failed to load config.json: {e}")

    env_mappings = {
        "SKYMAIL_ADMIN_EMAIL": "skymail_admin_email",
        "SKYMAIL_ADMIN_PASSWORD": "skymail_admin_password",
        "SKYMAIL_API_BASE": "skymail_api_base",
        "SKYMAIL_API_TOKEN": "skymail_api_token",
        "SKYMAIL_DOMAINS": "skymail_domains",
        "PROXY": "proxy",
        "TOTAL_ACCOUNTS": "total_accounts",
        "CONCURRENT_WORKERS": "concurrent_workers",
        "ENABLE_OAUTH": "enable_oauth",
        "OAUTH_REQUIRED": "oauth_required",
        "OAUTH_ISSUER": "oauth_issuer",
        "OAUTH_CLIENT_ID": "oauth_client_id",
        "OAUTH_REDIRECT_URI": "oauth_redirect_uri",
        "AK_FILE": "ak_file",
        "RK_FILE": "rk_file",
        "TOKEN_JSON_DIR": "token_json_dir",
        "UPLOAD_API_URL": "upload_api_url",
        "UPLOAD_API_TOKEN": "upload_api_token",
    }

    for env_key, config_key in env_mappings.items():
        env_value = os.environ.get(env_key)
        if env_value is None:
            continue
        if config_key in {"total_accounts", "concurrent_workers"}:
            config[config_key] = _parse_int(env_value)
        elif config_key in {"enable_oauth", "oauth_required"}:
            config[config_key] = _parse_bool(env_value)
        elif config_key == "skymail_domains":
            config[config_key] = _parse_list(env_value)
        else:
            config[config_key] = env_value.strip()

    return config


def as_bool(value):
    parsed = _parse_bool(value)
    return bool(parsed)
