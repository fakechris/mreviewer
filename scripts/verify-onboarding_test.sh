#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$repo_root"

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

bin_dir="$tmpdir/bin"
mkdir -p "$bin_dir"

for cmd in bash cat cp dirname docker git grep mktemp pwd rm; do
  target="$(command -v "$cmd")"
  ln -s "$target" "$bin_dir/$cmd"
done

PATH="$bin_dir" bash scripts/verify-onboarding.sh
