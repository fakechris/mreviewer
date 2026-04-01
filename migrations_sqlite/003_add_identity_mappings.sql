-- +goose Up

CREATE TABLE identity_mappings (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    platform TEXT NOT NULL DEFAULT '',
    project_path TEXT NOT NULL DEFAULT '',
    git_identity_key TEXT NOT NULL DEFAULT '',
    git_email TEXT NOT NULL DEFAULT '',
    git_name TEXT NOT NULL DEFAULT '',
    observed_role TEXT NOT NULL DEFAULT '',
    platform_username TEXT NOT NULL DEFAULT '',
    platform_user_id TEXT NOT NULL DEFAULT '',
    head_sha TEXT NOT NULL DEFAULT '',
    confidence REAL NOT NULL DEFAULT 0.0,
    source TEXT NOT NULL DEFAULT 'observed',
    status TEXT NOT NULL DEFAULT 'auto',
    last_seen_run_id INTEGER,
    resolved_by TEXT NOT NULL DEFAULT '',
    resolved_at TIMESTAMP NULL DEFAULT NULL,
    resolution_detail TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (platform, project_path, git_identity_key),
    CONSTRAINT fk_identity_last_seen_run FOREIGN KEY (last_seen_run_id) REFERENCES review_runs(id) ON DELETE SET NULL
);
CREATE INDEX idx_identity_mappings_status ON identity_mappings(status, platform);
CREATE INDEX idx_identity_mappings_project ON identity_mappings(project_path);
CREATE INDEX idx_identity_mappings_last_seen ON identity_mappings(last_seen_run_id);

-- +goose Down
DROP TABLE IF EXISTS identity_mappings;
