#!/usr/bin/env python3
"""Build control cases (correct changes with no known defects) from this
repository's own history, so the bench can count false positives on changes a
reviewer should wave through.

Usage: python3 scripts/review-bench/build_control_cases.py <out_dir> <sha> [<sha> ...]
Each commit becomes <out_dir>/<short_sha>/case.json with kind "control" and no
golden findings. Pick refactor-only or docs-only commits that were merged and
never reverted.
"""
import datetime, json, os, re, subprocess, sys

def git(*args):
    return subprocess.run(["git", *args], capture_output=True, text=True, check=True).stdout

def split_patch(patch):
    files, cur = [], None
    for line in patch.splitlines():
        if line.startswith("diff --git "):
            if cur: files.append(cur)
            m = re.match(r'diff --git a/(.*?) b/(.*)$', line)
            cur = {"old_path": m.group(1), "new_path": m.group(2), "lines": [], "new_file": False, "deleted_file": False, "renamed_file": m.group(1) != m.group(2)}
            continue
        if cur is None: continue
        if line.startswith("new file mode"): cur["new_file"] = True; continue
        if line.startswith("deleted file mode"): cur["deleted_file"] = True; continue
        if line.startswith(("index ", "--- ", "+++ ", "similarity index", "rename from", "rename to", "old mode", "new mode", "Binary files")):
            continue
        cur["lines"].append(line)
    if cur: files.append(cur)
    out = []
    for f in files:
        body = "\n".join(f.pop("lines"))
        if "@@" not in body: continue  # binary or mode-only
        f["diff"] = body
        out.append(f)
    return out

def main():
    out_dir, shas = sys.argv[1], sys.argv[2:]
    today = datetime.date.today().isoformat()
    repo = git("config", "--get", "remote.origin.url").strip()
    repo = re.sub(r'.*github.com[:/]', '', repo).removesuffix('.git')
    for sha in shas:
        full = git("rev-parse", sha).strip()
        parent = git("rev-parse", sha + "^").strip()
        subject = git("log", "-1", "--format=%s", sha).strip()
        body = git("log", "-1", "--format=%b", sha).strip()
        patch = git("diff-tree", "-p", "--no-color", "-M", "--root", full)
        diffs = split_patch(patch)
        if not diffs:
            print("skip (no text diffs)", sha); continue
        changed = sum(l.startswith(("+", "-")) for d in diffs for l in d["diff"].splitlines())
        case = {
            "id": f"control/{full[:10]}", "source": "mreviewer-history", "kind": "control", "added_at": today,
            "pr": {"url": f"https://github.com/{repo}/commit/{full}", "owner": repo.split('/')[0], "repo": repo.split('/')[1], "number": 0,
                   "base_sha": parent, "head_sha": full, "title": subject, "description": body[:2000],
                   "language": "Go", "category": "control", "changed_lines": changed},
            "diffs": diffs, "golden": [], "negatives": [],
        }
        d = os.path.join(out_dir, full[:10]); os.makedirs(d, exist_ok=True)
        with open(os.path.join(d, "case.json"), "w") as fh:
            json.dump(case, fh, ensure_ascii=False, indent=1)
        print("ok", full[:10], changed, "lines", len(diffs), "files", subject[:60])

if __name__ == "__main__":
    main()
