#!/usr/bin/env bash
set -euo pipefail

container_name="mreviewer-test-mysql-$$"
dry_run="false"
go_test_args=()

usage() {
  cat <<'EOF'
Usage: scripts/test-with-shared-mysql.sh [--dry-run] [go test args...]

Starts one shared MySQL 8.4 container for the whole test run, exports
MREVIEWER_TEST_ADMIN_DSN for dbtest, runs go test, then cleans up the container.

Examples:
  scripts/test-with-shared-mysql.sh
  scripts/test-with-shared-mysql.sh ./...
  scripts/test-with-shared-mysql.sh ./internal/llm -run TestProcessRunUsesDynamicSystemPrompt
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --dry-run)
      dry_run="true"
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      go_test_args+=("$1")
      shift
      ;;
  esac
done

if [[ ${#go_test_args[@]} -eq 0 ]]; then
  go_test_args=(./...)
fi

if [[ "$dry_run" == "true" ]]; then
  printf 'ADMIN_DSN_ENV_VAR=%s\n' "MREVIEWER_TEST_ADMIN_DSN"
  printf 'CONTAINER_NAME=%s\n' "$container_name"
  printf 'GO_TEST_ARGS='
  printf '%q ' "${go_test_args[@]}"
  printf '\n'
  exit 0
fi

cleanup() {
  docker rm -f "$container_name" >/dev/null 2>&1 || true
}

trap cleanup EXIT

docker run -d --rm \
  --name "$container_name" \
  -e MYSQL_ROOT_PASSWORD=test \
  -e MYSQL_ROOT_HOST=% \
  -e MYSQL_DATABASE=mysql \
  -p 127.0.0.1::3306 \
  mysql:8.4 >/dev/null

for _ in $(seq 1 60); do
  if docker exec "$container_name" mysqladmin ping -ptest --silent >/dev/null 2>&1; then
    break
  fi
  sleep 1
done

if ! docker exec "$container_name" mysqladmin ping -ptest --silent >/dev/null 2>&1; then
  echo "shared mysql did not become ready in time" >&2
  exit 1
fi

host_port="$(docker port "$container_name" 3306/tcp | awk -F: 'END {print $NF}')"
if [[ -z "$host_port" ]]; then
  echo "failed to resolve mapped MySQL port for $container_name" >&2
  exit 1
fi

export MREVIEWER_TEST_ADMIN_DSN="root:test@tcp(127.0.0.1:${host_port})/mysql?parseTime=true&loc=UTC&charset=utf8mb4&collation=utf8mb4_unicode_ci&multiStatements=true"

go test "${go_test_args[@]}"
