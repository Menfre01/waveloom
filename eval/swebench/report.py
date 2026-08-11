#!/usr/bin/env python3
"""report.py — 最终人类可读评测报告(规格书 Phase 3 汇总组件)。

聚合 analyze(指标)/compare(对比)/attribution(归因) 产出单一 report.md:
通过率、指标分布、成功 vs 失败对比、失败归因、改进建议。

用法:
    venv/bin/python report.py [输出路径,默认 report.md]
"""
import json
import sys
from datetime import datetime
from pathlib import Path

ROOT = Path(__file__).parent


def load(name: str, default: dict) -> dict:
    p = ROOT / name
    if not p.exists():
        return default
    return json.loads(p.read_text())


def markdown() -> str:
    compare = load("compare_result.json", {})
    attribution = load("attribution_result.json", {"attributions": []})
    instances = compare.get("instances", [])
    summary = compare.get("summary", {})
    p_stats = compare.get("passed_stats", {})
    f_stats = compare.get("failed_stats", {})

    lines = [
        "# SWE-bench 评测报告",
        "",
        f"> 生成时间:{datetime.now().strftime('%Y-%m-%d %H:%M')}",
        f"> 实例数:{summary.get('total', 0)} | "
        f"通过:{summary.get('passed', 0)} | "
        f"失败:{summary.get('failed', 0)} | "
        f"通过率:{summary.get('pass_rate', 0)*100:.0f}%",
        "",
        "## 1. 通过率",
        "",
        f"- **{summary.get('passed', 0)}/{summary.get('total', 0)}** "
        f"({summary.get('pass_rate', 0)*100:.0f}%)",
        "",
        "## 2. 实例明细",
        "",
        "| 实例 | 结果 | 轮数 | 工具 | 错误 | 缓存命中 | overlap |",
        "|------|------|------|------|------|---------|---------|",
    ]
    for r in sorted(instances, key=lambda x: x["instance_id"]):
        lines.append(
            f"| {r['instance_id']} | {'✅' if r['resolved'] else '❌'} "
            f"| {r['turns']} | {r['tool_calls']} | {r['tool_errors']} "
            f"| {r['cache_hit_rate']:.0%} | {r['overlap_rate']:.0%} |"
        )

    lines += ["", "## 3. 成功 vs 失败对比", "", "| 指标 | 成功 | 失败 |",
              "|------|------|------|"]
    metrics = [
        ("实例数", "count"), ("平均轮数", "turns_avg"),
        ("平均工具调用", "tool_calls_avg"), ("平均工具错误", "tool_errors_avg"),
        ("平均 prompt tokens", "prompt_tokens_avg"),
        ("平均 completion tokens", "completion_tokens_avg"),
        ("缓存命中率", "cache_hit_rate_avg"), ("平均 read 次数", "read_count_avg"),
        ("file_overlap 均值", "overlap_rate_avg"), ("盲编总数", "blind_edits_total"),
    ]
    for label, key in metrics:
        lines.append(f"| {label} | {p_stats.get(key, 0)} | {f_stats.get(key, 0)} |")

    lines += ["", "## 4. 失败归因(LLM)", ""]
    attrs = attribution.get("attributions", [])
    if not attrs:
        lines.append("无 FAIL 实例,跳过归因。")
    for a in attrs:
        lines += [
            f"### {a['instance_id']}",
            "",
            f"- **失败模式**:{a.get('failure_mode', '未知')}",
            f"- **根因**:{a.get('root_cause', '')}",
            "- **证据**:",
        ]
        for e in a.get("evidence", []):
            lines.append(f"  - {e}")
        sug = a.get("improvement_suggestion", "")
        if sug:
            lines += ["- **改进建议**:", f"  - {sug}", ""]

    # 改进建议聚合(去重)
    suggestions = {}
    for a in attrs:
        s = a.get("improvement_suggestion", "").strip()
        if s:
            suggestions.setdefault(s, []).append(a["instance_id"])
    if suggestions:
        lines += ["", "## 5. 改进建议汇总", ""]
        for sug, ids in suggestions.items():
            lines.append(f"- **[{', '.join(ids)}]** {sug}")

    lines += ["", "---", "*由 eval/swebench 自动生成,人类只读此报告。*", ""]
    return "\n".join(lines)


def main():
    out = sys.argv[1] if len(sys.argv) > 1 else str(ROOT / "report.md")
    Path(out).write_text(markdown())
    print(f"[report] 已生成 {out}")


if __name__ == "__main__":
    main()
