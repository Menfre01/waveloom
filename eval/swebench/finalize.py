#!/usr/bin/env python3
"""补齐 evidence 目录:复制 session → trace.jsonl/session.json,生成 meta.json。"""
import json
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).parent

def finalize(instance_id: str) -> None:
    inst = ROOT / "results" / instance_id
    sessions = inst / "sessions"
    # REGRESSION(2026-08-12 sphinx-9230 实测):重跑后 sessions/ 残留旧 session,
    # 按文件名(UUID)字典序取第一个会复制旧 session → trace 与 verdict 错位。
    # 改为按 mtime 取最新 session。
    jsonl = sorted(sessions.glob("*.jsonl"), key=lambda p: p.stat().st_mtime)
    if jsonl:
        (inst / "trace.jsonl").write_bytes(jsonl[-1].read_bytes())
    jsons = sorted(sessions.glob("*.json"), key=lambda p: p.stat().st_mtime)
    if jsons:
        (inst / "session.json").write_bytes(jsons[-1].read_bytes())

    # waveloom 版本/参数
    ver = subprocess.run(
        [str(Path("/Users/menfre/Workbench/waveloom/bin/waveloom")), "--version"],
        capture_output=True, text=True, timeout=30,
    ).stdout.strip()
    meta = {
        "instance_id": instance_id,
        "waveloom_version": ver,
        "waveloom_commit": subprocess.run(
            ["git", "-C", "/Users/menfre/Workbench/waveloom", "rev-parse", "HEAD"],
            capture_output=True, text=True, timeout=30,
        ).stdout.strip(),
        "max_turns": 25,
        "context_limit": 131072,
        "no_sandbox": True,
        "model": "deepseek-v4-flash(评测 settings sub_model + --model flash)",
        "prompt_template": "v5(2026-08-11: 纯任务声明 3 条;依赖/行为规则全部由产品系统 prompt 承担)",
        "trace_lines": len((inst / "trace.jsonl").read_text().splitlines()) if (inst / "trace.jsonl").exists() else 0,
    }
    (inst / "meta.json").write_text(json.dumps(meta, indent=2, ensure_ascii=False))
    print(f"[finalize] {instance_id}: trace={meta['trace_lines']} lines, waveloom={ver}")

if __name__ == "__main__":
    for iid in sys.argv[1:]:
        finalize(iid)
