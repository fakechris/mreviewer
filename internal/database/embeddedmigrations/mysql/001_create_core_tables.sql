-- +goose Up

-- GitLab instance registry.
CREATE TABLE gitlab_instances (
    id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    url VARCHAR(2048) NOT NULL,
    name VARCHAR(255) NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE KEY uk_url (url(255))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Projects registered for review.
CREATE TABLE projects (
    id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    gitlab_instance_id BIGINT NOT NULL,
    gitlab_project_id BIGINT NOT NULL,
    path_with_namespace VARCHAR(1024) NOT NULL DEFAULT '',
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE KEY uk_instance_project (gitlab_instance_id, gitlab_project_id),
    INDEX idx_enabled (enabled),
    CONSTRAINT fk_projects_instance FOREIGN KEY (gitlab_instance_id) REFERENCES gitlab_instances(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Per-project review policies.
CREATE TABLE project_policies (
    id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    project_id BIGINT NOT NULL,
    confidence_threshold DOUBLE NOT NULL DEFAULT 0.5,
    severity_threshold VARCHAR(50) NOT NULL DEFAULT 'low',
    include_paths JSON,
    exclude_paths JSON,
    gate_mode VARCHAR(50) NOT NULL DEFAULT 'threads_resolved',
    provider_route VARCHAR(255) NOT NULL DEFAULT '',
    extra JSON,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE KEY uk_project (project_id),
    CONSTRAINT fk_policies_project FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Inbound webhook events (idempotency and audit).
CREATE TABLE hook_events (
    id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    delivery_key VARCHAR(255) NOT NULL,
    hook_source VARCHAR(50) NOT NULL DEFAULT '',
    event_type VARCHAR(100) NOT NULL DEFAULT '',
    gitlab_instance_id BIGINT,
    project_id BIGINT,
    mr_iid BIGINT,
    action VARCHAR(100) NOT NULL DEFAULT '',
    head_sha VARCHAR(64) NOT NULL DEFAULT '',
    payload JSON,
    verification_outcome VARCHAR(20) NOT NULL DEFAULT 'verified',
    rejection_reason VARCHAR(500) NOT NULL DEFAULT '',
    received_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE KEY uk_delivery_key (delivery_key),
    INDEX idx_project_mr (project_id, mr_iid),
    INDEX idx_received (received_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Merge request tracking.
CREATE TABLE merge_requests (
    id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    project_id BIGINT NOT NULL,
    mr_iid BIGINT NOT NULL,
    title VARCHAR(1024) NOT NULL DEFAULT '',
    source_branch VARCHAR(512) NOT NULL DEFAULT '',
    target_branch VARCHAR(512) NOT NULL DEFAULT '',
    author VARCHAR(255) NOT NULL DEFAULT '',
    state VARCHAR(50) NOT NULL DEFAULT 'opened',
    is_draft BOOLEAN NOT NULL DEFAULT FALSE,
    head_sha VARCHAR(64) NOT NULL DEFAULT '',
    web_url VARCHAR(2048) NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE KEY uk_project_mr (project_id, mr_iid),
    INDEX idx_state (state),
    CONSTRAINT fk_mr_project FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- MR version snapshots (SHAs for diff discussion positioning).
CREATE TABLE mr_versions (
    id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    merge_request_id BIGINT NOT NULL,
    gitlab_version_id BIGINT NOT NULL,
    base_sha VARCHAR(64) NOT NULL DEFAULT '',
    start_sha VARCHAR(64) NOT NULL DEFAULT '',
    head_sha VARCHAR(64) NOT NULL DEFAULT '',
    patch_id_sha VARCHAR(64) NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE KEY uk_mr_version (merge_request_id, gitlab_version_id),
    CONSTRAINT fk_version_mr FOREIGN KEY (merge_request_id) REFERENCES merge_requests(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Review run lifecycle.
CREATE TABLE review_runs (
    id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    project_id BIGINT NOT NULL,
    merge_request_id BIGINT NOT NULL,
    hook_event_id BIGINT,
    trigger_type VARCHAR(50) NOT NULL DEFAULT 'webhook',
    head_sha VARCHAR(64) NOT NULL DEFAULT '',
    status VARCHAR(50) NOT NULL DEFAULT 'pending',
    error_code VARCHAR(100) NOT NULL DEFAULT '',
    error_detail TEXT,
    superseded_by_run_id BIGINT NULL,
    retry_count INT NOT NULL DEFAULT 0,
    max_retries INT NOT NULL DEFAULT 3,
    next_retry_at TIMESTAMP NULL DEFAULT NULL,
    claimed_by VARCHAR(255) NOT NULL DEFAULT '',
    claimed_at TIMESTAMP NULL DEFAULT NULL,
    started_at TIMESTAMP NULL DEFAULT NULL,
    completed_at TIMESTAMP NULL DEFAULT NULL,
    provider_latency_ms BIGINT NOT NULL DEFAULT 0,
    provider_tokens_total BIGINT NOT NULL DEFAULT 0,
    idempotency_key VARCHAR(255) NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE KEY uk_idempotency (idempotency_key),
    INDEX idx_status_retry (status, next_retry_at),
    INDEX idx_project_mr (project_id, merge_request_id),
    INDEX idx_head_sha (head_sha),
    INDEX idx_superseded_by_run (superseded_by_run_id),
    CONSTRAINT fk_run_project FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
    CONSTRAINT fk_run_mr FOREIGN KEY (merge_request_id) REFERENCES merge_requests(id) ON DELETE CASCADE,
    CONSTRAINT fk_run_hook FOREIGN KEY (hook_event_id) REFERENCES hook_events(id) ON DELETE SET NULL,
    CONSTRAINT fk_run_superseded_by FOREIGN KEY (superseded_by_run_id) REFERENCES review_runs(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Individual review findings.
CREATE TABLE review_findings (
    id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    review_run_id BIGINT NOT NULL,
    merge_request_id BIGINT NOT NULL,
    category VARCHAR(100) NOT NULL DEFAULT '',
    severity VARCHAR(50) NOT NULL DEFAULT 'medium',
    confidence DOUBLE NOT NULL DEFAULT 0.0,
    title VARCHAR(1024) NOT NULL DEFAULT '',
    body_markdown TEXT,
    path VARCHAR(1024) NOT NULL DEFAULT '',
    anchor_kind VARCHAR(50) NOT NULL DEFAULT 'new_line',
    old_line INT,
    new_line INT,
    anchor_snippet TEXT,
    evidence TEXT,
    suggested_patch TEXT,
    canonical_key VARCHAR(255) NOT NULL DEFAULT '',
    anchor_fingerprint VARCHAR(128) NOT NULL DEFAULT '',
    semantic_fingerprint VARCHAR(128) NOT NULL DEFAULT '',
    state VARCHAR(50) NOT NULL DEFAULT 'new',
    matched_finding_id BIGINT,
    last_seen_run_id BIGINT,
    gitlab_discussion_id VARCHAR(255) NOT NULL DEFAULT '',
    error_code VARCHAR(100) NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    INDEX idx_run (review_run_id),
    INDEX idx_mr_state (merge_request_id, state),
    INDEX idx_anchor_fp (anchor_fingerprint),
    INDEX idx_semantic_fp (semantic_fingerprint),
    CONSTRAINT fk_finding_run FOREIGN KEY (review_run_id) REFERENCES review_runs(id) ON DELETE CASCADE,
    CONSTRAINT fk_finding_mr FOREIGN KEY (merge_request_id) REFERENCES merge_requests(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- GitLab discussions created by the bot.
CREATE TABLE gitlab_discussions (
    id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    review_finding_id BIGINT NOT NULL,
    merge_request_id BIGINT NOT NULL,
    gitlab_discussion_id VARCHAR(255) NOT NULL DEFAULT '',
    discussion_type VARCHAR(50) NOT NULL DEFAULT 'diff',
    resolved BOOLEAN NOT NULL DEFAULT FALSE,
    superseded_by_discussion_id BIGINT,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    INDEX idx_finding (review_finding_id),
    INDEX idx_mr (merge_request_id),
    INDEX idx_gitlab_disc (gitlab_discussion_id),
    CONSTRAINT fk_disc_finding FOREIGN KEY (review_finding_id) REFERENCES review_findings(id) ON DELETE CASCADE,
    CONSTRAINT fk_disc_mr FOREIGN KEY (merge_request_id) REFERENCES merge_requests(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Idempotent comment/write action tracking.
CREATE TABLE comment_actions (
    id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    review_run_id BIGINT NOT NULL,
    review_finding_id BIGINT,
    action_type VARCHAR(50) NOT NULL DEFAULT '',
    idempotency_key VARCHAR(255) NOT NULL DEFAULT '',
    status VARCHAR(50) NOT NULL DEFAULT 'pending',
    error_code VARCHAR(100) NOT NULL DEFAULT '',
    error_detail TEXT,
    retry_count INT NOT NULL DEFAULT 0,
    latency_ms BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE KEY uk_idempotency (idempotency_key),
    INDEX idx_run (review_run_id),
    INDEX idx_status (status),
    CONSTRAINT fk_action_run FOREIGN KEY (review_run_id) REFERENCES review_runs(id) ON DELETE CASCADE,
    CONSTRAINT fk_action_finding FOREIGN KEY (review_finding_id) REFERENCES review_findings(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Audit log for all significant actions.
CREATE TABLE audit_logs (
    id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    entity_type VARCHAR(100) NOT NULL DEFAULT '',
    entity_id BIGINT NOT NULL DEFAULT 0,
    action VARCHAR(100) NOT NULL DEFAULT '',
    actor VARCHAR(255) NOT NULL DEFAULT '',
    detail JSON,
    delivery_key VARCHAR(255) NOT NULL DEFAULT '',
    hook_source VARCHAR(50) NOT NULL DEFAULT '',
    verification_outcome VARCHAR(20) NOT NULL DEFAULT '',
    rejection_reason VARCHAR(500) NOT NULL DEFAULT '',
    error_code VARCHAR(100) NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_entity (entity_type, entity_id),
    INDEX idx_action (action),
    INDEX idx_delivery_key (delivery_key),
    INDEX idx_created (created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

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
