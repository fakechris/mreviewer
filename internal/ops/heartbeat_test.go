package ops

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/db/dbtest"
)

const migrationsDir = "../../migrations"

func setupHeartbeatTestDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, migrationsDir)
	return sqlDB
}

func insertHeartbeatTestInstance(t *testing.T, sqlDB *sql.DB) int64 {
	t.Helper()
	res, err := sqlDB.Exec("INSERT INTO gitlab_instances (url, name) VALUES ('https://test.gitlab.com', 'test')")
	if err != nil {
		t.Fatalf("insert instance: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("instance last insert id: %v", err)
	}
	return id
}

func insertHeartbeatTestProject(t *testing.T, sqlDB *sql.DB, instanceID int64) int64 {
	t.Helper()
	res, err := sqlDB.Exec(`INSERT INTO projects (gitlab_instance_id, gitlab_project_id, path_with_namespace, enabled)
		VALUES (?, ?, ?, TRUE)`, instanceID, 101, "group/project")
	if err != nil {
		t.Fatalf("insert project: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("project last insert id: %v", err)
	}
	return id
}

func insertHeartbeatTestMR(t *testing.T, sqlDB *sql.DB, projectID int64) int64 {
	t.Helper()
	res, err := sqlDB.Exec(`INSERT INTO merge_requests (project_id, mr_iid, title, state, target_branch, source_branch, head_sha)
		VALUES (?, ?, ?, 'opened', 'main', 'feature', ?)`, projectID, 1, "Heartbeat test", "sha-heartbeat")
	if err != nil {
		t.Fatalf("insert merge request: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("merge request last insert id: %v", err)
	}
	return id
}

func TestHeartbeatServiceBeatsAndListsActiveWorkers(t *testing.T) {
	ctx := context.Background()
	sqlDB := setupHeartbeatTestDB(t)
	now := time.Date(2026, time.March, 29, 18, 15, 0, 0, time.UTC)

	svc := NewService(db.New(sqlDB), WithNow(func() time.Time { return now }))
	identity := WorkerIdentity{
		WorkerID:              "worker-1",
		Hostname:              "host-a",
		Version:               "dev",
		ConfiguredConcurrency: 4,
	}

	if err := svc.Beat(ctx, identity); err != nil {
		t.Fatalf("Beat: %v", err)
	}

	workers, err := svc.ListActiveWorkers(ctx, 30*time.Second)
	if err != nil {
		t.Fatalf("ListActiveWorkers: %v", err)
	}
	if len(workers) != 1 {
		t.Fatalf("active workers = %d, want 1", len(workers))
	}
	if workers[0].WorkerID != identity.WorkerID {
		t.Fatalf("worker_id = %q, want %q", workers[0].WorkerID, identity.WorkerID)
	}
	if workers[0].Hostname != identity.Hostname {
		t.Fatalf("hostname = %q, want %q", workers[0].Hostname, identity.Hostname)
	}
	if workers[0].Version != identity.Version {
		t.Fatalf("version = %q, want %q", workers[0].Version, identity.Version)
	}
	if workers[0].ConfiguredConcurrency != identity.ConfiguredConcurrency {
		t.Fatalf("configured concurrency = %d, want %d", workers[0].ConfiguredConcurrency, identity.ConfiguredConcurrency)
	}
	if !workers[0].LastSeenAt.Equal(now) {
		t.Fatalf("last_seen_at = %s, want %s", workers[0].LastSeenAt, now)
	}
	if !workers[0].StartedAt.Equal(now) {
		t.Fatalf("started_at = %s, want %s", workers[0].StartedAt, now)
	}

	later := now.Add(45 * time.Second)
	expiredSvc := NewService(db.New(sqlDB), WithNow(func() time.Time { return later }))
	workers, err = expiredSvc.ListActiveWorkers(ctx, 30*time.Second)
	if err != nil {
		t.Fatalf("ListActiveWorkers after expiry: %v", err)
	}
	if len(workers) != 0 {
		t.Fatalf("active workers after expiry = %d, want 0", len(workers))
	}
}

func TestHeartbeatServiceIncludesRunningClaims(t *testing.T) {
	ctx := context.Background()
	sqlDB := setupHeartbeatTestDB(t)
	now := time.Date(2026, time.March, 29, 18, 20, 0, 0, time.UTC)

	instanceID := insertHeartbeatTestInstance(t, sqlDB)
	projectID := insertHeartbeatTestProject(t, sqlDB, instanceID)
	mrID := insertHeartbeatTestMR(t, sqlDB, projectID)

	if _, err := sqlDB.Exec(`INSERT INTO review_runs (project_id, merge_request_id, status, trigger_type, idempotency_key, head_sha, claimed_by, claimed_at, started_at, max_retries)
		VALUES (?, ?, 'running', 'mr_open', ?, ?, ?, ?, ?, 3)`,
		projectID, mrID, "heartbeat-running", "sha-running", "worker-1", now, now); err != nil {
		t.Fatalf("insert running review run: %v", err)
	}

	svc := NewService(db.New(sqlDB), WithNow(func() time.Time { return now }))
	if err := svc.Beat(ctx, WorkerIdentity{
		WorkerID:              "worker-1",
		Hostname:              "host-a",
		Version:               "dev",
		ConfiguredConcurrency: 4,
	}); err != nil {
		t.Fatalf("Beat worker-1: %v", err)
	}
	if err := svc.Beat(ctx, WorkerIdentity{
		WorkerID:              "worker-2",
		Hostname:              "host-b",
		Version:               "dev",
		ConfiguredConcurrency: 2,
	}); err != nil {
		t.Fatalf("Beat worker-2: %v", err)
	}

	workers, err := svc.ListActiveWorkers(ctx, 30*time.Second)
	if err != nil {
		t.Fatalf("ListActiveWorkers: %v", err)
	}
	if len(workers) != 2 {
		t.Fatalf("active workers = %d, want 2", len(workers))
	}

	counts := map[string]int64{}
	for _, worker := range workers {
		counts[worker.WorkerID] = worker.RunningRuns
	}
	if counts["worker-1"] != 1 {
		t.Fatalf("worker-1 running runs = %d, want 1", counts["worker-1"])
	}
	if counts["worker-2"] != 0 {
		t.Fatalf("worker-2 running runs = %d, want 0", counts["worker-2"])
	}
}
