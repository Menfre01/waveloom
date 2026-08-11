#!/usr/bin/env python3
"""analyze.py 雏形:从 evidence 目录提取确定性评测指标。

输出(每实例 + 汇总):
- 轮数、工具调用序列、工具错误数
- token 统计(累计 + cache hit 率)
- file_overlap(model_patch vs gold_patch 文件交集)
- 盲编检测(edit 前同文件无 read,序列推断)
- 成功 vs 失败对比
"""
import json
import sys
from pathlib import Path

ROOT = Path(__file__).parent

def load_trace(instance_id: str) -> list[dict]:
    lines = (ROOT / "results" / instance_id / "trace.jsonl").read_text().splitlines()
    return [json.loads(l) for l in lines if l.strip()]

def tool_sequence(trace: list[dict]) -> list[dict]:
    """提取 (turn, tool_name, input_keys, is_error, read_files) 序列。"""
    seq = []
    pending = {}  # tool_use_id -> info
    for ev in trace:
        if ev.get("type") not in ("user", "assistant"):
            continue
        msg = ev.get("message", {})
        for block in msg.get("content", []):
            if not isinstance(block, dict):
                continue
            if block.get("type") == "tool_use":
                tid = block.get("id", "")
                pending[tid] = {
                    "name": block.get("name", ""),
                    "input": block.get("input", {}),
                }
            elif block.get("type") == "tool_result":
                tid = block.get("tool_use_id", "")
                info = pending.pop(tid, {})
                content = block.get("content", "")
                # 仅 bash 工具结果参与错误判定:read 返回的是代码内容,
                # 含 "error" 字样属正常(规格书 F2:需 IsError 结构化才精确)
                is_err = isinstance(content, str) and (
                    info.get("name") == "bash"
                    and ("Error" in content or "failed" in content.lower()
                         or "FAILED" in content)
                )
                seq.append({
                    "tool": info.get("name", "?"),
                    "input": info.get("input", {}),
                    "is_error": is_err,
                })
    return seq

def read_files_before_edit(seq: list[dict]) -> dict:
    """盲编检测:edit 前同文件是否有 read(序列推断)。"""
    reads = set()
    blind = []
    for s in seq:
        if s["tool"] == "read":
            reads.add(s["input"].get("file_path", ""))
        elif s["tool"] == "edit":
            fp = s["input"].get("file_path", "")
            if fp and fp not in reads:
                blind.append(fp)
    return {"blind_edits": blind, "read_count": len(reads)}

def token_stats(trace: list[dict]) -> dict:
    """每条 assistant 消息的 usage 累计。"""
    p = c = hit = miss = r = 0
    for ev in trace:
        if ev.get("type") != "assistant":
            continue
        u = ev.get("message", {}).get("usage") or {}
        p += u.get("PromptTokens", 0)
        c += u.get("CompletionTokens", 0)
        hit += u.get("CacheHitTokens", 0)
        miss += u.get("CacheMissTokens", 0)
        r += u.get("ReasoningTokens", 0)
    return {"prompt_tokens": p, "completion_tokens": c, "cache_hit": hit,
            "cache_miss": miss, "reasoning": r,
            "cache_hit_rate": round(hit / (hit + miss), 3) if hit + miss else 0}

def file_overlap(instance_id: str) -> dict:
    """model_patch vs gold_patch 文件交集。"""
    import re
    def files(p: Path):
        return set(re.findall(r"^diff --git a/(\S+) b/", p.read_text(), re.M))
    m = files(ROOT / "results" / instance_id / "model_patch.diff")
    g = files(ROOT / "results" / instance_id / "gold_patch.diff")
    inter = m & g
    return {"model_files": sorted(m), "gold_files": sorted(g),
            "overlap": sorted(inter),
            "overlap_rate": round(len(inter) / len(g), 3) if g else 0}

def analyze(instance_id: str) -> dict:
    trace = load_trace(instance_id)
    seq = tool_sequence(trace)
    blind = read_files_before_edit(seq)
    verdict = json.loads((ROOT / "results" / instance_id / "verdict.json").read_text())[instance_id]
    return {
        "instance_id": instance_id,
        "resolved": verdict.get("resolved"),
        "turns": len([ev for ev in trace if ev.get("type") == "assistant"]),
        "tool_calls": len(seq),
        "tool_errors": sum(1 for s in seq if s["is_error"]),
        "tool_sequence": [s["tool"] for s in seq],
        "errors_at": [i for i, s in enumerate(seq) if s["is_error"]],
        **token_stats(trace),
        **blind,
        **file_overlap(instance_id),
    }

def main():
    ids = sys.argv[1:] or ["pytest-dev__pytest-5809", "pylint-dev__pylint-4661"]
    results = [analyze(i) for i in ids]
    for r in results:
        print("=" * 60)
        for k, v in r.items():
            if k == "tool_sequence":
                print(f"  {k}: {' → '.join(v)}")
            else:
                print(f"  {k}: {v}")

if __name__ == "__main__":
    main()
