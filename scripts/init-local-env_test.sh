#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

output_file="$tmpdir/.env"

(
  cd "$repo_root"
  GITLAB_TOKEN="gitlab-token-for-test" \
  MINIMAX_API_KEY="minimax-token-for-test" \
  bash scripts/init-local-env.sh --output "$output_file"
)

grep -q '^GITLAB_BASE_URL=https://gitlab.example.com$' "$output_file"
grep -q '^GITLAB_TOKEN=gitlab-token-for-test$' "$output_file"
grep -q '^MINIMAX_API_KEY=minimax-token-for-test$' "$output_file"
grep -q '^MINIMAX_BASE_URL=https://api.minimaxi.com/anthropic$' "$output_file"
grep -q '^MINIMAX_MODEL=MiniMax-M2.7-highspeed$' "$output_file"
grep -q '^REVIEW_MODEL_CHAIN=review_primary$' "$output_file"
grep -q '^REVIEW_ADVISOR_CHAIN=advisor_primary$' "$output_file"
grep -q '^REVIEW_PACKS=security,architecture,database$' "$output_file"
grep -q '^MYSQL_DSN=mreviewer:mreviewer_password@tcp(127.0.0.1:3306)/mreviewer?parseTime=true&loc=UTC&charset=utf8mb4&collation=utf8mb4_unicode_ci$' "$output_file"
grep -q '^REDIS_ADDR=127.0.0.1:6380$' "$output_file"
grep -q '^GITLAB_WEBHOOK_SECRET=' "$output_file"

if (
  cd "$repo_root"
  GITLAB_TOKEN="gitlab-token-for-test" \
  MINIMAX_API_KEY="minimax-token-for-test" \
  bash scripts/init-local-env.sh --output "$output_file" >/dev/null 2>&1
); then
  echo "expected init-local-env.sh to refuse overwriting an existing file without --force" >&2
  exit 1
fi
