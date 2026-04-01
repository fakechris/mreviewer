#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

assert_contains() {
  local file="$1"
  local pattern="$2"
  if ! grep -Eq -- "$pattern" "$file"; then
    echo "missing pattern in $file: $pattern" >&2
    exit 1
  fi
}

assert_contains "Dockerfile" "/out/manual-trigger"
assert_contains "Dockerfile" "/out/mreviewer"
assert_contains "Dockerfile" "/app/manual-trigger"
assert_contains "Dockerfile" "/app/mreviewer"

assert_contains "README.md" "GITHUB_BASE_URL"
assert_contains "README.md" "GITHUB_TOKEN"
assert_contains "README.md" "GITHUB_WEBHOOK_SECRET"
assert_contains "README.md" "/github/webhook"
assert_contains "README.md" "/app/mreviewer"
assert_contains "README.md" "WEBHOOK.md"
assert_contains "README.md" "--exit-mode requested_changes"
assert_contains "README.md" "--advisor-route"
assert_contains "README.md" "REVIEW_PACKS"
assert_contains "README.md" "REVIEW_ADVISOR_ROUTE"
assert_contains "README.md" "REVIEW_COMPARE_REVIEWERS"
assert_contains "README.md" "https://github.com/.*/pull/"
assert_contains "README.md" "https://gitlab.example.com/.*/-/merge_requests/"
assert_contains "WEBHOOK.md" "/webhook"
assert_contains "WEBHOOK.md" "/github/webhook"
assert_contains "WEBHOOK.md" "GITHUB_WEBHOOK_SECRET"
assert_contains "WEBHOOK.md" "GITLAB_WEBHOOK_SECRET"
assert_contains ".env.example" "GITHUB_BASE_URL="
assert_contains ".env.example" "GITHUB_TOKEN="
assert_contains ".env.example" "GITHUB_WEBHOOK_SECRET="
assert_contains ".env.example" "REVIEW_PACKS="
assert_contains ".env.example" "REVIEW_ADVISOR_ROUTE="
assert_contains ".env.example" "REVIEW_COMPARE_REVIEWERS="
assert_contains ".github/workflows/review.yml.example" "/app/mreviewer"
assert_contains ".github/workflows/review.yml.example" "--exit-mode requested_changes"
assert_contains "docs/architecture/portable-review-council.md" "ReviewBundle"
assert_contains "docs/operations/reviewer-comparison.md" "agreement rate"
assert_contains "docs/operations/github-runtime.md" "/github/webhook"
assert_contains "docs/operations/gitlab-runtime.md" "/webhook"
assert_contains "docs/operations/github-runtime.md" "REVIEW_ADVISOR_ROUTE"
assert_contains "docs/operations/gitlab-runtime.md" "REVIEW_COMPARE_REVIEWERS"
