# Findings

- `internal/gate/service.go` currently derives discussion resolution state from `review_findings.evidence` and `review_findings.body_markdown`.
- `internal/writer/writer.go` currently derives range anchor types from the first non-empty line of `review_findings.evidence`.
- `gitlab_discussions` already has a structured `resolved` column and is updated by writer resolution flow.
- `internal/writer.Writer.Write` processes findings strictly sequentially.
- `internal/scheduler.Service.RunOnce` claims and processes a single run at a time per worker process.
- `internal/rules/loader.go` prompt injection detection currently matches only 5 literal substrings.
- `internal/llm/provider.go` is 2334 lines and mixes provider transport, orchestration, parsing, persistence, and audit concerns.
