#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: scripts/show-run-audit.sh [--latest] [run-id]

Prints the stored review_run row and full audit_logs.detail payloads for a run.
By default this reads from the local Docker Compose MySQL container.
Useful for investigating provider_failed, worker_timeout, or superseded_by_new_head runs.
EOF
}

latest="false"
run_id=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --latest)
      latest="true"
      shift
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
      if [[ -n "$run_id" ]]; then
        echo "run id provided more than once" >&2
        usage >&2
        exit 2
      fi
      run_id="$1"
      shift
      ;;
  esac
done

if [[ "$latest" == "true" && -n "$run_id" ]]; then
  echo "use either --latest or a run id, not both" >&2
  exit 2
fi

if [[ "$latest" != "true" && -z "$run_id" ]]; then
  usage >&2
  exit 2
fi

mysql_exec() {
  docker exec -i mreviewer-mysql mysql --default-character-set=utf8mb4 -umreviewer -pmreviewer_password mreviewer "$@"
}

if [[ "$latest" == "true" ]]; then
  run_id="$(
    mysql_exec -Nse "SELECT id FROM review_runs ORDER BY id DESC LIMIT 1"
  )"
fi

if [[ -z "$run_id" ]]; then
  echo "unable to resolve run id" >&2
  exit 1
fi

cat <<EOF
=== review_runs ($run_id) ===
EOF

mysql_exec -e "
SELECT
  id,
  status,
  trigger_type,
  head_sha,
  superseded_by_run_id,
  claimed_by,
  claimed_at,
  error_code,
  error_detail,
  provider_latency_ms,
  provider_tokens_total,
  JSON_PRETTY(scope_json) AS scope_json,
  created_at,
  updated_at
FROM review_runs
WHERE id = ${run_id};
"

cat <<EOF

=== audit_logs ($run_id) ===
EOF

mysql_exec -e "
SELECT
  id,
  action,
  error_code,
  JSON_PRETTY(detail) AS detail,
  created_at
FROM audit_logs
WHERE entity_type = 'review_run' AND entity_id = ${run_id}
ORDER BY id;
"
