#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

env_file="$tmpdir/.env"
cat >"$env_file" <<'EOF'
APP_ENV=development
PORT=3100
MYSQL_DSN=mreviewer:mreviewer_password@tcp(127.0.0.1:3306)/mreviewer?parseTime=true&loc=UTC&charset=utf8mb4&collation=utf8mb4_unicode_ci
REDIS_ADDR=127.0.0.1:6380
GITLAB_BASE_URL=https://github.91jinrong.com
GITLAB_TOKEN=gitlab-token-for-test
GITLAB_WEBHOOK_SECRET=test-webhook-secret
MINIMAX_BASE_URL=https://api.minimaxi.com/anthropic
MINIMAX_API_KEY=minimax-token-for-test
MINIMAX_MODEL=MiniMax-M2.7-highspeed
EOF

output="$(
  cd "$repo_root"
  bash scripts/review-mr.sh \
    --env-file "$env_file" \
    --dry-run \
    "https://github.91jinrong.com/songchuansheng/case-revenue-bridge/-/merge_requests/1"
)"

grep -q '^MR_PATH=songchuansheng/case-revenue-bridge$' <<<"$output"
grep -q '^MR_IID=1$' <<<"$output"
grep -q '^GITLAB_BASE_URL=https://github.91jinrong.com$' <<<"$output"
grep -q '^PROJECT_LOOKUP_PATH=songchuansheng%2Fcase-revenue-bridge$' <<<"$output"
grep -q '^COMPOSE_UP=docker compose up -d --build mysql redis migrate worker$' <<<"$output"
grep -q '^TRIGGER_MODE=docker-run-manual-trigger$' <<<"$output"
