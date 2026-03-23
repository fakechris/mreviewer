#!/usr/bin/env bash
set -euo pipefail

env_file=".env"
dry_run="false"
llm_route=""

usage() {
  cat <<'EOF'
Usage: scripts/review-mr.sh [--env-file PATH] [--dry-run] [--llm-route ROUTE] <gitlab-mr-url>

Starts the local Docker Compose stack if needed, resolves the GitLab project id,
and triggers a manual review run for the given merge request.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --env-file)
      env_file="${2:?missing value for --env-file}"
      shift 2
      ;;
    --dry-run)
      dry_run="true"
      shift
      ;;
    --llm-route)
      llm_route="${2:?missing value for --llm-route}"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    --*)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
    *)
      break
      ;;
  esac
done

if [[ $# -ne 1 ]]; then
  usage >&2
  exit 2
fi

mr_url="$1"

if [[ ! -f "$env_file" ]]; then
  echo "env file not found: $env_file" >&2
  exit 1
fi

read_env() {
  python3 - "$env_file" "$1" <<'PY'
from pathlib import Path
import sys

env_path = Path(sys.argv[1])
target = sys.argv[2]
for raw in env_path.read_text().splitlines():
    line = raw.strip()
    if not line or line.startswith('#') or '=' not in line:
        continue
    key, value = line.split('=', 1)
    if key == target:
        print(value)
        sys.exit(0)
sys.exit(1)
PY
}

gitlab_base_url="$(read_env GITLAB_BASE_URL)"
gitlab_token="$(read_env GITLAB_TOKEN)"
gitlab_webhook_secret="$(read_env GITLAB_WEBHOOK_SECRET)"

if [[ -z "$gitlab_base_url" || -z "$gitlab_token" ]]; then
  echo "GITLAB_BASE_URL and GITLAB_TOKEN are required in $env_file" >&2
  exit 1
fi

parse_output="$(
python3 - "$mr_url" <<'PY'
import re
import sys
from urllib.parse import quote, urlparse

url = sys.argv[1]
parsed = urlparse(url)
match = re.match(r"^/(?P<path>.+)/-/merge_requests/(?P<iid>\d+)$", parsed.path)
if not match:
    raise SystemExit("unsupported GitLab MR URL: " + url)
mr_path = match.group("path").strip("/")
mr_iid = match.group("iid")
print(parsed.scheme + "://" + parsed.netloc)
print(mr_path)
print(mr_iid)
print(quote(mr_path, safe=""))
PY
)"

mr_base_url="$(printf '%s\n' "$parse_output" | sed -n '1p')"
mr_path="$(printf '%s\n' "$parse_output" | sed -n '2p')"
mr_iid="$(printf '%s\n' "$parse_output" | sed -n '3p')"
project_lookup_path="$(printf '%s\n' "$parse_output" | sed -n '4p')"

if [[ "$mr_base_url" != "$gitlab_base_url" ]]; then
  echo "MR URL host $mr_base_url does not match GITLAB_BASE_URL $gitlab_base_url" >&2
  exit 1
fi

compose_up_cmd="docker compose up -d --build mysql redis migrate worker"

if [[ "$dry_run" == "true" ]]; then
  cat <<EOF
MR_PATH=$mr_path
MR_IID=$mr_iid
GITLAB_BASE_URL=$gitlab_base_url
PROJECT_LOOKUP_PATH=$project_lookup_path
COMPOSE_UP=$compose_up_cmd
TRIGGER_MODE=docker-run-manual-trigger
LLM_ROUTE=$llm_route
EOF
  exit 0
fi

eval "$compose_up_cmd"

project_id="$(
python3 - "$gitlab_base_url" "$gitlab_token" "$project_lookup_path" <<'PY'
import json
import sys
from urllib import request

base_url, token, encoded_path = sys.argv[1:4]
url = base_url.rstrip('/') + '/api/v4/projects/' + encoded_path
req = request.Request(url, headers={'PRIVATE-TOKEN': token})
with request.urlopen(req, timeout=30) as resp:
    data = json.load(resp)
print(data['id'])
PY
)"

compose_network="${COMPOSE_PROJECT_NAME:-$(basename "$PWD")}_default"

manual_trigger_args=(
  --project-id "$project_id"
  --mr-iid "$mr_iid"
  --wait
  --json
)

if [[ -n "$llm_route" ]]; then
  manual_trigger_args+=(--llm-route "$llm_route")
fi

docker run --rm \
  --network "$compose_network" \
  -v "$PWD":/src \
  -w /src \
  -e GITLAB_BASE_URL="$gitlab_base_url" \
  -e GITLAB_TOKEN="$gitlab_token" \
  -e GITLAB_WEBHOOK_SECRET="$gitlab_webhook_secret" \
  -e MYSQL_DSN="mreviewer:mreviewer_password@tcp(mysql:3306)/mreviewer?parseTime=true&loc=UTC&charset=utf8mb4&collation=utf8mb4_unicode_ci" \
  -e REDIS_ADDR="redis:6379" \
  golang:1.25 \
  go run ./cmd/manual-trigger "${manual_trigger_args[@]}"
