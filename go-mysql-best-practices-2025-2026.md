# Go + MySQL Best Practices Research Report (2025-2026)

## 1. Best MySQL Driver for Go

### Recommendation: `github.com/go-sql-driver/mysql` (v1.8+)

This is the **undisputed standard** MySQL driver for Go. It has been the de facto choice for 10+ years and remains so in 2025-2026.

**Why:**
- Pure Go implementation (no CGO dependencies)
- Implements Go's `database/sql` interface natively
- Actively maintained (latest release supports Go 1.22+)
- Used by virtually every Go+MySQL project including GORM, sqlc, sqlx, ent
- 14k+ GitHub stars, mature and battle-tested
- Supports MySQL 5.5+, MariaDB, TiDB, PlanetScale

**Alternatives considered:**
- **`github.com/ziutek/mymysql`** — Older, rarely used now, no real advantage
- **`gorm.io/driver/mysql`** — This is a wrapper around `go-sql-driver/mysql` for GORM

**Production connection setup:**

```go
import (
    "database/sql"
    "time"

    _ "github.com/go-sql-driver/mysql"
)

func NewDB(dsn string) (*sql.DB, error) {
    // DSN format: user:password@tcp(host:port)/dbname?parseTime=true&loc=UTC&multiStatements=true
    db, err := sql.Open("mysql", dsn)
    if err != nil {
        return nil, err
    }

    // Connection pool settings (CRITICAL for production)
    db.SetMaxOpenConns(25)                  // Max open connections to MySQL
    db.SetMaxIdleConns(10)                  // Keep idle connections ready
    db.SetConnMaxLifetime(5 * time.Minute)  // Recycle connections periodically
    db.SetConnMaxIdleTime(1 * time.Minute)  // Close idle connections after 1min

    // Verify connectivity
    if err := db.Ping(); err != nil {
        return nil, err
    }

    return db, nil
}
```

**Critical DSN parameters for MySQL:**
- `parseTime=true` — Parse MySQL `DATETIME`/`TIMESTAMP` into Go `time.Time` (MUST HAVE)
- `loc=UTC` — Use UTC timezone for time values
- `charset=utf8mb4` — Full Unicode support (default in MySQL 8+)
- `collation=utf8mb4_unicode_ci` — Proper Unicode collation
- `multiStatements=true` — Needed for migrations (but be careful with SQL injection)
- `interpolateParams=true` — Client-side parameter interpolation (reduces round trips)
- `tls=preferred` — Enable TLS in production

---

## 2. Best Migration Tool

### Recommendation: **Goose** (`github.com/pressly/goose/v3`)

**Why Goose over alternatives:**

| Feature | Goose | golang-migrate | Atlas |
|---------|-------|---------------|-------|
| MySQL support | ✅ Excellent | ✅ Good | ✅ Good |
| SQL migrations | ✅ | ✅ | ✅ |
| Go migrations | ✅ | ❌ | ❌ |
| Embed in Go binary | ✅ (`embed.FS`) | ✅ | ❌ (separate CLI) |
| CLI tool | ✅ | ✅ | ✅ |
| Versioning | Sequential + Timestamp | Timestamp pairs | Declarative |
| Community consensus | ⭐ Most recommended | Popular | Growing |
| Simplicity | ⭐ Simple | Medium | Complex (HCL config) |
| Production-ready | ✅ | ✅ | ✅ |

**Community consensus (2025):** Goose is the most frequently recommended tool on r/golang. It's simple, embeddable, and works great with sqlc.

**Atlas** is powerful (declarative schema-as-code, drift detection) but more complex — best for large teams needing schema governance. Overkill for most projects.

**golang-migrate** is solid but lacks Go-function migrations and has a more fragmented codebase.

**Goose setup with MySQL:**

```go
// migrations/001_create_tables.sql
-- +goose Up
CREATE TABLE merge_requests (
    id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    gitlab_mr_id BIGINT NOT NULL,
    project_id BIGINT NOT NULL,
    title VARCHAR(500) NOT NULL,
    description TEXT,
    status ENUM('pending', 'reviewing', 'approved', 'rejected') NOT NULL DEFAULT 'pending',
    metadata JSON,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE KEY uk_gitlab_mr (gitlab_mr_id, project_id),
    INDEX idx_status (status),
    INDEX idx_created (created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE IF EXISTS merge_requests;
```

**Embedding migrations in Go binary (recommended):**

```go
package main

import (
    "database/sql"
    "embed"
    "log"

    "github.com/pressly/goose/v3"
    _ "github.com/go-sql-driver/mysql"
)

//go:embed migrations/*.sql
var embedMigrations embed.FS

func runMigrations(db *sql.DB) error {
    goose.SetBaseFS(embedMigrations)

    if err := goose.SetDialect("mysql"); err != nil {
        return err
    }

    return goose.Up(db, "migrations")
}
```

**CLI usage:**
```bash
# Install
go install github.com/pressly/goose/v3/cmd/goose@latest

# Create migration
goose -dir migrations create add_reviews_table sql

# Run migrations
goose -dir migrations mysql "user:password@tcp(localhost:3306)/dbname?parseTime=true" up

# Rollback
goose -dir migrations mysql "user:password@tcp(localhost:3306)/dbname?parseTime=true" down

# Check status
goose -dir migrations mysql "user:password@tcp(localhost:3306)/dbname?parseTime=true" status
```

---

## 3. Best ORM/Query Builder Patterns

### Recommendation: **sqlc** (primary) + **sqlx** (dynamic queries) + **Goose** (migrations)

This is the **most recommended production stack** in the Go community as of 2025-2026.

### Why sqlc as primary:

| Tool | Type Safety | Performance | SQL Control | Learning Curve | MySQL Support |
|------|------------|-------------|-------------|----------------|---------------|
| **sqlc** | ⭐ Compile-time | ⭐ Zero overhead | ⭐ Full SQL | Medium | ✅ Full |
| **GORM** | Runtime only | Reflection overhead | Limited | Low | ✅ Full |
| **sqlx** | Runtime only | Low overhead | Full SQL | Low | ✅ Full |
| **ent** | Code-gen | Good | Generated | High | ✅ Full |
| **Bun** | Runtime | Good | Good | Medium | ✅ Full |

**sqlc generates type-safe Go code from SQL** — you write SQL, it generates Go functions with proper types. Zero runtime overhead. MySQL is fully supported (engine: `mysql`).

### sqlc configuration for MySQL:

```yaml
# sqlc.yaml
version: "2"
sql:
  - engine: "mysql"
    queries: "internal/db/queries/"
    schema: "migrations/"
    gen:
      go:
        package: "db"
        out: "internal/db"
        sql_package: "database/sql"
        emit_json_tags: true
        emit_prepared_queries: false
        emit_interface: true
        emit_exact_table_names: false
        emit_empty_slices: true
        overrides:
          - db_type: "json"
            go_type: "encoding/json.RawMessage"
          - db_type: "bigint"
            go_type: "int64"
          - db_type: "timestamp"
            go_type: "time.Time"
```

### sqlc query examples (MySQL syntax):

```sql
-- internal/db/queries/merge_requests.sql

-- name: GetMergeRequest :one
SELECT * FROM merge_requests
WHERE id = ? LIMIT 1;

-- name: ListMergeRequestsByStatus :many
SELECT * FROM merge_requests
WHERE status = ?
ORDER BY created_at DESC
LIMIT ? OFFSET ?;

-- name: CreateMergeRequest :execresult
INSERT INTO merge_requests (
    gitlab_mr_id, project_id, title, description, status, metadata
) VALUES (?, ?, ?, ?, ?, ?);

-- name: UpdateMergeRequestStatus :exec
UPDATE merge_requests
SET status = ?
WHERE id = ?;

-- name: CountMergeRequestsByStatus :one
SELECT COUNT(*) as count FROM merge_requests
WHERE status = ?;
```

### When to add sqlx for dynamic queries:

sqlc can't handle fully dynamic WHERE clauses. Use sqlx (`github.com/jmoiron/sqlx`) for those:

```go
import "github.com/jmoiron/sqlx"

type MergeRequestFilter struct {
    Status    *string
    ProjectID *int64
    After     *time.Time
    Limit     int
    Offset    int
}

func (r *Repository) ListMergeRequests(ctx context.Context, filter MergeRequestFilter) ([]MergeRequest, error) {
    query := "SELECT * FROM merge_requests WHERE 1=1"
    args := []interface{}{}

    if filter.Status != nil {
        query += " AND status = ?"
        args = append(args, *filter.Status)
    }
    if filter.ProjectID != nil {
        query += " AND project_id = ?"
        args = append(args, *filter.ProjectID)
    }
    if filter.After != nil {
        query += " AND created_at > ?"
        args = append(args, *filter.After)
    }

    query += " ORDER BY created_at DESC LIMIT ? OFFSET ?"
    args = append(args, filter.Limit, filter.Offset)

    var results []MergeRequest
    err := sqlx.SelectContext(ctx, r.db, &results, query, args...)
    return results, err
}
```

### Why NOT GORM:

- **Performance:** Reflection-based, slower for high-throughput
- **Magic behavior:** AutoMigrate is dangerous in production; hard to debug generated SQL
- **Community trend:** Go community in 2025 is strongly moving toward sqlc/sqlx
- **SQL control:** You lose fine-grained control over queries

GORM is fine for rapid prototyping or simple CRUD apps, but for a production review system, sqlc+sqlx is the better choice.

### Why NOT ent:

- Heavy code generation (long compile times)
- MySQL support is good but PostgreSQL is the primary target
- Overkill for most applications; best for apps with very complex graph-like relationships
- Steeper learning curve

---

## 4. Docker Compose Setup for MySQL 8+ Local Dev

```yaml
# docker-compose.yml
version: "3.8"

services:
  mysql:
    image: mysql:8.4
    container_name: mreviewer-mysql
    restart: unless-stopped
    ports:
      - "3306:3306"
    environment:
      MYSQL_ROOT_PASSWORD: rootpassword
      MYSQL_DATABASE: mreviewer
      MYSQL_USER: mreviewer
      MYSQL_PASSWORD: mreviewer_password
    volumes:
      - mysql_data:/var/lib/mysql
      - ./init-db:/docker-entrypoint-initdb.d  # Auto-run SQL scripts on first start
    command:
      - --default-authentication-plugin=caching_sha2_password
      - --character-set-server=utf8mb4
      - --collation-server=utf8mb4_unicode_ci
      - --innodb-buffer-pool-size=256M
      - --max-connections=200
      - --log-bin-trust-function-creators=1
      # Performance optimizations for dev
      - --innodb-flush-log-at-trx-commit=2
      - --sync-binlog=0
    healthcheck:
      test: ["CMD", "mysqladmin", "ping", "-h", "localhost", "-u", "root", "-prootpassword"]
      interval: 10s
      timeout: 5s
      retries: 5
      start_period: 30s  # MySQL 8 takes a while to initialize

  app:
    build:
      context: .
      dockerfile: Dockerfile
    depends_on:
      mysql:
        condition: service_healthy
    environment:
      DB_DSN: "mreviewer:mreviewer_password@tcp(mysql:3306)/mreviewer?parseTime=true&loc=UTC&charset=utf8mb4&collation=utf8mb4_unicode_ci"
    ports:
      - "8080:8080"

volumes:
  mysql_data:
```

**Key considerations for MySQL 8+:**
- MySQL 8.4 is the latest LTS (recommended over 9.x innovation releases)
- `caching_sha2_password` is default auth in MySQL 8+ (go-sql-driver supports it)
- Use `healthcheck` with `start_period` — MySQL 8 takes 20-30s to initialize on first run
- `innodb-flush-log-at-trx-commit=2` speeds up dev significantly (NOT for production)

**Makefile targets for convenience:**

```makefile
.PHONY: db-up db-down db-reset migrate

db-up:
	docker compose up -d mysql
	@echo "Waiting for MySQL to be ready..."
	@docker compose exec mysql mysqladmin ping -h localhost -u root -prootpassword --wait=30

db-down:
	docker compose down

db-reset:
	docker compose down -v
	docker compose up -d mysql

migrate:
	goose -dir migrations mysql "mreviewer:mreviewer_password@tcp(localhost:3306)/mreviewer?parseTime=true" up

migrate-down:
	goose -dir migrations mysql "mreviewer:mreviewer_password@tcp(localhost:3306)/mreviewer?parseTime=true" down

sqlc:
	sqlc generate
```

---

## 5. Go Project Structure Conventions (2025-2026)

### Recommended structure for a MySQL-backed Go service:

```
mreviewer/
├── cmd/
│   └── server/
│       └── main.go              # Entry point: config loading, DI wiring, server start
├── internal/
│   ├── config/
│   │   └── config.go            # Configuration loading (env vars, YAML)
│   ├── db/
│   │   ├── db.go                # Generated by sqlc (DBTX interface)
│   │   ├── models.go            # Generated by sqlc (struct types)
│   │   ├── merge_requests.sql.go # Generated by sqlc (query functions)
│   │   └── queries/
│   │       └── merge_requests.sql # Hand-written SQL queries for sqlc
│   ├── domain/
│   │   └── review.go            # Domain types and business logic interfaces
│   ├── service/
│   │   ├── review.go            # Business logic / use cases
│   │   └── review_test.go
│   ├── handler/
│   │   ├── review.go            # HTTP/gRPC handlers
│   │   └── review_test.go
│   ├── repository/
│   │   ├── review.go            # Repository layer (wraps sqlc + sqlx)
│   │   └── review_test.go
│   └── middleware/
│       └── auth.go              # HTTP middleware
├── migrations/
│   ├── 001_create_tables.sql    # Goose migrations
│   └── 002_add_indexes.sql
├── docker-compose.yml
├── Dockerfile
├── Makefile
├── sqlc.yaml
├── go.mod
├── go.sum
└── README.md
```

### Key conventions in 2025-2026:

1. **`internal/` is mandatory** for application code — compiler-enforced privacy
2. **`cmd/` for entry points** — each subdirectory = one binary
3. **Feature-based or layer-based organization** — layer-based (`handler/`, `service/`, `repository/`) is more common for smaller services; feature-based (`internal/review/`, `internal/project/`) for larger monoliths
4. **`pkg/` is rare** — only if you're publishing reusable libraries. Most projects don't need it.
5. **Flat `main.go`** — Keep it to ~30 lines: load config, build dependencies, start server
6. **No `utils/` or `helpers/`** — Anti-pattern in Go. Put functions in the package that uses them.
7. **`go.work`** for monorepos with multiple modules
8. **`sqlc.yaml` at project root**

---

## 6. Testing Patterns for Go with MySQL

### Recommendation: **testcontainers-go** (integration tests) + standard `testing` (unit tests)

### Why testcontainers-go over dockertest:

| Feature | testcontainers-go | dockertest | go-mysql-test |
|---------|------------------|------------|---------------|
| Active maintenance | ✅ Very active | ⚠️ Slower | ❌ Stale |
| MySQL module | ✅ Dedicated module | Generic | N/A |
| Init scripts | ✅ Built-in | Manual | N/A |
| Cleanup | ✅ Automatic | Manual | N/A |
| Community | ⭐ Standard in 2025 | Legacy | N/A |
| Testcontainers ecosystem | ✅ Part of wider ecosystem | Standalone | N/A |

### testcontainers-go MySQL setup:

```go
// internal/testutil/mysql.go
package testutil

import (
    "context"
    "database/sql"
    "path/filepath"
    "testing"

    _ "github.com/go-sql-driver/mysql"
    "github.com/testcontainers/testcontainers-go"
    "github.com/testcontainers/testcontainers-go/modules/mysql"
)

func SetupMySQLContainer(t *testing.T) *sql.DB {
    t.Helper()
    ctx := context.Background()

    mysqlContainer, err := mysql.Run(ctx,
        "mysql:8.4",
        mysql.WithDatabase("mreviewer_test"),
        mysql.WithUsername("test"),
        mysql.WithPassword("test"),
        mysql.WithScripts(filepath.Join("..", "..", "migrations", "schema.sql")),
    )
    if err != nil {
        t.Fatalf("failed to start MySQL container: %v", err)
    }

    t.Cleanup(func() {
        if err := testcontainers.TerminateContainer(mysqlContainer); err != nil {
            t.Logf("failed to terminate container: %v", err)
        }
    })

    connStr, err := mysqlContainer.ConnectionString(ctx, "parseTime=true")
    if err != nil {
        t.Fatalf("failed to get connection string: %v", err)
    }

    db, err := sql.Open("mysql", connStr)
    if err != nil {
        t.Fatalf("failed to connect to MySQL: %v", err)
    }

    t.Cleanup(func() { db.Close() })
    return db
}
```

### Integration test example:

```go
// internal/repository/review_test.go
package repository_test

import (
    "context"
    "testing"

    "myproject/internal/db"
    "myproject/internal/repository"
    "myproject/internal/testutil"
)

func TestReviewRepository_Create(t *testing.T) {
    if testing.Short() {
        t.Skip("skipping integration test")
    }

    sqlDB := testutil.SetupMySQLContainer(t)
    queries := db.New(sqlDB)
    repo := repository.NewReview(queries, sqlDB)

    ctx := context.Background()
    review, err := repo.Create(ctx, repository.CreateReviewInput{
        GitlabMRID: 123,
        ProjectID:  456,
        Title:      "Test MR",
    })

    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if review.Title != "Test MR" {
        t.Errorf("expected title 'Test MR', got %q", review.Title)
    }
}
```

### Unit testing pattern (mock the sqlc interface):

sqlc can generate interfaces (`emit_interface: true` in config), making mocking easy:

```go
// sqlc generates this interface:
type Querier interface {
    GetMergeRequest(ctx context.Context, id int64) (MergeRequest, error)
    ListMergeRequestsByStatus(ctx context.Context, arg ListMergeRequestsByStatusParams) ([]MergeRequest, error)
    CreateMergeRequest(ctx context.Context, arg CreateMergeRequestParams) (sql.Result, error)
}

// In tests, create a mock:
type mockQuerier struct {
    getMergeRequestFn func(ctx context.Context, id int64) (db.MergeRequest, error)
}

func (m *mockQuerier) GetMergeRequest(ctx context.Context, id int64) (db.MergeRequest, error) {
    return m.getMergeRequestFn(ctx, id)
}
// ... implement other methods
```

### Test organization:

```bash
# Run unit tests only (fast, no Docker needed)
go test -short ./...

# Run all tests including integration (requires Docker)
go test ./...

# Run with race detector
go test -race ./...

# Run specific package tests with verbose output
go test -v ./internal/repository/...
```

---

## 7. MySQL-Specific Gotchas vs PostgreSQL

### 7.1 JSON Columns

**MySQL:** Has `JSON` type (since 5.7). Stored as binary internally (like PostgreSQL JSONB), but:
- No `JSONB` equivalent — MySQL's `JSON` is already binary-optimized
- JSON indexing requires generated/virtual columns + indexes (more verbose than PG)
- Use `JSON_EXTRACT()`, `->`, and `->>` operators for querying

```sql
-- MySQL JSON column
CREATE TABLE reviews (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    metadata JSON,
    -- Virtual column for indexing a JSON field
    reviewer_name VARCHAR(255) GENERATED ALWAYS AS (metadata->>'$.reviewer_name') STORED,
    INDEX idx_reviewer (reviewer_name)
);

-- Query JSON
SELECT * FROM reviews WHERE metadata->>'$.severity' = 'critical';
SELECT * FROM reviews WHERE JSON_CONTAINS(metadata, '"bug"', '$.tags');
```

**In Go with sqlc**, map JSON columns to `json.RawMessage`:
```yaml
# sqlc.yaml overrides
overrides:
  - db_type: "json"
    go_type: "encoding/json.RawMessage"
```

### 7.2 Advisory Locks

**MySQL `GET_LOCK()` vs PostgreSQL `pg_advisory_lock()`:**

| Feature | MySQL GET_LOCK | PostgreSQL pg_advisory_lock |
|---------|---------------|---------------------------|
| Key type | String (max 64 chars) | Integer (bigint) |
| Multiple locks per session | ✅ (MySQL 5.7+) | ✅ |
| Try-lock with timeout | ✅ `GET_LOCK('name', timeout)` | ✅ `pg_try_advisory_lock()` |
| Session-level locks | ✅ | ✅ |
| Transaction-level locks | ❌ Not natively | ✅ `pg_advisory_xact_lock()` |
| Release | Manual `RELEASE_LOCK()` | Auto (session/xact end) |

**MySQL advisory lock pattern in Go:**
```go
func (r *Repository) WithAdvisoryLock(ctx context.Context, lockName string, fn func() error) error {
    // Acquire lock with 10-second timeout
    var acquired int
    err := r.db.QueryRowContext(ctx, "SELECT GET_LOCK(?, 10)", lockName).Scan(&acquired)
    if err != nil {
        return fmt.Errorf("acquiring lock: %w", err)
    }
    if acquired != 1 {
        return fmt.Errorf("could not acquire lock %q", lockName)
    }

    defer func() {
        // Always release the lock
        r.db.ExecContext(ctx, "SELECT RELEASE_LOCK(?)", lockName)
    }()

    return fn()
}

// Usage:
err := repo.WithAdvisoryLock(ctx, fmt.Sprintf("mr_review_%d", mrID), func() error {
    // Critical section: only one process reviews this MR at a time
    return processReview(ctx, mrID)
})
```

**⚠️ MySQL gotcha:** Advisory locks are tied to the **connection**, not the transaction. If your connection pool returns a different connection, the lock is lost. Use a dedicated `*sql.Conn` for lock operations:

```go
func (r *Repository) WithAdvisoryLockSafe(ctx context.Context, lockName string, fn func(conn *sql.Conn) error) error {
    conn, err := r.db.Conn(ctx)
    if err != nil {
        return err
    }
    defer conn.Close()

    var acquired int
    err = conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, 10)", lockName).Scan(&acquired)
    if err != nil || acquired != 1 {
        return fmt.Errorf("could not acquire lock %q", lockName)
    }
    defer conn.ExecContext(ctx, "SELECT RELEASE_LOCK(?)", lockName)

    return fn(conn)
}
```

There is also a Go library for this: `github.com/sanketplus/go-mysql-lock`.

### 7.3 Auto-Increment vs Sequences

- MySQL uses `AUTO_INCREMENT` on columns (no separate sequence objects like PG)
- Use `LAST_INSERT_ID()` to get the last auto-increment value
- sqlc handles this via `:execresult` which returns `sql.Result` with `LastInsertId()`

### 7.4 ENUM Types

MySQL has native `ENUM` type (PostgreSQL requires `CREATE TYPE`):
```sql
status ENUM('pending', 'reviewing', 'approved', 'rejected') NOT NULL DEFAULT 'pending'
```
In Go, map these to strings. sqlc will generate string types.

### 7.5 UPSERT Syntax

```sql
-- MySQL (different from PostgreSQL's ON CONFLICT)
INSERT INTO reviews (gitlab_mr_id, project_id, status)
VALUES (?, ?, ?)
ON DUPLICATE KEY UPDATE
    status = VALUES(status),
    updated_at = CURRENT_TIMESTAMP;
```

### 7.6 Timestamp Handling

- MySQL `TIMESTAMP` is stored in UTC, displayed in session timezone
- MySQL `DATETIME` is stored as-is (no timezone conversion)
- **Always use `TIMESTAMP`** for audit columns and `parseTime=true` in DSN
- MySQL `ON UPDATE CURRENT_TIMESTAMP` auto-updates timestamp columns (no PG equivalent)

### 7.7 Full-Text Search

MySQL has built-in `FULLTEXT` indexes (InnoDB):
```sql
ALTER TABLE merge_requests ADD FULLTEXT INDEX ft_title_desc (title, description);
SELECT * FROM merge_requests WHERE MATCH(title, description) AGAINST('bug fix' IN NATURAL LANGUAGE MODE);
```
This is simpler than PostgreSQL's `tsvector`/`tsquery` approach.

### 7.8 Transaction Isolation

- MySQL InnoDB default: `REPEATABLE READ` (PG default: `READ COMMITTED`)
- Consider setting to `READ COMMITTED` for better concurrency in high-write systems:
```sql
SET GLOBAL transaction_isolation = 'READ-COMMITTED';
```

### 7.9 Connection Handling

- MySQL has a hard connection limit (`max_connections`), defaults to 151
- Go's `database/sql` pool must be configured smaller than MySQL's max
- Unlike PG, MySQL doesn't have a built-in connection pooler like PgBouncer. For high-scale, consider ProxySQL.

### 7.10 NULL Handling in Go

MySQL NULLable columns require `sql.NullString`, `sql.NullInt64`, etc. in Go:
```go
type MergeRequest struct {
    ID          int64
    Title       string
    Description sql.NullString  // nullable TEXT column
}
```
sqlc handles this automatically based on your schema's NULL/NOT NULL constraints.

---

## Summary: Recommended Stack

| Component | Tool | Why |
|-----------|------|-----|
| **MySQL Driver** | `go-sql-driver/mysql` | Only real option, battle-tested |
| **Migrations** | Goose v3 | Simple, embeddable, SQL+Go migrations |
| **Query Layer (static)** | sqlc | Type-safe generated code, zero overhead |
| **Query Layer (dynamic)** | sqlx | For complex filter/search queries |
| **Testing** | testcontainers-go | Dedicated MySQL module, auto-cleanup |
| **Project Structure** | `cmd/` + `internal/` | Standard Go conventions |
| **Docker MySQL** | MySQL 8.4 LTS | Latest stable LTS |
| **Connection Pool** | `database/sql` built-in | 25 max open, 10 idle, 5min lifetime |

### Go module dependencies:

```
github.com/go-sql-driver/mysql   v1.8+     # MySQL driver
github.com/pressly/goose/v3      v3.x      # Migrations
github.com/sqlc-dev/sqlc          v1.30+   # Code generation (dev tool)
github.com/jmoiron/sqlx           v1.4+    # Dynamic queries
github.com/testcontainers/testcontainers-go/modules/mysql  # Testing
```
