#!/usr/bin/env python3
"""Mask sensitive data in scenario fixture files.

Replaces:
- Home directory paths (/Users/<username>/) -> /Users/user/
- UUIDs -> deterministic dummy values (00000000-0000-0000-0000-XXXXXXXXXXXX)
- URLs (https://...) -> https://example.com/masked
"""

import re
import glob
import os

FIXTURES_DIR = os.path.join(os.path.dirname(__file__), "..", "frontend", "src", "fixtures", "scenarios")

# Detect actual home directory username
HOME_USER = os.path.expanduser("~").split("/")[-1]

def mask_file(path: str) -> bool:
    with open(path) as f:
        content = f.read()

    original = content

    # Replace home directory
    content = content.replace(f"/Users/{HOME_USER}/", "/Users/user/")

    # Replace URLs
    content = re.sub(r"https?://[^\s\"'\\]+", "https://example.com/masked", content)

    # Replace UUIDs with deterministic dummies
    uuids = sorted(set(re.findall(r"[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}", content)))
    for i, uuid in enumerate(uuids):
        dummy = f"00000000-0000-0000-0000-{i:012d}"
        content = content.replace(uuid, dummy)

    if content != original:
        with open(path, "w") as f:
            f.write(content)
        return True
    return False


def main():
    pattern = os.path.join(FIXTURES_DIR, "*.ts")
    changed = 0
    for path in sorted(glob.glob(pattern)):
        if os.path.basename(path) == "index.ts":
            continue
        if mask_file(path):
            print(f"Masked: {os.path.basename(path)}")
            changed += 1
    if changed == 0:
        print("No masking needed.")
    else:
        print(f"Done: {changed} file(s) masked.")


if __name__ == "__main__":
    main()
