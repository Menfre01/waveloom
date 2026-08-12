#!/usr/bin/env python3
"""SWE-bench 单实例 runner(正式版:单容器内完成修复 + 测试)。

架构(2026-08-10 升级,规格书 P0-1 反转):
- waveloom 二进制(linux-amd64)挂载进官方 eval 镜像容器
- agent 的 bash 工具运行在容器内 = testbed 环境(python 版本/依赖正确)
- agent 修复后可运行测试验证(pytest 直接可用)
- 修复完成后同一容器内执行 eval_script(应用官方 test_patch + 跑测试)
- API key 从宿主全局 settings 提取,经 LLM_API_KEY 注入容器(不落盘)

流程:checkout(宿主)→ 单容器[waveloom 修复 → eval_script 测试] → 宿主收集
  model_patch + get_eval_report 判定 → evidence 目录

用法:
    venv/bin/python run.py <instance_id> [<instance_id> ...] [--parallel N]
    --parallel N   并发容器数(默认 1;受 DeepSeek Flash ~10 RPM 限流约束)
"""
import argparse
from concurrent.futures import ThreadPoolExecutor, as_completed
import json
import os
import subprocess
import sys
import time
from pathlib import Path

ROOT = Path(__file__).parent
WAVELOOM_LINUX = ROOT / "bin" / "waveloom-linux-amd64"
SETTINGS = ROOT / "settings.json"
MAX_TURNS = 25
TIMEOUT = 40 * 60

def host_api_key() -> str:
    """提取 API key,来源优先级:环境变量 > 评测目录 .api_key 文件 > 宿主全局 settings。

    沙箱内 bash 读不到 ~/.waveloom/settings.json(Seatbelt 遮蔽),环境变量凭据
    也被剥离;因此在评测目录放置 .api_key 文件(权限 600,已 gitignore),
    使 run.py 可在 bash background 模式(沙箱内)运行。
    """
    # 1. 环境变量(宿主机非沙箱场景)
    key = os.environ.get("LLM_API_KEY", "")
    if key:
        return key
    # 2. 评测目录 .api_key 文件(沙箱内可读,工作区内)
    key_file = ROOT / ".api_key"
    if key_file.exists():
        key = key_file.read_text().strip()
        if key:
            return key
    # 3. 宿主全局 settings(仅沙箱外可读)
    p = Path.home() / ".waveloom" / "settings.json"
    try:
        data = json.loads(p.read_text())
        llm = data.get("llm", {})
        key = llm.get("api_key", "")
        if not key:
            profiles = llm.get("profiles", {})
            prov = llm.get("provider", "deepseek")
            key = profiles.get(prov, {}).get("api_key", "")
    except (OSError, json.JSONDecodeError):
        key = ""
    if not key:
        raise SystemExit(
            "❌ 无 API key:放置 eval/swebench/.api_key 文件(含 key,权限 600),"
            "或设置 LLM_API_KEY 环境变量")
    return key

def make_spec(instance_id: str):
    from swebench.harness.test_spec.test_spec import make_test_spec
    from datasets import load_dataset
    ds = load_dataset("princeton-nlp/SWE-bench_Verified", split="test")
    ex = next(e for e in ds if e["instance_id"] == instance_id)
    return make_test_spec(ex, namespace="swebench"), ex

def run_single(instance_id: str) -> int:
    inst_dir = ROOT / "results" / instance_id
    repo_dir = inst_dir / "repo"
    sessions_dir = inst_dir / "sessions"
    sessions_dir.mkdir(parents=True, exist_ok=True)

    spec, _ = make_spec(instance_id)
    prompt = (inst_dir / "prompt.txt").read_text()
    prompt_path = inst_dir / "container-prompt.txt"
    prompt_path.write_text(prompt)
    eval_script = inst_dir / "eval_script.sh"
    eval_script.write_text(spec.eval_script)

    cmd = [
        "docker", "run", "--rm",
        "-v", f"{repo_dir}:/testbed",
        "-v", f"{prompt_path}:/prompt.txt:ro",
        "-v", f"{WAVELOOM_LINUX}:/usr/local/bin/waveloom:ro",
        "-v", f"{SETTINGS}:/settings.json:ro",
        "-v", f"{eval_script}:/eval_script.sh:ro",
        "-v", f"{sessions_dir}:/sessions",
        "-e", f"LLM_API_KEY={host_api_key()}",
        "-e", "WAVELOOM_SESSION_DIR=/sessions",
        "-w", "/testbed",
        spec.instance_image_key,
        "bash", "-lc",
        "source /opt/miniconda3/bin/activate && conda activate testbed && "
        'waveloom "$(cat /prompt.txt)" '
        "--settings /settings.json --model deepseek-v4-flash "
        f"--max-turns {MAX_TURNS} --no-sandbox ; "
        "bash /eval_script.sh",
    ]
    log_path = inst_dir / "container-run.log"
    print(f"[run] 单容器启动: {instance_id}, image={spec.instance_image_key}, timeout={TIMEOUT}s")
    t0 = time.time()
    with open(log_path, "w") as logf:
        proc = subprocess.run(cmd, stdin=subprocess.DEVNULL,
                              stdout=logf, stderr=logf, timeout=TIMEOUT)
    elapsed = time.time() - t0
    print(f"[run] 容器结束: exit={proc.returncode}, {elapsed:.0f}s")
    return proc.returncode

def collect_patch(instance_id: str) -> None:
    """git diff 收集 model_patch.diff(容器内 eval_script 已含 git diff,
    这里宿主侧再收集一次,与 verdict 判定对齐)。"""
    repo_dir = ROOT / "results" / instance_id / "repo"
    patch = subprocess.run(["git", "-C", str(repo_dir), "diff"],
                           capture_output=True, text=True, timeout=60)
    out = ROOT / "results" / instance_id / "model_patch.diff"
    out.write_text(patch.stdout)
    print(f"[run] model_patch.diff: {len(patch.stdout)} bytes")

def verdict(instance_id: str) -> None:
    """官方 get_eval_report 判定,写 verdict.json。"""
    import re
    from swebench.harness.grading import get_eval_report, get_logs_eval
    spec, _ = make_spec(instance_id)
    pred = {"instance_id": instance_id,
            "model_patch": (ROOT / "results" / instance_id / "model_patch.diff").read_text()}
    log_path = str(ROOT / "results" / instance_id / "container-run.log")
    report = get_eval_report(spec, pred, log_path, include_tests_status=True)

    # REGRESSION(2026-08-11 sphinx 实例实测):官方 parser 对 tox 输出
    # (点号进度 + 汇总行,无逐测试 PASSED/FAILED 行)解析为空 status_map,
    # 导致 45 passed 全通过的实例被误判 FAIL。回退:汇总行判定。
    status_map, found = get_logs_eval(spec, log_path)
    if found and not status_map:
        log = Path(log_path).read_text(errors="replace")
        summary = re.findall(r"(\d+) (?:passed|failed)", log)
        if summary:
            n_fail = int(summary[-1]) if summary and len(summary) > 1 else 0
            n_pass = int(summary[0])
            # 汇总格式 "N passed" / "M failed" — 取最后一条 "N failed"(0 失败 = 全过)
            failed_m = re.findall(r"(\d+) failed", log)
            n_failed = int(failed_m[-1]) if failed_m else 0
            resolved = n_failed == 0
            r = report[instance_id]
            r["resolved"] = resolved
            r["status_map_empty_fallback"] = {
                "reason": "official parser empty status_map, fallback to summary",
                "n_passed": n_pass,
                "n_failed": n_failed,
            }
            # 标注 tests_status 来自汇总行推断(非逐测试解析),防止 analyze/report 误读 f2p
            if "tests_status" in r:
                r["tests_status"]["_from_summary"] = True
            print(f"[run] verdict: status_map 空,汇总回退 → resolved={resolved} "
                  f"(passed={n_pass}, failed={n_failed})")
    (ROOT / "results" / instance_id / "verdict.json").write_text(
        json.dumps(report, indent=2, ensure_ascii=False))
    print(f"[run] verdict resolved={report[instance_id]['resolved']}")

def process_one(instance_id: str) -> dict:
    """单实例全流程:单容器修复+测试 → patch → verdict。返回摘要。"""
    t0 = time.time()
    prepare_instance(instance_id)
    rc = run_single(instance_id)
    print(f"[run] {instance_id}: waveloom+测试 exit={rc}")
    collect_patch(instance_id)
    verdict(instance_id)
    # 自动补齐 evidence:meta.json + trace.jsonl + session.json
    from finalize import finalize
    finalize(instance_id)
    elapsed = time.time() - t0
    return {"instance_id": instance_id, "exit": rc, "elapsed_s": round(elapsed)}

def prepare_instance(instance_id: str) -> None:
    """数据提取 + checkout + prompt 生成(幂等:已存在则跳过)。"""
    inst_dir = ROOT / "results" / instance_id
    inst_dir.mkdir(parents=True, exist_ok=True)

    # 数据集字段(problem_statement / patch / test_patch / meta)
    if not (inst_dir / "instance.json").exists():
        subprocess.run([sys.executable, str(ROOT / "save_instance.py"), instance_id],
                       check=True, timeout=300)

    # checkout repo
    repo_dir = inst_dir / "repo"
    if not (repo_dir / ".git").exists():
        _clone_repo(instance_id, repo_dir)
        subprocess.run(["git", "-C", str(repo_dir), "checkout", "-q",
                        _base_commit(instance_id)], check=True, timeout=120)
    else:
        # 重置为干净 base_commit(并行重跑场景)
        subprocess.run(["git", "-C", str(repo_dir), "checkout", "-q", "--", "."],
                       check=True, timeout=120)

    # prompt 生成(v4 模板:精简为任务特有要求,通用行为规则由产品系统 prompt
    # pkg/prompt/default.md 承担——禁止回滚/即时验证/停止探索/最小修改已在产品侧,
    # 模板重复会污染评测归因,改进效果无法剥离模板变量。2026-08-11)
    if not (inst_dir / "prompt.txt").exists():
        stmt = (inst_dir / "problem_statement.md").read_text()
        template = (
            "\n\n请修复上述问题。要求:\n"
            "1. 先阅读相关文件定位根因,再修改代码\n"
            "2. 修改完成后运行相关测试验证\n"
            "3. 修改完成后简要说明改动内容与验证结果\n"
        )
        (inst_dir / "prompt.txt").write_text(stmt + template)

def _clone_repo(instance_id: str, repo_dir: Path) -> None:
    """优先本地 mirror 克隆(秒级),mirror 缺失时回退 GitHub 并提示。"""
    repo = _repo_full(instance_id)  # e.g. "pytest-dev/pytest"
    mirror = ROOT / "mirrors" / (repo.replace("/", "__") + ".git")
    url = f"https://github.com/{repo}"
    if mirror.exists():
        # 本地 mirror → 文件级 clone,秒级
        subprocess.run(["git", "clone", "-o", "origin", "--single-branch",
                        str(mirror), str(repo_dir)],
                       check=True, timeout=300)
        print(f"[run] {instance_id}: 从本地 mirror 克隆 {mirror}")
        return
    # 回退 GitHub(慢);提示可预先建 mirror
    print(f"[run] {instance_id}: mirror 不存在({mirror}),回退 GitHub 克隆——"
          f"建议预先执行: git clone --mirror {url} {mirror}")
    subprocess.run(["git", "clone", "-o", "origin", "--single-branch",
                    url, str(repo_dir)],
                   check=True, timeout=900)

def _repo_full(instance_id: str) -> str:
    meta = json.loads((ROOT / "results" / instance_id / "instance.json").read_text())
    return meta["repo"]

def _base_commit(instance_id: str) -> str:
    meta = json.loads((ROOT / "results" / instance_id / "instance.json").read_text())
    return meta["base_commit"]

def main():
    parser = argparse.ArgumentParser(description="SWE-bench 单容器评测 runner")
    parser.add_argument("instances", nargs="+", help="实例 ID 列表")
    parser.add_argument("--parallel", type=int, default=1,
                        help="并发容器数(默认 1;建议 ≤5,受 LLM API 限流约束)")
    args = parser.parse_args()

    results = []
    if args.parallel <= 1:
        for iid in args.instances:
            results.append(process_one(iid))
    else:
        print(f"[run] 并行执行 {len(args.instances)} 实例,并发 {args.parallel}")
        with ThreadPoolExecutor(max_workers=args.parallel) as pool:
            futures = {pool.submit(process_one, iid): iid for iid in args.instances}
            for fut in as_completed(futures):
                iid = futures[fut]
                try:
                    results.append(fut.result())
                except Exception as e:
                    print(f"[run] {iid} 失败: {e}", file=sys.stderr)
                    results.append({"instance_id": iid, "exit": -1, "error": str(e)})

    print("\n[run] === 汇总 ===")
    for r in sorted(results, key=lambda x: x["instance_id"]):
        print(f"  {r['instance_id']}: exit={r.get('exit')} "
              f"({r.get('elapsed_s', '?')}s)")

if __name__ == "__main__":
    main()
