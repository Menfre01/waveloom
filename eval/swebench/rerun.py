#!/usr/bin/env python3
"""Phase 4 复测:8 实例 × 3 轮 × 2 策略(旧/新),新旧交替避免时段漂移。

每实例内部串行(old1 → new1 → old2 → new2 → old3 → new3),实例间并行;
每轮结果归档到 results/<id>/rerun/<strategy>/round<N>/,不覆盖主结果。

用法:
    venv/bin/python rerun.py [--parallel N] [--instances id1 id2 ...]
"""
import argparse
import json
import shutil
import sys
import time
from concurrent.futures import ThreadPoolExecutor, as_completed
from pathlib import Path

ROOT = Path(__file__).parent
sys.path.insert(0, str(ROOT))
import run as runner  # noqa: E402

DEFAULT_INSTANCES = [
    # 0-edit 失败模式(5):First-edit deadline 直接针对
    "pylint-dev__pylint-6386",
    "django__django-11477",
    "django__django-11734",
    "django__django-12663",
    "sympy__sympy-20916",
    # 晚 edit / 修改错误(3)
    "astropy__astropy-13453",
    "matplotlib__matplotlib-20488",
    "sphinx-doc__sphinx-9230",
]
ROUNDS = 3
PARALLEL = 8
ARCHIVE = ("verdict.json", "model_patch.diff", "trace.jsonl", "meta.json")

def run_one_round(iid: str, strat: str, rnd: int) -> dict:
    """单轮:切换二进制/settings → process_one → 归档结果。"""
    binary = (ROOT / "bin" / (
        "waveloom-linux-amd64-old" if strat == "old" else "waveloom-linux-amd64"
    )).resolve()
    settings = (ROOT / "settings-rerun.json").resolve()
    inst_dir = ROOT / "results" / iid
    arch = inst_dir / "rerun" / strat / f"round{rnd}"
    arch.mkdir(parents=True, exist_ok=True)

    t0 = time.time()
    res = runner.process_one(iid, binary=binary, settings=settings)
    elapsed = time.time() - t0

    for f in ARCHIVE:
        src = inst_dir / f
        if src.exists():
            shutil.copy2(src, arch / f)

    resolved = None
    vp = arch / "verdict.json"
    if vp.exists():
        d = json.loads(vp.read_text())
        k = next(iter(d))
        resolved = d[k].get("resolved")
    out = {"instance_id": iid, "strategy": strat, "round": rnd,
           "resolved": resolved, "elapsed_s": round(elapsed)}
    print(f"[rerun] {iid} {strat} r{rnd}: resolved={resolved} ({elapsed:.0f}s) → {arch.name}",
          flush=True)
    return out

def run_instance(iid: str) -> list:
    """单实例串行 3 轮 × 2 策略,交替执行。"""
    results = []
    for rnd in range(1, ROUNDS + 1):
        for strat in ("old", "new"):
            try:
                results.append(run_one_round(iid, strat, rnd))
            except Exception as e:
                print(f"[rerun] {iid} {strat} r{rnd} 失败: {e}", file=sys.stderr, flush=True)
                results.append({"instance_id": iid, "strategy": strat,
                                "round": rnd, "resolved": None, "error": str(e)})
    return results

def main():
    global ROUNDS
    parser = argparse.ArgumentParser(description="Phase 4 复测(新旧策略多数投票)")
    parser.add_argument("--parallel", type=int, default=PARALLEL)
    parser.add_argument("--rounds", type=int, default=ROUNDS,
                        help="每策略轮数(默认 3;快速验证可用 1-2)")
    parser.add_argument("--instances", nargs="+", default=DEFAULT_INSTANCES)
    args = parser.parse_args()
    ROUNDS = args.rounds

    print(f"[rerun] 复测启动: {len(args.instances)} 实例 × {args.rounds} 轮 × 2 策略,并行 {args.parallel}",
          flush=True)
    all_results = []
    t0 = time.time()
    with ThreadPoolExecutor(max_workers=args.parallel) as pool:
        futures = {pool.submit(run_instance, iid): iid for iid in args.instances}
        for fut in as_completed(futures):
            all_results.extend(fut.result())

    # 汇总:每实例每策略多数投票
    print("\n[rerun] === 复测汇总(多数投票)===", flush=True)
    from collections import defaultdict
    votes = defaultdict(lambda: defaultdict(list))
    for r in all_results:
        if r.get("resolved") is not None:
            votes[r["instance_id"]][r["strategy"]].append(r["resolved"])
    for iid in args.instances:
        line = f"  {iid}: "
        for strat in ("old", "new"):
            vs = votes[iid][strat]
            if not vs:
                line += f"{strat}=[无结果] "
                continue
            maj = sum(vs) > len(vs) / 2
            line += f"{strat}={sum(vs)}/{len(vs)}({'PASS' if maj else 'FAIL'}) "
        print(line, flush=True)
    print(f"[rerun] 完成,总耗时 {(time.time()-t0)/60:.0f} 分钟", flush=True)

if __name__ == "__main__":
    main()
