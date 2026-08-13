#!/usr/bin/env python3
"""build_testbed_spec.py — 输出构建 testbed env 所需的 spec 信息(JSON)。

用法: build_testbed_spec.py <instance_id>
输出: {"repo": "...", "python": "3.9", "packages": ["pytest", "..."], ...}
"""
import json
import re
import sys

from datasets import load_dataset
from swebench.harness.test_spec.test_spec import make_test_spec

iid = sys.argv[1]
ds = load_dataset("princeton-nlp/SWE-bench_Verified", split="test")
ex = next(e for e in ds if e["instance_id"] == iid)
spec = make_test_spec(ex, namespace="swebench")
script = spec.setup_env_script

# python 版本: conda create -n testbed python=3.9 [extra pkgs] -y
py_m = re.search(r"python=(\d+\.\d+)", script)

# requirements.txt heredoc 内容(多数 repo)
req_m = re.search(
    r"cat <<'EOF_\w+'\s*>\s*\$HOME/requirements.txt\n(.*?)\n\s*EOF",
    script, re.S)

# conda create 行内联包: python=3.9 pytest -y → pytest
# (requests 的 pytest、sympy 的 mpmath flake8 等在此)
create_m = re.search(r"conda create\s+(?:-c \S+\s+)*-n testbed python=\d+\.\d+\s+([^-][^-]*?)\s*-y", script)
inline = []
if create_m:
    inline = create_m.group(1).split()

# pip install 行: python -m pip install <pkgs>(行首,排除 -r/-e)
pip_m = re.findall(r"(?:^|\n)\s*python -m pip install\s+(?!-r|-e)([^\n]+)", script)
pip_pkgs = []
for m in pip_m:
    pip_pkgs.extend(m.split())

# 合并去重: requirements(若有) + conda create 内联 + pip install 行
pkgs = []
seen = set()
for chunk in (req_m.group(1).split() if req_m else [], inline, pip_pkgs):
    for p in chunk:
        p = p.strip()
        if p and p not in seen:
            seen.add(p)
            pkgs.append(p)

print(json.dumps({
    "repo": spec.repo,
    "python": py_m.group(1) if py_m else "3.9",
    "packages": pkgs,
}, ensure_ascii=False))
