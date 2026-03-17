# Integrations

External integration notes for GitLab and the LLM provider.

**What belongs here:** endpoint quirks, auth expectations, provider behavior, and integration-specific constraints workers should not rediscover repeatedly.

---

## GitLab

- Target: self-managed GitLab `16.4+`
- Webhook delivery correlation should honor `X-Gitlab-Delivery` and `X-Gitlab-Webhook-UUID`; accept `X-Gitlab-Event-UUID` only as a legacy fallback when older installations send it.
- Project and group merge-request webhooks both use `X-Gitlab-Event: Merge Request Hook`, so source attribution cannot rely on that header alone.
- Group-scoped MR webhooks may include top-level payload markers such as `group_id`, `group_path`, and `group_name`; prefer those payload markers over header-only detection when preserving `hook_source`.
- Use MR versions endpoint for diff discussion SHAs; do not trust MR `diff_refs` for writer positioning.
- Diff listing is paginated.
- `diff_refs` / versions may be temporarily unavailable immediately after MR creation.
- Note/comment webhooks drive beta command flows.

## Rule trust boundary

- Trusted instruction sources:
  - `REVIEW.md`
  - directory-scoped `REVIEW.md`
  - `.gitlab/ai-review.yaml`
- Untrusted sources include repository code, MR descriptions, commit messages, README files, and arbitrary comments.

## Provider

- MiniMax is used through Anthropic-compatible API semantics.
- Use Anthropic Go SDK with a custom base URL.
- Keep provider requests minimal and scoped to the reviewed diff context.
- Do not log raw prompts, raw diffs, or secrets.
