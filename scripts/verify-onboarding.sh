#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$repo_root"

fail() {
  echo "verify-onboarding: $*" >&2
  exit 1
}

require_pattern() {
  local file="$1"
  local pattern="$2"
  local description="$3"
  if ! rg -q --multiline "$pattern" "$file"; then
    fail "$description"
  fi
}

forbid_pattern() {
  local file="$1"
  local pattern="$2"
  local description="$3"
  if rg -q --multiline "$pattern" "$file"; then
    fail "$description"
  fi
}

# No-git production path must support direct Anthropic-compatible env configuration.
for file in docker-compose.prod.yaml .env.prod.example; do
  [[ -f "$file" ]] || fail "missing required file: $file"
done
git ls-files --error-unmatch .env.prod.example >/dev/null 2>&1 || fail ".env.prod.example must be tracked in git"
require_pattern "docker-compose.prod.yaml" "ANTHROPIC_BASE_URL" "docker-compose.prod.yaml must expose ANTHROPIC_BASE_URL"
require_pattern "docker-compose.prod.yaml" "ANTHROPIC_API_KEY" "docker-compose.prod.yaml must expose ANTHROPIC_API_KEY"
require_pattern "docker-compose.prod.yaml" "ANTHROPIC_MODEL" "docker-compose.prod.yaml must expose ANTHROPIC_MODEL"
require_pattern "docker-compose.prod.yaml" 'MINIMAX_API_KEY: \$\{MINIMAX_API_KEY:-\}' "docker-compose.prod.yaml must make MINIMAX_API_KEY optional for non-MiniMax providers"
require_pattern ".env.prod.example" "^ANTHROPIC_BASE_URL=" ".env.prod.example must document ANTHROPIC_BASE_URL"
require_pattern ".env.prod.example" "^ANTHROPIC_API_KEY=" ".env.prod.example must document ANTHROPIC_API_KEY"
require_pattern ".env.prod.example" "^ANTHROPIC_MODEL=" ".env.prod.example must document ANTHROPIC_MODEL"

# The documented no-git paths should be self-contained and consistently use docker compose.
require_pattern "README.md" "^### Method 1: Minimal Setup \\(No Git Required\\)" "README.md must keep a dedicated minimal setup section"
require_pattern "README.md" "docker compose -f docker-compose\\.prod\\.yaml up -d" "README.md must show how to start the minimal no-git path"
require_pattern "README.md" "^### Method 2: Advanced No-Git Setup" "README.md must present advanced no-git setup as Method 2"
forbid_pattern "README.md" "^### Method 1B:" "README.md must not use the Method 1B heading"
forbid_pattern "README.md" "docker-compose " "README.md must use docker compose consistently"
require_pattern "README.zh-CN.md" "^### 方式 1：极简部署（无需 Git）" "README.zh-CN.md must keep a dedicated minimal setup section"
require_pattern "README.zh-CN.md" "docker compose -f docker-compose\\.prod\\.yaml up -d" "README.zh-CN.md must show how to start the minimal no-git path"
require_pattern "README.zh-CN.md" "^### 方式 2：高级无 Git 部署" "README.zh-CN.md must present advanced no-git setup as 方式 2"
forbid_pattern "README.zh-CN.md" "^### 方式 1B：" "README.zh-CN.md must not use the 方式 1B heading"
forbid_pattern "README.zh-CN.md" "docker-compose " "README.zh-CN.md must use docker compose consistently"

# Advanced no-git setup must have a dedicated config override compose file.
[[ -f docker-compose.prod.config.yaml ]] || fail "missing docker-compose.prod.config.yaml for advanced config-based setup"
require_pattern "docker-compose.prod.config.yaml" "/app/config.yaml" "docker-compose.prod.config.yaml must mount config.yaml into /app/config.yaml"

# Developer compose must run local source, not prebuilt images.
require_pattern "docker-compose.yaml" "build:" "docker-compose.yaml must build local images for developer workflow"
if rg -q "container_name:" docker-compose.yaml docker-compose.prod.yaml; then
  fail "compose files must not hard-code container_name values"
fi

# Render the documented compose paths successfully.
tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

cp .env.example "$tmpdir/.env"
cp .env.prod.example "$tmpdir/.env.prod"
cp docker-compose.yaml "$tmpdir/docker-compose.yaml"
cp docker-compose.prod.yaml "$tmpdir/docker-compose.prod.yaml"
cp docker-compose.prod.config.yaml "$tmpdir/docker-compose.prod.config.yaml"
cat >"$tmpdir/config.yaml" <<'EOF'
llm:
  default_route: minimax-review
  fallback_route: minimax-review
  routes:
    minimax-review:
      provider: minimax
      base_url: https://api.minimaxi.com/anthropic
      api_key: ${MINIMAX_API_KEY}
      model: MiniMax-M2.7-highspeed
      output_mode: tool_call
      max_tokens: 4096
EOF

(
  cd "$tmpdir"
  docker compose config >/dev/null
)

(
  cd "$tmpdir"
  docker compose -f docker-compose.prod.yaml --env-file .env.prod config >/dev/null
)

(
  cd "$tmpdir"
  docker compose -f docker-compose.prod.yaml -f docker-compose.prod.config.yaml --env-file .env.prod config >/dev/null
)
