# Integrations

External integration notes for GitLab and the LLM provider.

**What belongs here:** endpoint quirks, auth expectations, provider behavior, and integration-specific constraints workers should not rediscover repeatedly.

---

## GitLab

- Target: self-managed GitLab `16.4+`
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
