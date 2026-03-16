#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN_DIR="$ROOT_DIR/.factory/bin"

mkdir -p "$BIN_DIR"
export GOBIN="$BIN_DIR"

if [[ ! -f "$ROOT_DIR/.env.example" ]]; then
  cat > "$ROOT_DIR/.env.example" <<'EOF'
APP_ENV=development
PORT=3100
MYSQL_DSN=mreviewer:mreviewer_password@tcp(127.0.0.1:3306)/mreviewer?parseTime=true&loc=UTC&charset=utf8mb4&collation=utf8mb4_unicode_ci
REDIS_ADDR=127.0.0.1:6380
GITLAB_BASE_URL=https://gitlab.example.com
GITLAB_TOKEN=replace-me
GITLAB_WEBHOOK_SECRET=replace-me
ANTHROPIC_BASE_URL=https://api.minimaxi.com/anthropic
ANTHROPIC_API_KEY=replace-me
ANTHROPIC_MODEL=MiniMax-M2.5
EOF
fi

if [[ ! -f "$ROOT_DIR/.env" ]]; then
  cp "$ROOT_DIR/.env.example" "$ROOT_DIR/.env"
fi

if command -v go >/dev/null 2>&1; then
  [[ -x "$BIN_DIR/sqlc" ]] || go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest
  [[ -x "$BIN_DIR/goose" ]] || go install github.com/pressly/goose/v3/cmd/goose@latest
  [[ -x "$BIN_DIR/golangci-lint" ]] || go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

  if [[ -f "$ROOT_DIR/go.mod" ]]; then
    (cd "$ROOT_DIR" && go mod download)
  fi
fi
