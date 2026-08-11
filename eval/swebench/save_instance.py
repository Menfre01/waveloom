#!/usr/bin/env python3
"""从数据集提取实例数据(problem_statement / gold_patch / test_patch / instance.json)。"""
import json
import sys
from pathlib import Path

from datasets import load_dataset

ROOT = Path(__file__).parent
DS = "princeton-nlp/SWE-bench_Verified"


def save_instance(instance_id: str) -> None:
    ds = load_dataset(DS, split="test")
    ex = next(e for e in ds if e["instance_id"] == instance_id)
    inst = ROOT / "results" / instance_id
    inst.mkdir(parents=True, exist_ok=True)
    (inst / "problem_statement.md").write_text(ex["problem_statement"])
    (inst / "gold_patch.diff").write_text(ex["patch"])
    (inst / "test_patch.diff").write_text(ex["test_patch"])
    meta = {
        "instance_id": ex["instance_id"],
        "repo": ex["repo"],
        "base_commit": ex["base_commit"],
        "dataset": DS,
        "f2p": json.loads(ex["FAIL_TO_PASS"]),
        "p2p": json.loads(ex["PASS_TO_PASS"]),
    }
    (inst / "instance.json").write_text(json.dumps(meta, indent=2, ensure_ascii=False))
    print(f"saved {instance_id}: f2p={meta['f2p']} patch={len(ex['patch'])}B")


if __name__ == "__main__":
    save_instance(sys.argv[1])
