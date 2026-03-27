#!/usr/bin/env python3
import re
from pathlib import Path
from urllib.parse import urljoin

import requests


BASE_URL = "https://docs.rosekhlifa.cn/"
HOME_MD_URL = urljoin(BASE_URL, "home.md")


def main() -> None:
    repo_root = Path(__file__).resolve().parent.parent
    public_dir = repo_root / "web" / "public"
    docs_md_path = public_dir / "docs.md"
    images_dir = public_dir / "images"
    images_dir.mkdir(parents=True, exist_ok=True)

    response = requests.get(HOME_MD_URL, timeout=30)
    response.raise_for_status()
    markdown = response.text
    docs_md_path.write_text(markdown, encoding="utf-8")

    image_paths = re.findall(r"!\[[^\]]*\]\(([^)]+)\)", markdown)
    downloaded = 0
    for image_path in image_paths:
        clean_path = image_path.strip()
        if not clean_path:
            continue
        image_url = urljoin(HOME_MD_URL, clean_path)
        image_name = Path(clean_path).name
        if not image_name:
            continue
        image_bytes = requests.get(image_url, timeout=30).content
        (images_dir / image_name).write_bytes(image_bytes)
        downloaded += 1

    print(f"docs saved: {docs_md_path}")
    print(f"images downloaded: {downloaded}")


if __name__ == "__main__":
    main()
