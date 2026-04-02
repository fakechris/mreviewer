#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$repo_root"

fail() {
  echo "release_test: $*" >&2
  exit 1
}

require_pattern() {
  local file="$1"
  local pattern="$2"
  local message="$3"
  if ! grep -Eq "$pattern" "$file"; then
    fail "$message"
  fi
}

require_multiline_pattern() {
  local file="$1"
  local pattern="$2"
  local message="$3"
  local python_bin="${PYTHON_BIN:-}"
  if [[ -z "$python_bin" ]]; then
    if [[ -x /usr/bin/python3 ]]; then
      python_bin="/usr/bin/python3"
    elif command -v python3 >/dev/null 2>&1; then
      python_bin="$(command -v python3)"
    elif command -v python >/dev/null 2>&1; then
      python_bin="$(command -v python)"
    else
      fail "missing python interpreter for multiline release checks"
    fi
  fi
  if ! PATTERN="$pattern" "$python_bin" - "$file" <<'PY'
import os
import pathlib
import re
import sys

path = pathlib.Path(sys.argv[1])
pattern = os.environ["PATTERN"]
text = path.read_text()
sys.exit(0 if re.search(pattern, text, re.S) else 1)
PY
  then
    fail "$message"
  fi
}

require_pattern ".github/workflows/release.yml" "checksums\\.txt" "release workflow must publish a consolidated checksums.txt"
require_pattern ".github/workflows/release.yml" 'archive%\.tar\.gz' "release workflow must derive consolidated checksums from the published .sha256 asset names"
require_pattern ".github/workflows/release.yml" "Formula/mreviewer\\.rb" "release workflow must manage the checked-in BrewTap formula"
require_pattern ".github/workflows/release.yml" "release/formula-\\$\\{VERSION\\}" "release workflow must sync the generated formula through a dedicated branch"
require_pattern ".github/workflows/release.yml" "gh pr create" "release workflow must open a PR for formula sync instead of pushing main directly"
require_pattern ".github/workflows/release.yml" "workflow_dispatch:" "release workflow must support manual dispatch publishing"
require_pattern ".github/workflows/release.yml" 'git fetch origin main .*"\$branch"' "release workflow must fetch the existing formula sync branch so reruns can reuse it safely"
require_multiline_pattern ".github/workflows/release.yml" 'if \[\[ -z "\$\(git status --porcelain -- Formula/mreviewer\.rb\)" \]\]; then\s+echo "branch=\$branch" >> "\$GITHUB_OUTPUT"\s+exit 0' "release workflow must still expose the formula sync branch when reruns find no diff"

require_pattern "README.md" "brew tap fakechris/mreviewer https://github.com/fakechris/mreviewer" "README.md must document brew tap installation with the explicit repo URL"
require_pattern "README.md" "brew install mreviewer" "README.md must document brew install mreviewer"
require_pattern "README.zh-CN.md" "brew tap fakechris/mreviewer https://github.com/fakechris/mreviewer" "README.zh-CN.md must document brew tap installation with the explicit repo URL"
require_pattern "README.zh-CN.md" "brew install mreviewer" "README.zh-CN.md must document brew install mreviewer"

require_pattern "scripts/install.sh" "checksum_url=" "install.sh dry-run output must include checksum_url"

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

compute_sha256() {
  local file="$1"
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$file" | awk '{print $1}'
    return
  fi
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$file" | awk '{print $1}'
    return
  fi
  fail "missing sha256 tool"
}

for target in "darwin amd64" "darwin arm64" "linux amd64" "linux arm64"; do
  read -r goos goarch <<<"$target"
  bin="$tmpdir/mreviewer"
  archive="$tmpdir/mreviewer_1.2.3_${goos}_${goarch}.tar.gz"
  GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "$bin" ./cmd/mreviewer >/dev/null
  tar -C "$tmpdir" -czf "$archive" mreviewer
  rm -f "$bin"
done

darwin_amd64_sha="$(compute_sha256 "$tmpdir/mreviewer_1.2.3_darwin_amd64.tar.gz")"
darwin_arm64_sha="$(compute_sha256 "$tmpdir/mreviewer_1.2.3_darwin_arm64.tar.gz")"
linux_amd64_sha="$(compute_sha256 "$tmpdir/mreviewer_1.2.3_linux_amd64.tar.gz")"
linux_arm64_sha="$(compute_sha256 "$tmpdir/mreviewer_1.2.3_linux_arm64.tar.gz")"

bash scripts/render-homebrew-formula.sh "v1.2.3" \
  "$darwin_amd64_sha" \
  "$darwin_arm64_sha" \
  "$linux_amd64_sha" \
  "$linux_arm64_sha" > "$tmpdir/mreviewer.rb"

ruby -c "$tmpdir/mreviewer.rb" >/dev/null
