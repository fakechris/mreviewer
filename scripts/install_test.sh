#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$repo_root"

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

output="$("$repo_root/scripts/install.sh" --version v1.2.3 --install-dir /tmp/mreviewer-bin --dry-run)"

grep -q 'version=v1.2.3' <<<"$output"
grep -q 'url=https://github.com/fakechris/mreviewer/releases/download/v1.2.3/mreviewer_1.2.3_' <<<"$output"
grep -q 'checksum_url=https://github.com/fakechris/mreviewer/releases/download/v1.2.3/mreviewer_1.2.3_.*\.sha256' <<<"$output"
grep -q 'target=/tmp/mreviewer-bin/mreviewer' <<<"$output"
