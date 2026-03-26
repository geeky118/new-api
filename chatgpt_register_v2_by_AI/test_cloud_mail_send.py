"""
Cloud Mail send/receive smoke test.

Usage:
  python test_cloud_mail_send.py
  python test_cloud_mail_send.py --to someone@example.com --subject "test"
"""

import argparse
import json
import random
import string
import sys
import time
from typing import Any, Dict, List, Optional

import requests
import urllib3

from lib.config import load_config

urllib3.disable_warnings(urllib3.exceptions.InsecureRequestWarning)


def _base_url(config: Dict[str, Any]) -> str:
    configured = config.get("skymail_api_base")
    if configured:
        return str(configured).rstrip("/")
    admin_email = str(config.get("skymail_admin_email", "")).strip()
    if "@" not in admin_email:
        raise ValueError("config 中缺少 skymail_admin_email 或格式不正确")
    return f"https://{admin_email.split('@', 1)[1]}"


def _build_session(config: Dict[str, Any]) -> requests.Session:
    session = requests.Session()
    proxy = str(config.get("proxy", "") or "").strip()
    if proxy:
        session.proxies = {"http": proxy, "https": proxy}
    return session


def _login(session: requests.Session, base: str, admin_email: str, admin_password: str) -> str:
    resp = session.post(
        f"{base}/api/login",
        json={"email": admin_email, "password": admin_password},
        timeout=20,
        verify=False,
    )
    data = resp.json()
    if resp.status_code != 200 or data.get("code") != 200:
        raise RuntimeError(f"登录失败: status={resp.status_code}, body={resp.text[:300]}")
    token = (data.get("data") or {}).get("token")
    if not token:
        raise RuntimeError(f"登录成功但未返回 token: {resp.text[:300]}")
    return token


def _get_accounts(session: requests.Session, base: str, token: str) -> List[Dict[str, Any]]:
    resp = session.get(
        f"{base}/api/account/list",
        headers={"Authorization": token},
        timeout=20,
        verify=False,
    )
    data = resp.json()
    if resp.status_code != 200 or data.get("code") != 200:
        raise RuntimeError(f"读取账号列表失败: status={resp.status_code}, body={resp.text[:300]}")
    return data.get("data") or []


def _pick_account_id(accounts: List[Dict[str, Any]], admin_email: str) -> int:
    for item in accounts:
        if str(item.get("email", "")).lower() == admin_email.lower():
            return int(item.get("accountId"))
    if accounts:
        return int(accounts[0].get("accountId"))
    raise RuntimeError("账号列表为空，无法发件")


def _send_email(
    session: requests.Session,
    base: str,
    token: str,
    account_id: int,
    from_email: str,
    to_email: str,
    subject: str,
    content: str,
) -> Dict[str, Any]:
    payload = {
        "sendEmail": from_email,
        "receiveEmail": [to_email],
        "accountId": account_id,
        "name": from_email.split("@", 1)[0],
        "subject": subject,
        "content": f"<p>{content}</p>",
        "sendType": "send",
        "text": content,
        "emailId": 0,
        "attachments": [],
        "draftId": None,
    }
    resp = session.post(
        f"{base}/api/email/send",
        headers={"Authorization": token, "Content-Type": "application/json"},
        json=payload,
        timeout=30,
        verify=False,
    )
    data = resp.json()
    return {"status": resp.status_code, "body": data, "raw": resp.text}


def _list_inbox(
    session: requests.Session,
    base: str,
    token: str,
    account_id: int,
    size: int = 5,
) -> Dict[str, Any]:
    resp = session.get(
        f"{base}/api/email/list",
        headers={"Authorization": token},
        params={
            "accountId": account_id,
            "allReceive": 0,
            "emailId": 0,
            "timeSort": "desc",
            "size": size,
            "type": 0,
        },
        timeout=20,
        verify=False,
    )
    data = resp.json()
    return {"status": resp.status_code, "body": data, "raw": resp.text}


def _first_subject(inbox_data: Dict[str, Any]) -> Optional[str]:
    items = ((inbox_data.get("body") or {}).get("data") or {}).get("list") or []
    if not items:
        return None
    return str(items[0].get("subject", ""))


def _gen_public_token(session: requests.Session, base: str, admin_email: str, admin_password: str) -> str:
    resp = session.post(
        f"{base}/api/public/genToken",
        json={"email": admin_email, "password": admin_password},
        timeout=20,
        verify=False,
    )
    data = resp.json()
    if resp.status_code != 200 or data.get("code") != 200:
        raise RuntimeError(f"生成 public token 失败: status={resp.status_code}, body={resp.text[:300]}")
    token = (data.get("data") or {}).get("token")
    if not token:
        raise RuntimeError(f"生成 public token 成功但无 token: {resp.text[:300]}")
    return token


def _public_email_list(
    session: requests.Session,
    base: str,
    public_token: str,
    target_email: str,
) -> Dict[str, Any]:
    resp = session.post(
        f"{base}/api/public/emailList",
        headers={"Authorization": public_token, "Content-Type": "application/json"},
        json={"toEmail": target_email, "timeSort": "desc", "num": 1, "size": 20},
        timeout=20,
        verify=False,
    )
    data = resp.json()
    return {"status": resp.status_code, "body": data, "raw": resp.text}


def main() -> int:
    parser = argparse.ArgumentParser(description="Cloud Mail 发件/收件连通性测试")
    parser.add_argument("--to", default="", help="收件人邮箱，默认发给管理员邮箱")
    parser.add_argument("--subject", default="cloud-mail-send-test", help="邮件主题")
    parser.add_argument("--content", default="cloud mail test", help="邮件正文(纯文本)")
    parser.add_argument("--probe-catchall", action="store_true", help="发送到随机未注册地址并轮询 public/emailList")
    args = parser.parse_args()

    cfg = load_config()
    admin_email = str(cfg.get("skymail_admin_email", "")).strip()
    admin_password = str(cfg.get("skymail_admin_password", "")).strip()
    if not admin_email or not admin_password:
        print("❌ config 缺少 skymail_admin_email/skymail_admin_password")
        return 1

    to_email = args.to.strip() or admin_email
    if args.probe_catchall:
        domains = cfg.get("skymail_domains") or []
        if not domains:
            print("❌ --probe-catchall 需要 config 中配置 skymail_domains")
            return 1
        probe_local = "probe" + "".join(random.choices(string.ascii_lowercase + string.digits, k=8))
        to_email = f"{probe_local}@{domains[0]}"
    base = _base_url(cfg)
    session = _build_session(cfg)

    print(f"[*] API: {base}")
    print(f"[*] From: {admin_email}")
    print(f"[*] To: {to_email}")

    try:
        token = _login(session, base, admin_email, admin_password)
        accounts = _get_accounts(session, base, token)
        account_id = _pick_account_id(accounts, admin_email)

        send_result = _send_email(
            session=session,
            base=base,
            token=token,
            account_id=account_id,
            from_email=admin_email,
            to_email=to_email,
            subject=args.subject,
            content=args.content,
        )
        print(f"[send] status={send_result['status']} body={json.dumps(send_result['body'], ensure_ascii=False)[:400]}")

        inbox_result = _list_inbox(session, base, token, account_id, size=5)
        first = _first_subject(inbox_result)
        print(f"[inbox] status={inbox_result['status']} latest_subject={first!r}")
        print(f"[inbox] body={json.dumps(inbox_result['body'], ensure_ascii=False)[:500]}")

        if args.probe_catchall:
            public_token = _gen_public_token(session, base, admin_email, admin_password)
            print(f"[catchall] polling public/emailList for {to_email}")
            found = False
            for i in range(10):
                result = _public_email_list(session, base, public_token, to_email)
                data = (result.get("body") or {}).get("data") or []
                print(f"[catchall] poll={i + 1} status={result['status']} count={len(data)}")
                if data:
                    subject = data[0].get("subject")
                    print(f"[catchall] ok subject={subject!r}")
                    found = True
                    break
                time.sleep(2)
            if not found:
                print("[catchall] 未在轮询窗口内看到邮件")
        return 0
    except Exception as e:
        print(f"❌ 测试失败: {e}")
        return 1


if __name__ == "__main__":
    sys.exit(main())
