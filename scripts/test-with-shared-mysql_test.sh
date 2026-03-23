#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"

output="$(
  cd "$repo_root"
  bash scripts/test-with-shared-mysql.sh --dry-run ./internal/llm -run TestMiniMaxRequestShape
)"

grep -q '^ADMIN_DSN_ENV_VAR=MREVIEWER_TEST_ADMIN_DSN$' <<<"$output"
grep -q '^CONTAINER_NAME=mreviewer-test-mysql-' <<<"$output"
grep -qE '^GO_TEST_ARGS=\./internal/llm -run TestMiniMaxRequestShape($| )' <<<"$output"
