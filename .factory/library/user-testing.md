# User Testing

Validation surface and runtime testing guidance for this mission.

**What belongs here:** real validation surfaces, tools to use, setup notes, concurrency limits, and accepted limitations.

---

## Validation Surface

- Primary user-facing surface: HTTP API / webhook endpoints
- Validation tools:
  - `curl`
  - Go integration tests
  - mocked GitLab and provider endpoints via `httptest.Server`
- No browser or TUI validation is required for this mission.

## Validation Strategy

- Validate ingress behavior with `curl` against local endpoints.
- Validate GitLab/provider behavior with contract and integration tests against mocks by default.
- Live GitLab validation is deferred until the user provides real instance details and credentials.
- Merge-gate behaviors should be validated with mocked GitLab discussion/status APIs.

## Validation Concurrency

- Machine profile observed during dry run:
  - 32 GB RAM
  - 10 CPU cores
  - ~10 GB effective available headroom at planning time
- Surface classification:
  - `curl` API validation: lightweight
  - Go integration tests with Docker-backed MySQL: moderate
- Max concurrent validators: **5**
- Rationale: use conservative parallelism so MySQL containers, Go test processes, and mock servers stay stable under repeated validation.

## Known limitation

- Until live GitLab details are configured, validators should treat mock-backed integration as the authoritative validation path.

## Flow Validator Guidance: curl

Testing surface: HTTP API endpoints via curl against localhost:3100.

### Isolation rules
- The API service runs on port 3100 with MySQL on port 3306 and Redis on port 6380.
- Multiple curl-based validators can run concurrently against the same service.
- Do NOT run goose down/up while the API service is running — it will break health checks and other endpoint tests.
- For shutdown validation, run a compiled binary from `.factory/run/ingress` rather than `go run`, because `go run` does not reliably forward SIGTERM to the child Go process in this environment.
- Use `curl -sf` for simple checks and `curl -v` when headers/status codes matter.
- All log output from the service goes to /tmp/mreviewer-api.log.

### Service details
- Health endpoint: GET http://127.0.0.1:3100/health → 200 {"status":"ok"}
- MySQL DSN: mreviewer:mreviewer_password@tcp(127.0.0.1:3306)/mreviewer?parseTime=true
- Config file: config.yaml in repo root
- Binary: cmd/ingress/main.go

## Flow Validator Guidance: cli

Testing surface: CLI commands (goose, sqlc, go build) run locally.

### Isolation rules
- goose migration commands are destructive (drop/create tables) — do NOT run concurrently with API or other DB-dependent validators.
- sqlc generate and go build are read-only and safe to run concurrently.
- Goose binary: $(go env GOPATH)/bin/goose
- Goose DSN: mreviewer:mreviewer_password@tcp(127.0.0.1:3306)/mreviewer?parseTime=true
- Migration dir: migrations/
- Expected tables after goose up: gitlab_instances, projects, project_policies, hook_events, merge_requests, mr_versions, review_runs, review_findings, gitlab_discussions, comment_actions, audit_logs

## Docker Registry Workaround

- Docker Hub registry may be unreachable. Redis image redis:7 was locally tagged from redis:8-alpine.
- MySQL 8.4 image is locally cached.
