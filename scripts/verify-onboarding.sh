#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$repo_root"

fail() {
  echo "verify-onboarding: $*" >&2
  exit 1
}

pattern_matches() {
  local pattern="$1"
  local file="$2"
  if command -v rg >/dev/null 2>&1; then
    rg -q --multiline "$pattern" "$file"
    return
  fi
  grep -Eq "$pattern" "$file"
}

require_pattern() {
  local file="$1"
  local pattern="$2"
  local description="$3"
  if ! pattern_matches "$pattern" "$file"; then
    fail "$description"
  fi
}

forbid_pattern() {
  local file="$1"
  local pattern="$2"
  local description="$3"
  if pattern_matches "$pattern" "$file"; then
    fail "$description"
  fi
}

# No-git production path must expose the provider-neutral quick-start contract.
for file in docker-compose.prod.yaml .env.prod.example; do
  [[ -f "$file" ]] || fail "missing required file: $file"
done
git ls-files --error-unmatch .env.prod.example >/dev/null 2>&1 || fail ".env.prod.example must be tracked in git"
require_pattern "docker-compose.prod.yaml" "LLM_PROVIDER" "docker-compose.prod.yaml must expose LLM_PROVIDER"
require_pattern "docker-compose.prod.yaml" "LLM_API_KEY" "docker-compose.prod.yaml must expose LLM_API_KEY"
require_pattern "docker-compose.prod.yaml" "LLM_BASE_URL" "docker-compose.prod.yaml must expose LLM_BASE_URL"
require_pattern "docker-compose.prod.yaml" "LLM_MODEL" "docker-compose.prod.yaml must expose LLM_MODEL"
require_pattern ".env.prod.example" "^LLM_PROVIDER=" ".env.prod.example must document LLM_PROVIDER"
require_pattern ".env.prod.example" "^LLM_API_KEY=" ".env.prod.example must document LLM_API_KEY"
require_pattern ".env.prod.example" "^LLM_BASE_URL=" ".env.prod.example must document LLM_BASE_URL"
require_pattern ".env.prod.example" "^LLM_MODEL=" ".env.prod.example must document LLM_MODEL"

# The documented no-git paths should present three equal quick starts and use docker compose consistently.
require_pattern "README.md" "^### Method 1: Minimal Setup \\(No Git Required\\)" "README.md must keep a dedicated minimal setup section"
require_pattern "README.md" "^#### Method 1A: MiniMax" "README.md must document MiniMax as Method 1A"
require_pattern "README.md" "^#### Method 1B: Anthropic" "README.md must document Anthropic as Method 1B"
require_pattern "README.md" "^#### Method 1C: ChatGPT / OpenAI" "README.md must document ChatGPT/OpenAI as Method 1C"
require_pattern "README.md" "docker compose -f docker-compose\\.prod\\.yaml up -d" "README.md must show how to start the minimal no-git path"
require_pattern "README.md" "^### Method 2: Advanced No-Git Setup" "README.md must present advanced no-git setup as Method 2"
require_pattern "README.md" "^### Method 3: Full Clone \\(For Developers\\)" "README.md must renumber the developer workflow to Method 3"
forbid_pattern "README.md" "^\\*\\*Option A:" "README.md must not describe quick starts as Option A"
forbid_pattern "README.md" "^\\*\\*Option B:" "README.md must not describe quick starts as Option B"
forbid_pattern "README.md" "docker-compose " "README.md must use docker compose consistently"
require_pattern "README.zh-CN.md" "^### 方式 1：极简部署（无需 Git）" "README.zh-CN.md must keep a dedicated minimal setup section"
require_pattern "README.zh-CN.md" "^#### 方式 1A：MiniMax" "README.zh-CN.md must document MiniMax as 方式 1A"
require_pattern "README.zh-CN.md" "^#### 方式 1B：Anthropic" "README.zh-CN.md must document Anthropic as 方式 1B"
require_pattern "README.zh-CN.md" "^#### 方式 1C：ChatGPT / OpenAI" "README.zh-CN.md must document ChatGPT/OpenAI as 方式 1C"
require_pattern "README.zh-CN.md" "docker compose -f docker-compose\\.prod\\.yaml up -d" "README.zh-CN.md must show how to start the minimal no-git path"
require_pattern "README.zh-CN.md" "^### 方式 2：高级无 Git 部署" "README.zh-CN.md must present advanced no-git setup as 方式 2"
require_pattern "README.zh-CN.md" "^### 方式 3：完整克隆（开发者）" "README.zh-CN.md must renumber the developer workflow to 方式 3"
forbid_pattern "README.zh-CN.md" "^\\*\\*选项 A:" "README.zh-CN.md must not describe quick starts as 选项 A"
forbid_pattern "README.zh-CN.md" "^\\*\\*选项 B:" "README.zh-CN.md must not describe quick starts as 选项 B"
forbid_pattern "README.zh-CN.md" "docker-compose " "README.zh-CN.md must use docker compose consistently"
forbid_pattern "DEPLOYMENT.md" "docker-compose " "DEPLOYMENT.md must use docker compose consistently"

# Enterprise docs must describe queue semantics and the admin control plane.
require_pattern "README.md" "/admin/" "README.md must mention the admin page"
require_pattern "README.md" "MySQL" "README.md must describe MySQL as part of the deployment story"
require_pattern "README.zh-CN.md" "/admin/" "README.zh-CN.md must mention the admin page"
require_pattern "README.zh-CN.md" "MySQL" "README.zh-CN.md must describe MySQL as part of the deployment story"
require_pattern "DEPLOYMENT.md" "enterprise" "DEPLOYMENT.md must document enterprise deployment guidance"
require_pattern "DEPLOYMENT.md" "MREVIEWER_ADMIN_TOKEN|ADMIN_TOKEN" "DEPLOYMENT.md must document admin auth token setup"
require_pattern "DEPLOYMENT.md" "/admin/" "DEPLOYMENT.md must mention the admin page"
require_pattern "WEBHOOK.md" "latest head wins|latest-head-wins" "WEBHOOK.md must document latest-head-wins queue semantics"
require_pattern "WEBHOOK.md" "supersede|superseded" "WEBHOOK.md must explain superseded runs"
require_pattern "WEBHOOK.md" "/admin/api/queue" "WEBHOOK.md must mention admin queue visibility"

for file in docs/architecture/enterprise-webhook.md docs/operations/admin-dashboard.md docs/operations/failure-playbook.md; do
  [[ -f "$file" ]] || fail "missing required enterprise doc: $file"
done
require_pattern "docs/architecture/enterprise-webhook.md" "latest head wins|latest-head-wins" "enterprise architecture doc must describe latest-head-wins"
require_pattern "docs/operations/admin-dashboard.md" "/admin/api/queue" "admin dashboard doc must mention queue endpoint"
require_pattern "docs/operations/admin-dashboard.md" "Bearer" "admin dashboard doc must describe bearer auth"
require_pattern "docs/operations/failure-playbook.md" "superseded_by_new_head" "failure playbook must document superseded runs"
require_pattern "docs/operations/failure-playbook.md" "provider_failed|worker_timeout" "failure playbook must document provider and worker failures"

# Advanced no-git setup must have a dedicated config override compose file.
[[ -f docker-compose.prod.config.yaml ]] || fail "missing docker-compose.prod.config.yaml for advanced config-based setup"
require_pattern "docker-compose.prod.config.yaml" "/app/config.yaml" "docker-compose.prod.config.yaml must mount config.yaml into /app/config.yaml"

# Developer compose must run local source, not prebuilt images.
require_pattern "docker-compose.yaml" "build:" "docker-compose.yaml must build local images for developer workflow"
if pattern_matches "container_name:" docker-compose.yaml || pattern_matches "container_name:" docker-compose.prod.yaml; then
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
