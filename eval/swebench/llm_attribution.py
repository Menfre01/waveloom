#!/usr/bin/env python3
"""llm_attribution.py — FAIL 实例 LLM 归因(规格书 Phase 3 组件)。

对每个 FAIL 实例,构造证据包(problem_statement、model/gold patch 差异、
测试失败输出、工具序列摘要),调用 DeepSeek(flash)归因失败模式:
理解错 / 定位错 / 修改错 / 未完成 / 环境问题,并给出证据引用与改进建议。

输入:results/ 下 resolved=false 的实例(或显式指定)。
输出:attribution_result.json + 可读文本,供 report.py 汇总。
"""
import json
import sys
from pathlib import Path

import requests

from analyze import analyze

ROOT = Path(__file__).parent
API_URL = "https://api.deepseek.com/chat/completions"
MODEL = "deepseek-v4-flash"

FAILURE_MODES = ["理解错误", "定位错误", "修改错误", "未完成", "环境问题"]

def api_key() -> str:
    key_file = ROOT / ".api_key"
    return key_file.read_text().strip() if key_file.exists() else ""

def failed_instances(explicit: list[str] | None = None) -> list[str]:
    if explicit:
        return explicit
    out = []
    for d in (ROOT / "results").iterdir():
        if d.is_dir() and (d / "verdict.json").exists():
            r = json.loads((d / "verdict.json").read_text())
            if not r.get(d.name, {}).get("resolved"):
                out.append(d.name)
    return sorted(out)

def build_evidence(instance_id: str) -> dict:
    inst = ROOT / "results" / instance_id
    a = analyze(instance_id)
    gold = (inst / "gold_patch.diff").read_text(errors="replace")
    model = (inst / "model_patch.diff").read_text(errors="replace")
    test_log = (inst / "container-run.log").read_text(errors="replace")
    # 提取测试失败摘要(最后 3000 字符)
    fail_tail = test_log[-3000:] if test_log else ""
    return {
        "instance_id": instance_id,
        "problem_statement": (inst / "problem_statement.md").read_text(errors="replace")[:1500],
        "metrics": {k: a[k] for k in ("turns", "tool_calls", "tool_errors", "overlap_rate")},
        "tool_sequence": " → ".join(
            s["tool"] for s in __import__("analyze").tool_sequence(
                __import__("analyze").load_trace(instance_id))),
        "model_patch": model[:2000],
        "gold_patch": gold[:2000],
        "test_failure_tail": fail_tail,
    }

PROMPT_TEMPLATE = """你是 SWE-bench 失败归因分析器。分析以下失败实例,输出 JSON。

实例: {instance_id}
问题描述:
{problem_statement}

评测指标:
{tool_sequence}
(轮数 {turns},工具调用 {tool_calls},工具错误 {tool_errors},与 gold 文件重合率 {overlap_rate})

模型补丁(model_patch):
{model_patch}

参考补丁(gold_patch):
{gold_patch}

测试失败输出(末尾):
{test_failure_tail}

请从以下失败模式中选一个最贴切的:理解错误 / 定位错误 / 修改错误 / 未完成 / 环境问题
输出 JSON(不要多余文字):
{{
  "failure_mode": "选一个",
  "root_cause": "一句话根因",
  "evidence": ["具体证据1(引用文件/命令/输出片段)", "证据2"],
  "improvement_suggestion": "对 waveloom 的具体改进建议(可执行)"
}}
"""

def attribute(instance_id: str) -> dict:
    ev = build_evidence(instance_id)
    prompt = PROMPT_TEMPLATE.format(
        instance_id=instance_id,
        problem_statement=ev["problem_statement"],
        tool_sequence=ev["tool_sequence"],
        turns=ev["metrics"]["turns"],
        tool_calls=ev["metrics"]["tool_calls"],
        tool_errors=ev["metrics"]["tool_errors"],
        overlap_rate=ev["metrics"]["overlap_rate"],
        model_patch=ev["model_patch"],
        gold_patch=ev["gold_patch"],
        test_failure_tail=ev["test_failure_tail"],
    )
    resp = requests.post(
        API_URL,
        headers={"Authorization": f"Bearer {api_key()}"},
        json={
            "model": MODEL,
            "messages": [{"role": "user", "content": prompt}],
            "response_format": {"type": "json_object"},
            # reasoning 模型会先消耗 token:800 被 thinking 吃光导致 content 为空
            # (2026-08-11 实测);禁用 thinking + 放宽上限保证 JSON 完整输出
            "max_tokens": 4000,
            "thinking": {"type": "disabled"},
        },
        timeout=120,
    )
    resp.raise_for_status()
    content = resp.json()["choices"][0]["message"]["content"]
    try:
        parsed = json.loads(content)
    except json.JSONDecodeError:
        parsed = {"failure_mode": "未知", "root_cause": content[:300],
                  "evidence": [], "improvement_suggestion": ""}
    parsed["instance_id"] = instance_id
    # 校验 failure_mode 合法性
    if parsed.get("failure_mode") not in FAILURE_MODES:
        parsed["failure_mode"] = "未知"
    print(f"[attribution] {instance_id}: {parsed.get('failure_mode')} — "
          f"{parsed.get('root_cause', '')[:80]}")
    return parsed

def main():
    args = sys.argv[1:]
    force = "--force" in args
    ids = [a for a in args if not a.startswith("--")]
    ids = failed_instances(ids or None) if not force else (ids or failed_instances())
    if not ids:
        print("[attribution] 无 FAIL 实例(当前全部 PASS),跳过归因")
        return
    results = [attribute(i) for i in ids]
    out = {"model": MODEL, "attributions": results}
    (ROOT / "attribution_result.json").write_text(
        json.dumps(out, indent=2, ensure_ascii=False))
    print(f"[attribution] 已写入 attribution_result.json ({len(results)} 实例)")

if __name__ == "__main__":
    main()
