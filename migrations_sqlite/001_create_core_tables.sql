-- +goose Up

-- GitLab instance registry.
CREATE TABLE gitlab_instances (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    url TEXT NOT NULL,
    name TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (url)
);

-- Projects registered for review.
CREATE TABLE projects (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    gitlab_instance_id INTEGER NOT NULL,
    gitlab_project_id INTEGER NOT NULL,
    path_with_namespace TEXT NOT NULL DEFAULT '',
    enabled BOOLEAN NOT NULL DEFAULT 1,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (gitlab_instance_id, gitlab_project_id),
    CONSTRAINT fk_projects_instance FOREIGN KEY (gitlab_instance_id) REFERENCES gitlab_instances(id) ON DELETE CASCADE
);
CREATE INDEX idx_projects_enabled ON projects(enabled);

-- Per-project review policies.
CREATE TABLE project_policies (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id INTEGER NOT NULL,
    confidence_threshold REAL NOT NULL DEFAULT 0.5,
    severity_threshold TEXT NOT NULL DEFAULT 'low',
    include_paths TEXT,
    exclude_paths TEXT,
    gate_mode TEXT NOT NULL DEFAULT 'threads_resolved',
    provider_route TEXT NOT NULL DEFAULT '',
    extra TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (project_id),
    CONSTRAINT fk_policies_project FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
);

-- Inbound webhook events (idempotency and audit).
CREATE TABLE hook_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    delivery_key TEXT NOT NULL,
    hook_source TEXT NOT NULL DEFAULT '',
    event_type TEXT NOT NULL DEFAULT '',
    gitlab_instance_id INTEGER,
    project_id INTEGER,
    mr_iid INTEGER,
    action TEXT NOT NULL DEFAULT '',
    head_sha TEXT NOT NULL DEFAULT '',
    payload TEXT,
    verification_outcome TEXT NOT NULL DEFAULT 'verified',
    rejection_reason TEXT NOT NULL DEFAULT '',
    received_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (delivery_key)
);
CREATE INDEX idx_hook_events_project_mr ON hook_events(project_id, mr_iid);
CREATE INDEX idx_hook_events_received ON hook_events(received_at);

-- Merge request tracking.
CREATE TABLE merge_requests (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id INTEGER NOT NULL,
    mr_iid INTEGER NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    source_branch TEXT NOT NULL DEFAULT '',
    target_branch TEXT NOT NULL DEFAULT '',
    author TEXT NOT NULL DEFAULT '',
    state TEXT NOT NULL DEFAULT 'opened',
    is_draft BOOLEAN NOT NULL DEFAULT 0,
    head_sha TEXT NOT NULL DEFAULT '',
    web_url TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (project_id, mr_iid),
    CONSTRAINT fk_mr_project FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
);
CREATE INDEX idx_merge_requests_state ON merge_requests(state);

-- MR version snapshots (SHAs for diff discussion positioning).
CREATE TABLE mr_versions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    merge_request_id INTEGER NOT NULL,
    gitlab_version_id INTEGER NOT NULL,
    base_sha TEXT NOT NULL DEFAULT '',
    start_sha TEXT NOT NULL DEFAULT '',
    head_sha TEXT NOT NULL DEFAULT '',
    patch_id_sha TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (merge_request_id, gitlab_version_id),
    CONSTRAINT fk_version_mr FOREIGN KEY (merge_request_id) REFERENCES merge_requests(id) ON DELETE CASCADE
);

-- Review run lifecycle.
CREATE TABLE review_runs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id INTEGER NOT NULL,
    merge_request_id INTEGER NOT NULL,
    hook_event_id INTEGER,
    trigger_type TEXT NOT NULL DEFAULT 'webhook',
    head_sha TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending',
    error_code TEXT NOT NULL DEFAULT '',
    error_detail TEXT,
    retry_count INTEGER NOT NULL DEFAULT 0,
    max_retries INTEGER NOT NULL DEFAULT 3,
    next_retry_at TIMESTAMP NULL DEFAULT NULL,
    claimed_by TEXT NOT NULL DEFAULT '',
    claimed_at TIMESTAMP NULL DEFAULT NULL,
    started_at TIMESTAMP NULL DEFAULT NULL,
    completed_at TIMESTAMP NULL DEFAULT NULL,
    provider_latency_ms INTEGER NOT NULL DEFAULT 0,
    provider_tokens_total INTEGER NOT NULL DEFAULT 0,
    idempotency_key TEXT NOT NULL DEFAULT '',
    scope_json TEXT DEFAULT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (idempotency_key),
    CONSTRAINT fk_run_project FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
    CONSTRAINT fk_run_mr FOREIGN KEY (merge_request_id) REFERENCES merge_requests(id) ON DELETE CASCADE,
    CONSTRAINT fk_run_hook FOREIGN KEY (hook_event_id) REFERENCES hook_events(id) ON DELETE SET NULL
);
CREATE INDEX idx_review_runs_status_retry ON review_runs(status, next_retry_at);
CREATE INDEX idx_review_runs_project_mr ON review_runs(project_id, merge_request_id);
CREATE INDEX idx_review_runs_head_sha ON review_runs(head_sha);

-- Individual review findings.
CREATE TABLE review_findings (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    review_run_id INTEGER NOT NULL,
    merge_request_id INTEGER NOT NULL,
    category TEXT NOT NULL DEFAULT '',
    severity TEXT NOT NULL DEFAULT 'medium',
    confidence REAL NOT NULL DEFAULT 0.0,
    title TEXT NOT NULL DEFAULT '',
    body_markdown TEXT,
    path TEXT NOT NULL DEFAULT '',
    anchor_kind TEXT NOT NULL DEFAULT 'new_line',
    old_line INTEGER,
    new_line INTEGER,
    range_start_kind TEXT,
    range_start_old_line INTEGER,
    range_start_new_line INTEGER,
    range_end_kind TEXT,
    range_end_old_line INTEGER,
    range_end_new_line INTEGER,
    anchor_snippet TEXT,
    evidence TEXT,
    suggested_patch TEXT,
    canonical_key TEXT NOT NULL DEFAULT '',
    anchor_fingerprint TEXT NOT NULL DEFAULT '',
    semantic_fingerprint TEXT NOT NULL DEFAULT '',
    state TEXT NOT NULL DEFAULT 'new',
    matched_finding_id INTEGER,
    last_seen_run_id INTEGER,
    gitlab_discussion_id TEXT NOT NULL DEFAULT '',
    error_code TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_finding_run FOREIGN KEY (review_run_id) REFERENCES review_runs(id) ON DELETE CASCADE,
    CONSTRAINT fk_finding_mr FOREIGN KEY (merge_request_id) REFERENCES merge_requests(id) ON DELETE CASCADE
);
CREATE INDEX idx_review_findings_run ON review_findings(review_run_id);
CREATE INDEX idx_review_findings_mr_state ON review_findings(merge_request_id, state);
CREATE INDEX idx_review_findings_anchor_fp ON review_findings(anchor_fingerprint);
CREATE INDEX idx_review_findings_semantic_fp ON review_findings(semantic_fingerprint);

-- GitLab discussions created by the bot.
CREATE TABLE gitlab_discussions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    review_finding_id INTEGER NOT NULL,
    merge_request_id INTEGER NOT NULL,
    gitlab_discussion_id TEXT NOT NULL DEFAULT '',
    discussion_type TEXT NOT NULL DEFAULT 'diff',
    resolved BOOLEAN NOT NULL DEFAULT 0,
    superseded_by_discussion_id INTEGER,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_disc_finding FOREIGN KEY (review_finding_id) REFERENCES review_findings(id) ON DELETE CASCADE,
    CONSTRAINT fk_disc_mr FOREIGN KEY (merge_request_id) REFERENCES merge_requests(id) ON DELETE CASCADE
);
CREATE INDEX idx_gitlab_discussions_finding ON gitlab_discussions(review_finding_id);
CREATE INDEX idx_gitlab_discussions_mr ON gitlab_discussions(merge_request_id);
CREATE INDEX idx_gitlab_discussions_gitlab_disc ON gitlab_discussions(gitlab_discussion_id);

-- Idempotent comment/write action tracking.
CREATE TABLE comment_actions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    review_run_id INTEGER NOT NULL,
    review_finding_id INTEGER,
    action_type TEXT NOT NULL DEFAULT '',
    idempotency_key TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending',
    error_code TEXT NOT NULL DEFAULT '',
    error_detail TEXT,
    retry_count INTEGER NOT NULL DEFAULT 0,
    latency_ms INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (idempotency_key),
    CONSTRAINT fk_action_run FOREIGN KEY (review_run_id) REFERENCES review_runs(id) ON DELETE CASCADE,
    CONSTRAINT fk_action_finding FOREIGN KEY (review_finding_id) REFERENCES review_findings(id) ON DELETE SET NULL
);
CREATE INDEX idx_comment_actions_run ON comment_actions(review_run_id);
CREATE INDEX idx_comment_actions_status ON comment_actions(status);

-- Audit log for all significant actions.
CREATE TABLE audit_logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    entity_type TEXT NOT NULL DEFAULT '',
    entity_id INTEGER NOT NULL DEFAULT 0,
    action TEXT NOT NULL DEFAULT '',
    actor TEXT NOT NULL DEFAULT '',
    detail TEXT,
    delivery_key TEXT NOT NULL DEFAULT '',
    hook_source TEXT NOT NULL DEFAULT '',
    verification_outcome TEXT NOT NULL DEFAULT '',
    rejection_reason TEXT NOT NULL DEFAULT '',
    error_code TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_audit_logs_entity ON audit_logs(entity_type, entity_id);
CREATE INDEX idx_audit_logs_action ON audit_logs(action);
CREATE INDEX idx_audit_logs_delivery_key ON audit_logs(delivery_key);
CREATE INDEX idx_audit_logs_created ON audit_logs(created_at);

-- +goose Down
DROP TABLE IF EXISTS audit_logs;
DROP TABLE IF EXISTS comment_actions;
DROP TABLE IF EXISTS gitlab_discussions;
DROP TABLE IF EXISTS review_findings;
DROP TABLE IF EXISTS review_runs;
DROP TABLE IF EXISTS mr_versions;
DROP TABLE IF EXISTS merge_requests;
DROP TABLE IF EXISTS hook_events;
DROP TABLE IF EXISTS project_policies;
DROP TABLE IF EXISTS projects;
DROP TABLE IF EXISTS gitlab_instances;
