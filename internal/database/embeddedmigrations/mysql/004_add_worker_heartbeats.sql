-- +goose Up

CREATE TABLE worker_heartbeats (
    worker_id VARCHAR(255) NOT NULL PRIMARY KEY,
    hostname VARCHAR(255) NOT NULL DEFAULT '',
    version VARCHAR(255) NOT NULL DEFAULT '',
    configured_concurrency INT NOT NULL DEFAULT 1,
    started_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_seen_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    INDEX idx_worker_heartbeats_last_seen (last_seen_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE IF EXISTS worker_heartbeats;
