#!/bin/bash
# build_testbed.sh — 非容器评测 testbed env 构建(per-repo)。
#
# 用法:
#   eval/swebench/build_testbed.sh <instance_id> [env_prefix]
#     例: eval/swebench/build_testbed.sh pytest-dev__pytest-5809
#
# 流程(与官方 setup_env_script 对齐):
#   1. build_testbed_spec.py 提取 python 版本 + 依赖包
#      (requirements.txt + conda create 内联包 + pip install 行)
#   2. uv venv --python <ver> <env_prefix>
#   3. uv pip install 依赖 + pip(uv venv 默认无 pip,
#      eval_script 的 `pip install -e .` 需要;2026-08 实测坑)
#   4. uv pip install -e <results/<id>/repo>(预装 repo,agent 可直接自测)
#
# 输出:/tmp/<repo>-testbed/bin/python(env_prefix 覆盖时 <env_prefix>/bin/python)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
INSTANCE_ID="${1:?用法: build_testbed.sh <instance_id> [env_prefix]}"
ENV_PREFIX="${2:-}"

# 1. 提取 spec(python 版本 + 依赖包)
SPEC_JSON="$("$SCRIPT_DIR/venv/bin/python" "$SCRIPT_DIR/build_testbed_spec.py" "$INSTANCE_ID")"
REPO="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["repo"])' <<< "$SPEC_JSON")"
PY_VER="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["python"])' <<< "$SPEC_JSON")"
PKGS="$(python3 -c 'import json,sys; print(" ".join(json.load(sys.stdin)["packages"]))' <<< "$SPEC_JSON")"

if [ -z "$ENV_PREFIX" ]; then
  ENV_PREFIX="/tmp/$(echo "$REPO" | tr '/' '_')-testbed"
fi
echo "[testbed] repo=$REPO python=$PY_VER env=$ENV_PREFIX"
echo "[testbed] packages: ${PKGS:-(无)}"

# 2. uv venv
uv venv --python "$PY_VER" "$ENV_PREFIX" 2>&1 | tail -1

# 3. 依赖 + pip 补齐
if [ -n "$PKGS" ]; then
  # shellcheck disable=SC2086
  uv pip install --python "$ENV_PREFIX/bin/python" $PKGS 2>&1 | tail -1
fi
uv pip install --python "$ENV_PREFIX/bin/python" pip 2>&1 | tail -1

# 4. 预装 repo(editable,agent 可直接 pytest 自测)
REPO_DIR="$SCRIPT_DIR/results/$INSTANCE_ID/repo"
if [ -d "$REPO_DIR" ]; then
  uv pip install --python "$ENV_PREFIX/bin/python" -e "$REPO_DIR" 2>&1 | tail -1
else
  echo "[testbed] 警告: $REPO_DIR 不存在,跳过 repo 预装(仅判定环境)"
fi

echo "[testbed] 完成: $ENV_PREFIX/bin/python"
