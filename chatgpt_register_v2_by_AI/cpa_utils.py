import argparse
import asyncio
import json
import urllib.parse
from pathlib import Path

# 用法示例：
# 1) 仅检测 401：
#    python cpa_utils.py --cpa-token Bearer_xxx
# 2) 指定 CPA 地址并删除 401：
#    python cpa_utils.py --cpa-base-url http://localhost:8317 --cpa-token Bearer_xxx --delete
# 3) 保存检测结果到 JSON 文件：
#    python cpa_utils.py --cpa-token Bearer_xxx --output result.json
# 4) 批量上传目录下 JSON：
#    python cpa_utils.py --cpa-token Bearer_xxx --upload-dir ./tokens

try:
    import aiohttp
except ImportError:
    aiohttp = None

import requests

DEFAULT_MGMT_UA = "codex_cli_rs/0.76.0 (Debian 13.0.0; x86_64) WindowsTerminal"


def _mgmt_headers(token: str) -> dict:
    return {"Authorization": f"Bearer {token}", "Accept": "application/json"}


def _safe_json(text: str):
    try:
        return json.loads(text)
    except Exception:
        return {}


def _extract_account_id(item: dict):
    for key in ("chatgpt_account_id", "chatgptAccountId", "account_id", "accountId"):
        val = item.get(key)
        if val:
            return str(val)
    return None


def _get_item_type(item: dict) -> str:
    return str(item.get("type") or item.get("typo") or "")


def _read_json_file(path: Path):
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except Exception:
        return None


def _upload_one_json(base_url: str, token: str, path: Path, timeout: int = 30) -> bool:
    if not path.exists() or not path.is_file():
        return False
    data = _read_json_file(path)
    if data is None:
        return False
    content = json.dumps(data, ensure_ascii=False).encode("utf-8")
    files = {"file": (path.name, content, "application/json")}
    headers = {"Authorization": f"Bearer {token}"}
    try:
        resp = requests.post(f"{base_url}/v0/management/auth-files", files=files, headers=headers, timeout=timeout)
        return resp.status_code in (200, 201, 204)
    except Exception:
        return False


class Cpa401Checker:
    def __init__(self, base_url: str, token: str, target_type: str = "codex", user_agent: str = DEFAULT_MGMT_UA):
        self.base_url = (base_url or "").rstrip("/")
        self.token = token
        self.target_type = target_type
        self.user_agent = user_agent

    def fetch_auth_files(self, timeout: int = 15):
        resp = requests.get(f"{self.base_url}/v0/management/auth-files", headers=_mgmt_headers(self.token), timeout=timeout)
        resp.raise_for_status()
        data = resp.json()
        return (data.get("files") if isinstance(data, dict) else []) or []

    async def probe_401_async(self, workers: int = 20, timeout: int = 10, retries: int = 1, show_progress: bool = True, verbose: bool = False, batch_delay: float = 2.0, auto_delete: bool = False):
        if aiohttp is None:
            raise RuntimeError("需要安装 aiohttp: pip install aiohttp")
        files = self.fetch_auth_files(timeout)
        candidates = [f for f in files if _get_item_type(f).lower() == self.target_type.lower()]
        if not candidates:
            return {"total": len(files), "candidates": 0, "invalid_401": [], "errors": [], "error_stats": {}, "deleted_ok": 0, "deleted_fail": 0}

        semaphore = asyncio.Semaphore(max(1, workers))
        # 优化连接池配置
        connector = aiohttp.TCPConnector(
            limit=max(workers * 2, 50),
            limit_per_host=max(workers, 20),
            ttl_dns_cache=300,
            force_close=False,
            enable_cleanup_closed=True
        )
        client_timeout = aiohttp.ClientTimeout(total=max(1, timeout), connect=10, sock_read=max(1, timeout))
        errors = []
        invalid = []
        completed = 0
        error_types = {}  # 统计错误类型
        deleted_ok = 0
        deleted_fail = 0

        async def delete_one_immediate(session, name: str):
            """立即删除检测到的401文件"""
            nonlocal deleted_ok, deleted_fail
            if not name:
                return False
            encoded = urllib.parse.quote(name, safe="")
            try:
                async with session.delete(
                    f"{self.base_url}/v0/management/auth-files?name={encoded}",
                    headers=_mgmt_headers(self.token),
                    timeout=timeout,
                ) as resp:
                    text = await resp.text()
                    data = _safe_json(text)
                    success = resp.status == 200 and data.get("status") == "ok"
                    if success:
                        deleted_ok += 1
                        if show_progress:
                            print(f"\n🗑️  已删除: {name}", flush=True)
                    else:
                        deleted_fail += 1
                        if show_progress:
                            print(f"\n❌ 删除失败: {name}", flush=True)
                    return success
            except Exception as e:
                deleted_fail += 1
                if verbose:
                    print(f"\n❌ 删除异常: {name} - {e}", flush=True)
                return False

        async def probe_one(session, item):
            nonlocal completed
            auth_index = item.get("auth_index")
            name = item.get("name") or item.get("id")
            if not auth_index:
                completed += 1
                if show_progress:
                    msg = f"进度: {completed}/{len(candidates)} (跳过: {name})"
                    print(f"\r{msg:<80}", end="", flush=True)
                return {"name": name, "auth_index": auth_index, "invalid_401": False}
            
            account_id = _extract_account_id(item)
            header = {"Authorization": "Bearer $TOKEN$", "Content-Type": "application/json", "User-Agent": self.user_agent}
            if account_id:
                header["Chatgpt-Account-Id"] = account_id
            payload = {"authIndex": auth_index, "method": "GET", "url": "https://chatgpt.com/backend-api/wham/usage", "header": header}
            
            for attempt in range(retries + 1):
                try:
                    async with semaphore:
                        async with session.post(
                            f"{self.base_url}/v0/management/api-call",
                            headers={**_mgmt_headers(self.token), "Content-Type": "application/json"},
                            json=payload,
                            timeout=timeout,
                        ) as resp:
                            text = await resp.text()
                            if resp.status >= 400:
                                error_msg = f"HTTP {resp.status}"
                                if verbose:
                                    print(f"\n[错误] {name}: {error_msg} - {text[:100]}")
                                raise RuntimeError(error_msg)
                            data = _safe_json(text)
                            sc = data.get("status_code")
                            is_401 = sc == 401
                            result = {"name": name, "auth_index": auth_index, "invalid_401": is_401}
                            completed += 1
                            if show_progress:
                                status = "❌401" if is_401 else "✓"
                                short_name = name[:35] if len(name) > 35 else name
                                msg = f"进度: {completed}/{len(candidates)} {status} {short_name}"
                                print(f"\r{msg:<80}", end="", flush=True)
                            
                            # 如果是401且开启自动删除，立即删除
                            if is_401 and auto_delete:
                                await delete_one_immediate(session, name)
                            
                            return result
                except asyncio.TimeoutError:
                    error_type = "Timeout"
                    if attempt >= retries:
                        error_types[error_type] = error_types.get(error_type, 0) + 1
                        errors.append({"name": name, "auth_index": auth_index, "error": error_type})
                        completed += 1
                        if show_progress:
                            short_name = name[:35] if len(name) > 35 else name
                            msg = f"进度: {completed}/{len(candidates)} ⏱️ {short_name}"
                            print(f"\r{msg:<80}", end="", flush=True)
                        if verbose:
                            print(f"\n[超时] {name}")
                        return {"name": name, "auth_index": auth_index, "invalid_401": False}
                    await asyncio.sleep(1)  # 超时后等待更长时间
                except aiohttp.ClientError as e:
                    error_type = f"ClientError: {type(e).__name__}"
                    if attempt >= retries:
                        error_types[error_type] = error_types.get(error_type, 0) + 1
                        errors.append({"name": name, "auth_index": auth_index, "error": str(e)})
                        completed += 1
                        if show_progress:
                            short_name = name[:35] if len(name) > 35 else name
                            msg = f"进度: {completed}/{len(candidates)} ⚠️ {short_name}"
                            print(f"\r{msg:<80}", end="", flush=True)
                        if verbose:
                            print(f"\n[网络错误] {name}: {e}")
                        return {"name": name, "auth_index": auth_index, "invalid_401": False}
                    await asyncio.sleep(0.5)
                except Exception as e:
                    error_type = f"Exception: {type(e).__name__}"
                    if attempt >= retries:
                        error_types[error_type] = error_types.get(error_type, 0) + 1
                        errors.append({"name": name, "auth_index": auth_index, "error": str(e)})
                        completed += 1
                        if show_progress:
                            short_name = name[:35] if len(name) > 35 else name
                            msg = f"进度: {completed}/{len(candidates)} ⚠️ {short_name}"
                            print(f"\r{msg:<80}", end="", flush=True)
                        if verbose:
                            print(f"\n[异常] {name}: {e}")
                        return {"name": name, "auth_index": auth_index, "invalid_401": False}
                    await asyncio.sleep(0.5)
            
            completed += 1
            return {"name": name, "auth_index": auth_index, "invalid_401": False}

        async with aiohttp.ClientSession(connector=connector, timeout=client_timeout, trust_env=True) as session:
            # 分批处理，每批最多50个，批次间添加延迟避免服务器过载
            batch_size = 50
            total_batches = (len(candidates) + batch_size - 1) // batch_size
            for batch_idx, i in enumerate(range(0, len(candidates), batch_size), 1):
                batch = candidates[i:i + batch_size]
                tasks = [asyncio.create_task(probe_one(session, item)) for item in batch]
                for task in asyncio.as_completed(tasks):
                    r = await task
                    if r.get("invalid_401"):
                        invalid.append(r)
                
                # 批次间延迟，避免服务器过载（最后一批不需要延迟）
                if batch_idx < total_batches and batch_delay > 0:
                    if show_progress:
                        msg = f"批次 {batch_idx}/{total_batches} 完成，等待 {batch_delay}秒..."
                        print(f"\r{msg:<80}", end="", flush=True)
                    await asyncio.sleep(batch_delay)
        
        if show_progress:
            print()  # 换行

        return {"total": len(files), "candidates": len(candidates), "invalid_401": invalid, "errors": errors, "error_stats": error_types, "deleted_ok": deleted_ok, "deleted_fail": deleted_fail}

    async def delete_by_name_async(self, names, workers: int = 5, timeout: int = 10, show_progress: bool = True):
        """删除401文件，最高5个线程同时处理，每批最多50个"""
        if aiohttp is None:
            raise RuntimeError("需要安装 aiohttp: pip install aiohttp")
        if not names:
            return {"deleted_ok": 0, "deleted_fail": 0}

        # 限制最高5个并发
        workers = min(workers, 5)
        semaphore = asyncio.Semaphore(workers)
        connector = aiohttp.TCPConnector(
            limit=20,  # 总连接数限制
            limit_per_host=10,  # 每个主机的连接数
            force_close=False,
            enable_cleanup_closed=True
        )
        client_timeout = aiohttp.ClientTimeout(total=max(1, timeout), connect=5)
        deleted_ok = 0
        deleted_fail = 0
        completed = 0

        async def delete_one(session, name: str):
            nonlocal deleted_ok, deleted_fail, completed
            if not name:
                completed += 1
                return False
            encoded = urllib.parse.quote(name, safe="")
            try:
                async with semaphore:
                    async with session.delete(
                        f"{self.base_url}/v0/management/auth-files?name={encoded}",
                        headers=_mgmt_headers(self.token),
                        timeout=timeout,
                    ) as resp:
                        text = await resp.text()
                        data = _safe_json(text)
                        success = resp.status == 200 and data.get("status") == "ok"
                        completed += 1
                        if show_progress:
                            status = "✓" if success else "✗"
                            # 截断文件名并用固定宽度清除残留
                            short_name = name[:35] if len(name) > 35 else name
                            msg = f"删除进度: {completed}/{len(names)} {status} {short_name}"
                            print(f"\r{msg:<80}", end="", flush=True)
                        return success
            except Exception:
                completed += 1
                if show_progress:
                    short_name = name[:35] if len(name) > 35 else name
                    msg = f"删除进度: {completed}/{len(names)} ✗ {short_name}"
                    print(f"\r{msg:<80}", end="", flush=True)
                return False

        async with aiohttp.ClientSession(connector=connector, timeout=client_timeout, trust_env=True) as session:
            # 分批删除，每批最多50个
            batch_size = 50
            for i in range(0, len(names), batch_size):
                batch = names[i:i + batch_size]
                tasks = [asyncio.create_task(delete_one(session, name)) for name in batch]
                for task in asyncio.as_completed(tasks):
                    if await task:
                        deleted_ok += 1
                    else:
                        deleted_fail += 1
        
        if show_progress:
            print()  # 换行

        return {"deleted_ok": deleted_ok, "deleted_fail": deleted_fail}

    def probe_401_sync(self, workers: int = 20, timeout: int = 10, retries: int = 1, show_progress: bool = True, verbose: bool = False, batch_delay: float = 2.0, auto_delete: bool = False):
        return asyncio.run(self.probe_401_async(workers, timeout, retries, show_progress, verbose, batch_delay, auto_delete))

    def delete_by_name_sync(self, names, workers: int = 5, timeout: int = 10, show_progress: bool = True):
        """同步删除接口，最高5个线程"""
        return asyncio.run(self.delete_by_name_async(names, workers, timeout, show_progress))


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--cpa-base-url", default="http://localhost:8317", help="CPA 基础地址")
    parser.add_argument("--cpa-token", required=True, help="CPA 管理 token (Bearer)")
    parser.add_argument("--workers", type=int, default=6, help="并发探测数（默认6，建议5-20），删除时最高5个线程")
    parser.add_argument("--timeout", type=int, default=20, help="请求超时（秒，默认20）")
    parser.add_argument("--retries", type=int, default=1, help="重试次数")
    parser.add_argument("--output", default="", help="输出 JSON 文件路径")
    parser.add_argument("--delete", action="store_true", help="删除检测到的 401 凭证")
    parser.add_argument("--upload-dir", default="", help="批量上传 JSON 文件的目录")
    parser.add_argument("--no-progress", action="store_true", help="不显示进度条")
    parser.add_argument("--verbose", action="store_true", help="显示详细错误信息")
    parser.add_argument("--batch-delay", type=float, default=2.0, help="批次间延迟（秒，默认2.0，可设置避免服务器过载）")
    args = parser.parse_args()

    base_url = (args.cpa_base_url or "").rstrip("/")
    show_progress = not args.no_progress

    if args.upload_dir:
        upload_dir = Path(args.upload_dir).expanduser().resolve()
        if not upload_dir.exists() or not upload_dir.is_dir():
            raise SystemExit(f"目录不存在: {upload_dir}")
        files = sorted(upload_dir.glob("*.json"))
        uploaded_ok = 0
        uploaded_fail = 0
        for idx, path in enumerate(files, 1):
            if show_progress:
                short_name = path.name[:35] if len(path.name) > 35 else path.name
                msg = f"上传进度: {idx}/{len(files)} {short_name}"
                print(f"\r{msg:<80}", end="", flush=True)
            if _upload_one_json(base_url, args.cpa_token, path):
                uploaded_ok += 1
            else:
                uploaded_fail += 1
        if show_progress:
            print()
        print(f"uploaded_ok={uploaded_ok} uploaded_fail={uploaded_fail}")
        return

    checker = Cpa401Checker(base_url, args.cpa_token)
    result = checker.probe_401_sync(args.workers, args.timeout, args.retries, show_progress, args.verbose, args.batch_delay, args.delete)

    invalid = result.get("invalid_401", [])
    error_stats = result.get("error_stats", {})
    
    print(f"total={result.get('total')} candidates={result.get('candidates')} invalid_401={len(invalid)} errors={len(result.get('errors', []))}")
    
    # 显示错误统计
    if error_stats:
        print("\n错误类型统计:")
        for error_type, count in sorted(error_stats.items(), key=lambda x: x[1], reverse=True):
            print(f"  {error_type}: {count}")
    
    # 如果开启了自动删除，显示删除统计
    if args.delete:
        print(f"\n删除统计: deleted_ok={result.get('deleted_ok', 0)} deleted_fail={result.get('deleted_fail', 0)}")
    else:
        # 未开启自动删除时，显示检测到的401列表
        for item in invalid:
            print(f"401: name={item.get('name')} auth_index={item.get('auth_index')}")

    if args.output:
        with open(args.output, "w", encoding="utf-8") as f:
            json.dump(result, f, ensure_ascii=False, indent=2)


if __name__ == "__main__":
    main()
