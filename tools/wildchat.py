# /// script
# requires-python = ">=3.11"
# dependencies = ["datasets>=3.0", "numpy"]
# ///
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 Scott Friedman
"""Public-control sample for sluice (PROJECT.md §5, source 4).

Downloads WildChat-1M through the Hugging Face datasets API, keeps English
conversations, takes the first user turn, and writes a uniform random sample
of N rows (seeded) as JSONL with a `rank` field. `harvest --wildchat N`
keeps the first N by rank that pass its filters, so N is oversampled here.

If the dataset is gated, set HF_TOKEN. If it is still unavailable, the
fallback dataset (LMSYS-Chat-1M) is used and recorded in each row's
`dataset` field; the substitution must also be recorded on the GitHub issue.
"""

import argparse
import json
import os
import pathlib
import sys


def first_user_turn(conv):
    for turn in conv or []:
        if turn.get("role") in ("user", "human"):
            return turn.get("content") or ""
    return ""


def load(name):
    from datasets import load_dataset

    token = os.environ.get("HF_TOKEN") or None
    return load_dataset(name, split="train", token=token)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--out", required=True)
    ap.add_argument("--n", type=int, required=True)
    ap.add_argument("--seed", type=int, required=True)
    ap.add_argument("--dataset", default="allenai/WildChat-1M")
    ap.add_argument("--fallback", default="lmsys/lmsys-chat-1m")
    args = ap.parse_args()

    # Keep the multi-GB download cache off the internal disk when possible.
    xtmp = pathlib.Path.home() / ".xtmp"
    if "HF_HOME" not in os.environ and xtmp.is_dir():
        os.environ["HF_HOME"] = str(xtmp / "hf")

    import numpy as np

    used = args.dataset
    try:
        ds = load(args.dataset)
    except Exception as e:  # gated, missing, or network failure
        print(f"wildchat.py: {args.dataset} unavailable ({e}); falling back to {args.fallback}", file=sys.stderr)
        used = args.fallback
        ds = load(args.fallback)

    ds = ds.filter(lambda r: r.get("language") == "English", num_proc=4)
    rng = np.random.default_rng(args.seed)
    order = rng.permutation(len(ds))[: args.n]

    out = pathlib.Path(args.out)
    out.parent.mkdir(parents=True, exist_ok=True)
    tmp = out.with_suffix(".tmp")
    with tmp.open("w") as f:
        for rank, idx in enumerate(order):
            row = ds[int(idx)]
            rid = row.get("conversation_hash") or row.get("conversation_id") or str(int(idx))
            ts = row.get("timestamp")
            f.write(json.dumps({
                "id": str(rid),
                "text": first_user_turn(row.get("conversation")),
                "timestamp": ts.isoformat() if hasattr(ts, "isoformat") else (str(ts) if ts else ""),
                "rank": rank,
                "dataset": used,
            }, ensure_ascii=False) + "\n")
    tmp.rename(out)
    print(f"wildchat.py: wrote {len(order)} rows from {used} to {out}")


if __name__ == "__main__":
    main()
