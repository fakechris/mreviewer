-- +goose Up

CREATE TABLE identity_mappings (
    id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    platform VARCHAR(50) NOT NULL DEFAULT '',
    project_path VARCHAR(1024) NOT NULL DEFAULT '',
    git_identity_key VARCHAR(512) NOT NULL DEFAULT '',
    git_email VARCHAR(320) NOT NULL DEFAULT '',
    git_name VARCHAR(255) NOT NULL DEFAULT '',
    observed_role VARCHAR(50) NOT NULL DEFAULT '',
    platform_username VARCHAR(255) NOT NULL DEFAULT '',
    platform_user_id VARCHAR(255) NOT NULL DEFAULT '',
    head_sha VARCHAR(64) NOT NULL DEFAULT '',
    confidence DOUBLE NOT NULL DEFAULT 0.0,
    source VARCHAR(50) NOT NULL DEFAULT 'observed',
    status VARCHAR(50) NOT NULL DEFAULT 'auto',
    last_seen_run_id BIGINT NULL,
    resolved_by VARCHAR(255) NOT NULL DEFAULT '',
    resolved_at TIMESTAMP NULL DEFAULT NULL,
    resolution_detail JSON NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE KEY uk_identity_mapping (platform, project_path(255), git_identity_key(255)),
    INDEX idx_identity_status (status, platform),
    INDEX idx_identity_project (project_path(255)),
    INDEX idx_identity_last_seen (last_seen_run_id),
    CONSTRAINT fk_identity_last_seen_run FOREIGN KEY (last_seen_run_id) REFERENCES review_runs(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE IF EXISTS identity_mappings;
