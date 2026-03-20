# Environment

Environment variables, external dependencies, and setup notes.

**What belongs here:** required env vars, external services, local setup quirks, credential placeholders, machine-specific constraints.
**What does NOT belong here:** service start/stop commands and port ownership details that already live in `.factory/services.yaml`.

---

## Core environment

- Go `1.25.x`
- Docker + Docker Compose available through OrbStack
- MySQL is the authoritative primary store for this mission
- Redis is optional/secondary; core correctness must still work through database-backed paths

## Expected env vars

- `PORT`
- `MYSQL_DSN`
- `REDIS_ADDR`
- `GITLAB_BASE_URL`
- `GITLAB_TOKEN`
- `GITLAB_WEBHOOK_SECRET`
- `ANTHROPIC_BASE_URL`
- `ANTHROPIC_API_KEY`
- `ANTHROPIC_MODEL`

## Credential policy

- Never commit real secrets.
- `.env` is gitignored and should contain placeholders until the user provides live values.
- Use mocked GitLab/provider endpoints by default unless a feature explicitly requires live integration.

## Testcontainers-go workaround

- The Ryuk reaper container must be disabled (`TESTCONTAINERS_RYUK_DISABLED=true`) due to Docker Hub registry pull issues in this environment.
- The `internal/db/dbtest` helper sets this automatically via `t.Setenv`.
- Workers creating new testcontainers-based test helpers should use the same pattern or import `dbtest`.

## Important clarification

- Source docs may still mention PostgreSQL.
- Workers must treat MySQL 8.4 as the single source of truth for persistence design and implementation.

## Working tree hygiene

- Some worker runs have left an untracked repo-root `worker` binary behind after local builds. Treat repo-root build artifacts as residue to remove before claiming a clean tree in handoffs or validation evidence.
