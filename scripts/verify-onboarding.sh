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

multiline_pattern_matches() {
  local pattern="$1"
  local file="$2"
  PATTERN="$pattern" perl -0ne 'my $pattern = $ENV{PATTERN}; exit(!(/$pattern/s));' "$file"
}

forbid_pattern() {
  local file="$1"
  local pattern="$2"
  local description="$3"
  if pattern_matches "$pattern" "$file"; then
    fail "$description"
  fi
}

require_multiline_pattern() {
  local file="$1"
  local pattern="$2"
  local description="$3"
  if ! multiline_pattern_matches "$pattern" "$file"; then
    fail "$description"
  fi
}

for file in README.md README.zh-CN.md WEBHOOK.md DEPLOYMENT.md docker-compose.yaml docker-compose.prod.yaml docker-compose.prod.config.yaml Dockerfile .github/workflows/ci.yml .github/workflows/release.yml scripts/install.sh scripts/install_test.sh scripts/render-homebrew-formula.sh scripts/release_test.sh; do
  [[ -f "$file" ]] || fail "missing required file: $file"
done

[[ -x scripts/install.sh ]] || fail "scripts/install.sh must be executable"
[[ -x scripts/install_test.sh ]] || fail "scripts/install_test.sh must be executable"
[[ -x scripts/render-homebrew-formula.sh ]] || fail "scripts/render-homebrew-formula.sh must be executable"
[[ -x scripts/release_test.sh ]] || fail "scripts/release_test.sh must be executable"

# Personal CLI docs must be binary-first and docker-optional.
require_pattern "README.md" "^## Personal CLI Quick Start" "README.md must document a Personal CLI quick start"
require_pattern "README.md" "curl -fsSL https://raw.githubusercontent.com/fakechris/mreviewer/main/scripts/install.sh \\| bash" "README.md must document the installer script"
require_pattern "README.md" "brew tap fakechris/mreviewer https://github.com/fakechris/mreviewer" "README.md must document the Homebrew tap with the explicit repo URL"
require_pattern "README.md" "brew install mreviewer" "README.md must document brew install mreviewer"
require_pattern "README.md" "mreviewer init" "README.md must document mreviewer init"
require_pattern "README.md" "mreviewer doctor" "README.md must document mreviewer doctor"
require_pattern "README.md" "mreviewer review" "README.md must document mreviewer review"
require_pattern "README.md" "mreviewer serve" "README.md must document mreviewer serve"
require_pattern "README.md" "SQLite" "README.md must document SQLite for personal mode"
require_pattern "README.zh-CN.md" "^## 个人 CLI 快速开始" "README.zh-CN.md must document a personal CLI quick start"
require_pattern "README.zh-CN.md" "curl -fsSL https://raw.githubusercontent.com/fakechris/mreviewer/main/scripts/install.sh \\| bash" "README.zh-CN.md must document the installer script"
require_pattern "README.zh-CN.md" "brew tap fakechris/mreviewer https://github.com/fakechris/mreviewer" "README.zh-CN.md must document the Homebrew tap with the explicit repo URL"
require_pattern "README.zh-CN.md" "brew install mreviewer" "README.zh-CN.md must document brew install mreviewer"
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
require_pattern "DEPLOYMENT.md" "models" "DEPLOYMENT.md must document models-based configuration"
require_pattern "DEPLOYMENT.md" "model_chains" "DEPLOYMENT.md must document model_chains-based configuration"

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
require_pattern ".github/workflows/release.yml" "checksums\\.txt" "release workflow must publish a consolidated checksums.txt"
require_pattern ".github/workflows/release.yml" 'archive%\.tar\.gz' "release workflow must derive consolidated checksums from the published .sha256 asset names"
require_pattern ".github/workflows/release.yml" "Formula/mreviewer\\.rb" "release workflow must update the checked-in Homebrew formula"
require_pattern ".github/workflows/release.yml" "release/formula-\\$\\{VERSION\\}" "release workflow must sync the generated formula through a dedicated branch"
require_pattern ".github/workflows/release.yml" "gh pr create" "release workflow must open a PR for formula sync instead of pushing main directly"
require_pattern ".github/workflows/release.yml" "workflow_dispatch:" "release workflow must support manual dispatch publishing"
require_pattern ".github/workflows/release.yml" 'git fetch origin main .*"\$branch"' "release workflow must fetch the existing formula sync branch so reruns can reuse it safely"
require_multiline_pattern ".github/workflows/release.yml" 'if \[\[ -z "\$\(git status --porcelain -- Formula/mreviewer\.rb\)" \]\]; then\s+echo "branch=\$branch" >> "\$GITHUB_OUTPUT"\s+exit 0' "release workflow must still expose the formula sync branch when reruns find no diff"
require_pattern ".github/workflows/ci.yml" "bash scripts/install_test.sh" "CI must run the installer script test"
require_pattern ".github/workflows/ci.yml" "bash scripts/release_test.sh" "CI must run the release distribution test"
require_pattern "scripts/install.sh" "releases/latest" "install.sh must resolve the latest GitHub release"
require_pattern "scripts/install.sh" "mreviewer_\\$\\{version#v\\}_" "install.sh must download versioned release archives"
require_pattern "scripts/install.sh" "checksum_url=" "install.sh must expose the checksum release asset"
require_pattern "scripts/render-homebrew-formula.sh" "class Mreviewer < Formula" "Homebrew formula renderer must emit a Formula"

# Existing container story must stay valid for enterprise users.
require_pattern "Dockerfile" "/out/mreviewer" "Dockerfile must build the mreviewer CLI"
require_pattern "Dockerfile" "/out/manual-trigger" "Dockerfile must build the manual-trigger CLI"
require_pattern "Dockerfile" "cmd/manual-trigger" "Dockerfile must compile cmd/manual-trigger"
require_pattern "Dockerfile" "cmd/mreviewer" "Dockerfile must compile cmd/mreviewer"
require_pattern "config.yaml" "^models:" "config.yaml must use the model catalog schema"
require_pattern "config.yaml" "^model_chains:" "config.yaml must define model_chains"
require_pattern "config.yaml" "^review:" "config.yaml must define review bindings"
forbid_pattern "config.yaml" "^llm_provider:" "config.yaml must not use legacy llm_provider"
forbid_pattern "config.yaml" "^anthropic_base_url:" "config.yaml must not use legacy anthropic fields"
forbid_pattern ".env.prod.example" "^LLM_PROVIDER=" ".env.prod.example must not use legacy single-provider envs"
forbid_pattern ".env.prod.example" "^LLM_API_KEY=" ".env.prod.example must not use legacy single-provider envs"
forbid_pattern ".env.prod.example" "^LLM_BASE_URL=" ".env.prod.example must not use legacy single-provider envs"
forbid_pattern ".env.prod.example" "^LLM_MODEL=" ".env.prod.example must not use legacy single-provider envs"
forbid_pattern ".env.prod.example" "^ANTHROPIC_MODEL=" ".env.prod.example must not use legacy anthropic model envs"
forbid_pattern "docker-compose.prod.yaml" "LLM_PROVIDER:" "docker-compose.prod.yaml must not pass legacy LLM_PROVIDER envs"
forbid_pattern "docker-compose.prod.yaml" "LLM_API_KEY:" "docker-compose.prod.yaml must not pass legacy LLM_API_KEY envs"
forbid_pattern "docker-compose.prod.yaml" "LLM_BASE_URL:" "docker-compose.prod.yaml must not pass legacy LLM_BASE_URL envs"
forbid_pattern "docker-compose.prod.yaml" "LLM_MODEL:" "docker-compose.prod.yaml must not pass legacy LLM_MODEL envs"
forbid_pattern "docker-compose.prod.yaml" "ANTHROPIC_MODEL:" "docker-compose.prod.yaml must not pass legacy ANTHROPIC_MODEL envs"
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
