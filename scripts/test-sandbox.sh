#!/usr/bin/env bash
# 沙箱双平台集成测试(完整验证流程)。
#
# 覆盖:
#   1. 本机集成测试 — 当前平台真实后端(macOS sandbox-exec / Linux bwrap),
#      另一平台的测试自动 SKIP(条件跳过)
#   2. Linux 容器验证 — 交叉编译测试二进制 + alpine 容器装 bubblewrap 跑
#      bwrap 集成测试(需要 Docker;无 Docker 时提示替代命令)
#
# 用法:
#   bash scripts/test-sandbox.sh
set -euo pipefail

cd "$(dirname "$0")/.."

echo "=============================================="
echo "1/2 本机集成测试(当前平台真实后端)"
echo "=============================================="
go test ./pkg/sandbox/ -run "Integration" -v 2>&1 | grep -E "^(--- (PASS|FAIL|SKIP)|ok |FAIL)" || true

echo
echo "=============================================="
echo "2/2 Linux 容器验证(bubblewrap 真实环境)"
echo "=============================================="
if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
    echo "→ 交叉编译 Linux 测试二进制..."
    GOOS=linux GOARCH="$(go env GOARCH)" CGO_ENABLED=0 go test -c ./pkg/sandbox/ -o /tmp/sandbox.test.linux
    echo "→ 启动 alpine 容器(privileged + seccomp=unconfined,装 bubblewrap)..."
    docker run --rm --privileged --security-opt seccomp=unconfined \
        -v /tmp/sandbox.test.linux:/sb.test \
        alpine:latest sh -c \
        'apk add --no-cache bubblewrap >/dev/null 2>&1 && chmod +x /sb.test && /sb.test -test.run BwrapIntegration -test.v 2>&1 | tail -20'
    echo "✓ Linux 容器验证完成"
else
    echo "⚠ docker 不可用,跳过容器验证。"
    echo "  在 Linux 本机直接运行:"
    echo "    sudo apt install bubblewrap   # Ubuntu/Debian"
    echo "    go test ./pkg/sandbox/ -run BwrapIntegration -v"
    echo "  Ubuntu 24.04+ 若被 AppArmor 拦截:"
    echo "    sudo sysctl -w kernel.apparmor_restrict_unprivileged_userns=0"
fi
