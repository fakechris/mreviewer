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
