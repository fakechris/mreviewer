#!/usr/bin/env bash
set -euo pipefail

output_file=".env"
force="false"
gitlab_base_url="${GITLAB_BASE_URL:-https://gitlab.example.com}"
gitlab_webhook_secret="${GITLAB_WEBHOOK_SECRET:-}"
minimax_base_url="${MINIMAX_BASE_URL:-https://api.minimaxi.com/anthropic}"
minimax_model="${MINIMAX_MODEL:-MiniMax-M2.7-highspeed}"

usage() {
  cat <<'EOF'
Usage: scripts/init-local-env.sh [--output PATH] [--force] [--gitlab-base-url URL] [--gitlab-webhook-secret SECRET]

Generates a local .env file for the default Docker Compose deployment.

Required environment variables:
  GITLAB_TOKEN
  MINIMAX_API_KEY

Optional environment variables:
  GITLAB_BASE_URL
  GITLAB_WEBHOOK_SECRET
  MINIMAX_BASE_URL
  MINIMAX_MODEL
EOF
}

random_secret() {
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -hex 16
    return
  fi

  (
    set +o pipefail
    LC_ALL=C tr -dc 'A-Za-z0-9' </dev/urandom | head -c 32
  )
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --output)
      output_file="${2:?missing value for --output}"
      shift 2
      ;;
    --force)
      force="true"
      shift
      ;;
    --gitlab-base-url)
      gitlab_base_url="${2:?missing value for --gitlab-base-url}"
      shift 2
      ;;
    --gitlab-webhook-secret)
      gitlab_webhook_secret="${2:?missing value for --gitlab-webhook-secret}"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [[ -z "${GITLAB_TOKEN:-}" ]]; then
  echo "GITLAB_TOKEN is required" >&2
  exit 1
fi

if [[ -z "${MINIMAX_API_KEY:-}" ]]; then
  echo "MINIMAX_API_KEY is required" >&2
  exit 1
fi

if [[ -z "$gitlab_webhook_secret" ]]; then
  gitlab_webhook_secret="$(random_secret)"
fi

output_dir="$(dirname "$output_file")"
mkdir -p "$output_dir"

if [[ -e "$output_file" ]]; then
  if [[ "$force" != "true" ]]; then
    echo "$output_file already exists; rerun with --force to overwrite it" >&2
    exit 1
  fi
  backup_file="${output_file}.bak.$(date +%Y%m%d%H%M%S)"
  mv "$output_file" "$backup_file"
  echo "Backed up existing file to $backup_file" >&2
fi

umask 077

cat >"$output_file" <<EOF
APP_ENV=development
PORT=3100
MYSQL_DSN=mreviewer:mreviewer_password@tcp(127.0.0.1:3306)/mreviewer?parseTime=true&loc=UTC&charset=utf8mb4&collation=utf8mb4_unicode_ci
REDIS_ADDR=127.0.0.1:6380
GITLAB_BASE_URL=$gitlab_base_url
GITLAB_TOKEN=$GITLAB_TOKEN
GITLAB_WEBHOOK_SECRET=$gitlab_webhook_secret
MINIMAX_BASE_URL=$minimax_base_url
MINIMAX_API_KEY=$MINIMAX_API_KEY
MINIMAX_MODEL=$minimax_model
EOF

echo "Wrote $output_file" >&2
