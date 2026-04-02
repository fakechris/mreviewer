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
    rg -q --multiline -- "$pattern" "$file"
    return
  fi
  grep -Eq -- "$pattern" "$file"
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

for file in README.md README.zh-CN.md WEBHOOK.md DEPLOYMENT.md docker-compose.yaml docker-compose.prod.yaml docker-compose.prod.config.yaml Dockerfile .github/workflows/ci.yml .github/workflows/release.yml scripts/install.sh scripts/install_test.sh scripts/render-homebrew-formula.sh; do
  [[ -f "$file" ]] || fail "missing required file: $file"
done

[[ -x scripts/install.sh ]] || fail "scripts/install.sh must be executable"
[[ -x scripts/install_test.sh ]] || fail "scripts/install_test.sh must be executable"
[[ -x scripts/render-homebrew-formula.sh ]] || fail "scripts/render-homebrew-formula.sh must be executable"

# Personal CLI docs must be binary-first and docker-optional.
require_pattern "README.md" "^## Personal CLI Quick Start" "README.md must document a Personal CLI quick start"
require_pattern "README.md" "curl -fsSL https://raw.githubusercontent.com/fakechris/mreviewer/main/scripts/install.sh \\| bash" "README.md must document the installer script"
require_pattern "README.md" "brew install ./mreviewer.rb" "README.md must mention the Homebrew formula path"
require_pattern "README.md" "mreviewer init" "README.md must document mreviewer init"
require_pattern "README.md" "mreviewer doctor" "README.md must document mreviewer doctor"
require_pattern "README.md" "mreviewer review" "README.md must document mreviewer review"
require_pattern "README.md" "mreviewer serve" "README.md must document mreviewer serve"
require_pattern "README.md" "SQLite" "README.md must document SQLite for personal mode"
require_pattern "README.zh-CN.md" "^## 个人 CLI 快速开始" "README.zh-CN.md must document a personal CLI quick start"
require_pattern "README.zh-CN.md" "curl -fsSL https://raw.githubusercontent.com/fakechris/mreviewer/main/scripts/install.sh \\| bash" "README.zh-CN.md must document the installer script"
require_pattern "README.zh-CN.md" "brew install ./mreviewer.rb" "README.zh-CN.md must mention the Homebrew formula path"
require_pattern "README.zh-CN.md" "mreviewer init" "README.zh-CN.md must document mreviewer init"
require_pattern "README.zh-CN.md" "mreviewer doctor" "README.zh-CN.md must document mreviewer doctor"
require_pattern "README.zh-CN.md" "mreviewer review" "README.zh-CN.md must document mreviewer review"
require_pattern "README.zh-CN.md" "mreviewer serve" "README.zh-CN.md must document mreviewer serve"
require_pattern "README.zh-CN.md" "SQLite" "README.zh-CN.md must document SQLite for personal mode"

# Enterprise docs must still describe Docker/webhook/admin deployment.
require_pattern "README.md" "^## Enterprise Webhook Deployment" "README.md must document the enterprise webhook path"
require_pattern "README.md" "docker compose -f docker-compose.prod.yaml up -d" "README.md must keep the production compose path"
require_pattern "README.md" "/admin/" "README.md must mention /admin/"
require_pattern "README.md" "/admin/api/runs" "README.md must mention run visibility"
require_pattern "README.md" "/admin/api/trends" "README.md must mention trends visibility"
require_pattern "README.md" "/admin/api/ownership" "README.md must mention ownership visibility"
require_pattern "README.md" "/admin/api/identities" "README.md must mention identity visibility"
require_pattern "README.zh-CN.md" "^## 企业 Webhook 部署" "README.zh-CN.md must document the enterprise webhook path"
require_pattern "README.zh-CN.md" "docker compose -f docker-compose.prod.yaml up -d" "README.zh-CN.md must keep the production compose path"
require_pattern "README.zh-CN.md" "/admin/" "README.zh-CN.md must mention /admin/"
require_pattern "DEPLOYMENT.md" "企业默认部署 / enterprise deployment" "DEPLOYMENT.md must remain enterprise-focused"
require_pattern "DEPLOYMENT.md" "/admin/api/runs" "DEPLOYMENT.md must mention run visibility"
require_pattern "DEPLOYMENT.md" "/admin/api/trends" "DEPLOYMENT.md must mention trend visibility"
require_pattern "DEPLOYMENT.md" "/admin/api/identities" "DEPLOYMENT.md must mention identity mapping visibility"

# Webhook and dashboard docs must cover the productized control plane.
require_pattern "WEBHOOK.md" "GitLab / GitHub Webhook" "WEBHOOK.md must mention both GitLab and GitHub webhooks"
require_pattern "WEBHOOK.md" "/github/webhook" "WEBHOOK.md must document the GitHub webhook path"
require_pattern "WEBHOOK.md" "latest-head-wins" "WEBHOOK.md must document latest-head-wins semantics"
require_pattern "WEBHOOK.md" "supersede|superseded" "WEBHOOK.md must mention superseded runs"
require_pattern "WEBHOOK.md" "/admin/api/runs" "WEBHOOK.md must mention run visibility"
require_pattern "WEBHOOK.md" "局域网 IP|LAN IP|localhost" "WEBHOOK.md must still explain local-network webhook addresses"
require_pattern "docs/operations/admin-dashboard.md" "/admin/api/trends" "admin dashboard doc must mention trends"
require_pattern "docs/operations/admin-dashboard.md" "/admin/api/ownership" "admin dashboard doc must mention ownership"
require_pattern "docs/operations/admin-dashboard.md" "/admin/api/identities" "admin dashboard doc must mention identities"
require_pattern "docs/operations/admin-dashboard.md" "retry" "admin dashboard doc must mention operator actions"

# Binary/release assets must exist.
require_pattern ".github/workflows/release.yml" "mreviewer_\\$\\{VERSION#v\\}_" "release workflow must build versioned CLI archives"
require_pattern ".github/workflows/release.yml" "render-homebrew-formula" "release workflow must render a Homebrew formula"
require_pattern ".github/workflows/ci.yml" "bash scripts/install_test.sh" "CI must run the installer script test"
require_pattern "scripts/install.sh" "releases/latest" "install.sh must resolve the latest GitHub release"
require_pattern "scripts/install.sh" "mreviewer_\\$\\{version#v\\}_" "install.sh must download versioned release archives"
require_pattern "scripts/render-homebrew-formula.sh" "class Mreviewer < Formula" "Homebrew formula renderer must emit a Formula"

# Existing container story must stay valid for enterprise users.
require_pattern "Dockerfile" "/out/mreviewer" "Dockerfile must build the mreviewer CLI"
require_pattern "Dockerfile" "/out/manual-trigger" "Dockerfile must build the manual-trigger CLI"
require_pattern "Dockerfile" "cmd/manual-trigger" "Dockerfile must compile cmd/manual-trigger"
require_pattern "Dockerfile" "cmd/mreviewer" "Dockerfile must compile cmd/mreviewer"
forbid_pattern "README.md" "docker-compose " "README.md must use docker compose consistently"
forbid_pattern "README.zh-CN.md" "docker-compose " "README.zh-CN.md must use docker compose consistently"
forbid_pattern "DEPLOYMENT.md" "docker-compose " "DEPLOYMENT.md must use docker compose consistently"

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

cp .env.example "$tmpdir/.env"
cp .env.prod.example "$tmpdir/.env.prod"
cp docker-compose.yaml "$tmpdir/docker-compose.yaml"
cp docker-compose.prod.yaml "$tmpdir/docker-compose.prod.yaml"
cp docker-compose.prod.config.yaml "$tmpdir/docker-compose.prod.config.yaml"
cat >"$tmpdir/config.yaml" <<'EOF'
models:
  openai_default:
    provider: openai
    base_url: https://api.openai.com/v1
    api_key: ${OPENAI_API_KEY}
    model: gpt-5.4
    output_mode: json_schema
    max_completion_tokens: 12000
    reasoning_effort: medium
model_chains:
  review_primary:
    primary: openai_default
    fallbacks: []
review:
  model_chain: review_primary
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
