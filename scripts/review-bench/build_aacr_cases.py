#!/usr/bin/env python3
"""Build offline review-bench cases from AACR-Bench (Alibaba-Aone/aacr-bench).

Usage: python3 scripts/review-bench/build_aacr_cases.py <dataset.json> <out_dir> [--max-prs 40] [--max-lines 600]

For each selected PR it fetches the GitHub compare between pr_source_commit
(base) and pr_target_commit (reviewed revision) with `gh api`, and writes
<out_dir>/<owner>__<repo>__<number>/case.json with the diffs, the expert
labelled golden comments (label=1) and the known-incorrect comments (label=0).
Requires an authenticated `gh`. Re-running skips cases that already exist.
"""
import collections, json, os, re, subprocess, sys, datetime

def gh(path, jq=None):
    cmd = ["gh", "api", path]
    if jq: cmd += ["--jq", jq]
    out = subprocess.run(cmd, capture_output=True, text=True)
    if out.returncode != 0:
        raise RuntimeError(out.stderr.strip()[:300])
    return out.stdout

def select_prs(rows, max_prs, max_lines, per_lang):
    by = collections.defaultdict(list)
    for r in rows: by[r["pr_url"]].append(r)
    cands = []
    for url, rs in by.items():
        lines = int(rs[0]["pr_change_line_count"])
        golden = sum(1 for r in rs if r["label"] == 1)
        if lines > max_lines or golden == 0: continue
        cands.append((rs[0]["project_main_language"], -golden, lines, url, rs))
    cands.sort(key=lambda c: (c[0], c[1], c[2]))
    picked, per = [], collections.Counter()
    # round-robin across languages so no single language dominates
    buckets = collections.defaultdict(list)
    for c in cands: buckets[c[0]].append(c)
    langs = sorted(buckets)
    while len(picked) < max_prs and any(buckets[l] for l in langs):
        for l in langs:
            if buckets[l] and per[l] < per_lang and len(picked) < max_prs:
                picked.append(buckets[l].pop(0)); per[l] += 1
        if all(per[l] >= per_lang or not buckets[l] for l in langs): break
    return picked

def main():
    args = sys.argv[1:]
    dataset, out_dir = args[0], args[1]
    max_prs = int(args[args.index("--max-prs")+1]) if "--max-prs" in args else 40
    max_lines = int(args[args.index("--max-lines")+1]) if "--max-lines" in args else 600
    per_lang = int(args[args.index("--per-lang")+1]) if "--per-lang" in args else 6
    rows = json.load(open(dataset))
    os.makedirs(out_dir, exist_ok=True)
    today = datetime.date.today().isoformat()
    for lang, _, lines, url, rs in select_prs(rows, max_prs, max_lines, per_lang):
        m = re.match(r"https://github.com/([^/]+)/([^/]+)/pull/(\d+)", url)
        owner, repo, number = m.groups()
        case_id = f"{owner}__{repo}__{number}"
        case_dir = os.path.join(out_dir, case_id)
        if os.path.exists(os.path.join(case_dir, "case.json")):
            print("skip", case_id); continue
        base, head = rs[0]["pr_source_commit"], rs[0]["pr_target_commit"]
        try:
            cmp_ = json.loads(gh(f"repos/{owner}/{repo}/compare/{base}...{head}"))
            pr = json.loads(gh(f"repos/{owner}/{repo}/pulls/{number}", "{title,body}"))
        except RuntimeError as e:
            print("fail", case_id, e); continue
        diffs = []
        for f in cmp_.get("files", []):
            if "patch" not in f: continue  # binary or too large
            status = f.get("status")
            diffs.append({
                "old_path": f.get("previous_filename") or f["filename"],
                "new_path": f["filename"],
                "diff": f["patch"],
                "new_file": status == "added",
                "deleted_file": status == "removed",
                "renamed_file": status == "renamed",
            })
        def comment(r):
            return {"path": r["path"], "side": r["side"], "from_line": r["from_line"], "to_line": r["to_line"],
                    "category": r["category"], "context": r["context"], "note": r["note"],
                    "source": r["source_model"] or "human"}
        case = {
            "id": f"aacr/{case_id}", "source": "aacr-bench", "kind": "golden", "added_at": today,
            "pr": {"url": url, "owner": owner, "repo": repo, "number": int(number), "base_sha": base, "head_sha": head,
                   "title": pr.get("title") or "", "description": (pr.get("body") or "")[:2000],
                   "language": lang, "category": rs[0]["pr_category"], "changed_lines": lines},
            "diffs": diffs,
            "golden": [comment(r) for r in rs if r["label"] == 1],
            "negatives": [comment(r) for r in rs if r["label"] == 0],
        }
        os.makedirs(case_dir, exist_ok=True)
        with open(os.path.join(case_dir, "case.json"), "w") as fh:
            json.dump(case, fh, ensure_ascii=False, indent=1)
        print("ok", case_id, lang, lines, "golden", len(case["golden"]), "neg", len(case["negatives"]), "files", len(diffs))

if __name__ == "__main__":
    main()
