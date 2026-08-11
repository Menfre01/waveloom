#!/usr/bin/env python3
"""compare.py — 成功 vs 失败实例对比(规格书 Phase 3 组件)。

输入:results/ 下所有含 verdict.json 的实例(或显式指定实例列表)。
输出:JSON 对比表 + 可读文本,供 report.py 汇总。

对比维度:通过率、轮数、工具调用、错误数、token、缓存命中率、
盲编、read 次数、file_overlap、失败根因摘要。
"""
import json
import sys
from pathlib import Path

from analyze import analyze

ROOT = Path(__file__).parent


def discover_instances() -> list[str]:
    """扫描 results/ 下含 verdict.json 的实例,按实例 ID 排序。"""
    out = []
    for d in (ROOT / "results").iterdir():
        if d.is_dir() and (d / "verdict.json").exists():
            out.append(d.name)
    return sorted(out)


def resolved_of(instance_id: str) -> bool:
    r = json.loads((ROOT / "results" / instance_id / "verdict.json").read_text())
    return bool(r.get(instance_id, {}).get("resolved"))


def mean(xs: list[float]) -> float:
    return round(sum(xs) / len(xs), 1) if xs else 0.0


def compare(instances: list[str] | None = None) -> dict:
    ids = instances or discover_instances()
    if not ids:
        raise SystemExit("❌ results/ 下无实例(缺 verdict.json)")

    rows = []
    for iid in ids:
        try:
            a = analyze(iid)
            a["resolved"] = resolved_of(iid)
            rows.append(a)
        except FileNotFoundError as e:
            print(f"⚠ 跳过 {iid}: {e}", file=sys.stderr)

    passed = [r for r in rows if r["resolved"]]
    failed = [r for r in rows if not r["resolved"]]

    def group_stats(rs: list[dict]) -> dict:
        return {
            "count": len(rs),
            "turns_avg": mean([r["turns"] for r in rs]),
            "tool_calls_avg": mean([r["tool_calls"] for r in rs]),
            "tool_errors_avg": mean([r["tool_errors"] for r in rs]),
            "prompt_tokens_avg": mean([r["prompt_tokens"] for r in rs]),
            "completion_tokens_avg": mean([r["completion_tokens"] for r in rs]),
            "cache_hit_rate_avg": mean([r["cache_hit_rate"] for r in rs]),
            "read_count_avg": mean([r["read_count"] for r in rs]),
            "overlap_rate_avg": mean([r["overlap_rate"] for r in rs]),
            "blind_edits_total": sum(len(r["blind_edits"]) for r in rs),
        }

    return {
        "instances": rows,
        "summary": {
            "total": len(rows),
            "passed": len(passed),
            "failed": len(failed),
            "pass_rate": round(len(passed) / len(rows), 3) if rows else 0,
        },
        "passed_stats": group_stats(passed),
        "failed_stats": group_stats(failed),
    }


def render(result: dict) -> str:
    s = result["summary"]
    lines = [
        f"=== 对比:成功 {s['passed']}/{s['total']} (通过率 {s['pass_rate']*100:.0f}%) ===",
        "",
        f"{'指标':<22}{'成功':>12}{'失败':>12}",
        "-" * 46,
    ]
    p, f = result["passed_stats"], result["failed_stats"]
    metrics = [
        ("实例数", p["count"], f["count"]),
        ("平均轮数", p["turns_avg"], f["turns_avg"]),
        ("平均工具调用", p["tool_calls_avg"], f["tool_calls_avg"]),
        ("平均工具错误", p["tool_errors_avg"], f["tool_errors_avg"]),
        ("平均 prompt tokens", p["prompt_tokens_avg"], f["prompt_tokens_avg"]),
        ("平均 completion tokens", p["completion_tokens_avg"], f["completion_tokens_avg"]),
        ("缓存命中率", p["cache_hit_rate_avg"], f["cache_hit_rate_avg"]),
        ("平均 read 次数", p["read_count_avg"], f["read_count_avg"]),
        ("file_overlap 均值", p["overlap_rate_avg"], f["overlap_rate_avg"]),
        ("盲编总数", p["blind_edits_total"], f["blind_edits_total"]),
    ]
    for name, pv, fv in metrics:
        lines.append(f"{name:<22}{pv:>12}{fv:>12}")
    return "\n".join(lines)


def main():
    args = sys.argv[1:]
    result = compare(args or None)
    print(render(result))
    # 持久化供 report.py 复用
    (ROOT / "compare_result.json").write_text(json.dumps(result, indent=2, ensure_ascii=False))


if __name__ == "__main__":
    main()
