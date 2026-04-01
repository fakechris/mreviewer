package db

import (
	"context"
	"database/sql"
	"time"
)

type IdentityMapping struct {
	ID               int64          `json:"id"`
	Platform         string         `json:"platform"`
	ProjectPath      string         `json:"project_path"`
	GitIdentityKey   string         `json:"git_identity_key"`
	GitEmail         string         `json:"git_email"`
	GitName          string         `json:"git_name"`
	ObservedRole     string         `json:"observed_role"`
	PlatformUsername string         `json:"platform_username"`
	PlatformUserID   string         `json:"platform_user_id"`
	HeadSha          string         `json:"head_sha"`
	Confidence       float64        `json:"confidence"`
	Source           string         `json:"source"`
	Status           string         `json:"status"`
	LastSeenRunID    sql.NullInt64  `json:"last_seen_run_id"`
	ResolvedBy       string         `json:"resolved_by"`
	ResolvedAt       sql.NullTime   `json:"resolved_at"`
	ResolutionDetail NullRawMessage `json:"resolution_detail"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
}

type ListIdentityMappingsParams struct {
	Platform    string `json:"platform"`
	Status      string `json:"status"`
	ProjectPath string `json:"project_path"`
	LimitCount  int32  `json:"limit_count"`
}

type ListIdentityMappingsRow = IdentityMapping

type UpsertIdentityMappingParams struct {
	Platform         string
	ProjectPath      string
	GitIdentityKey   string
	GitEmail         string
	GitName          string
	ObservedRole     string
	PlatformUsername string
	PlatformUserID   string
	HeadSha          string
	Confidence       float64
	Source           string
	Status           string
	LastSeenRunID    sql.NullInt64
}

type ResolveIdentityMappingParams struct {
	ID               int64
	PlatformUsername string
	PlatformUserID   string
	ResolvedBy       string
	ResolutionDetail NullRawMessage
}

type GetIdentityMappingByIdentityKeyParams struct {
	Platform       string
	ProjectPath    string
	GitIdentityKey string
}

const upsertIdentityMapping = `
INSERT INTO identity_mappings (
	platform, project_path, git_identity_key, git_email, git_name, observed_role,
	platform_username, platform_user_id, head_sha, confidence, source, status, last_seen_run_id
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
	git_email = VALUES(git_email),
	git_name = VALUES(git_name),
	observed_role = VALUES(observed_role),
	head_sha = VALUES(head_sha),
	confidence = GREATEST(confidence, VALUES(confidence)),
	last_seen_run_id = VALUES(last_seen_run_id),
	platform_username = CASE WHEN status = 'manual' THEN platform_username ELSE VALUES(platform_username) END,
	platform_user_id = CASE WHEN status = 'manual' THEN platform_user_id ELSE VALUES(platform_user_id) END,
	source = CASE WHEN status = 'manual' THEN source ELSE VALUES(source) END,
	status = CASE WHEN status = 'manual' THEN status ELSE VALUES(status) END,
	updated_at = CURRENT_TIMESTAMP
`

func (q *Queries) UpsertIdentityMapping(ctx context.Context, arg UpsertIdentityMappingParams) error {
	_, err := q.db.ExecContext(ctx, upsertIdentityMapping,
		arg.Platform,
		arg.ProjectPath,
		arg.GitIdentityKey,
		arg.GitEmail,
		arg.GitName,
		arg.ObservedRole,
		arg.PlatformUsername,
		arg.PlatformUserID,
		arg.HeadSha,
		arg.Confidence,
		arg.Source,
		arg.Status,
		arg.LastSeenRunID,
	)
	return err
}

func (q *Queries) ListIdentityMappings(ctx context.Context, arg ListIdentityMappingsParams) ([]ListIdentityMappingsRow, error) {
	rows, err := q.db.QueryContext(ctx, `
SELECT
	id, platform, project_path, git_identity_key, git_email, git_name, observed_role,
	platform_username, platform_user_id, head_sha, confidence, source, status,
	last_seen_run_id, resolved_by, resolved_at, resolution_detail, created_at, updated_at
FROM identity_mappings
WHERE (? = '' OR platform = ?)
  AND (? = '' OR status = ?)
  AND (? = '' OR project_path LIKE CONCAT('%', ?, '%'))
ORDER BY updated_at DESC, id DESC
LIMIT ?
`, arg.Platform, arg.Platform, arg.Status, arg.Status, arg.ProjectPath, arg.ProjectPath, arg.LimitCount)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []ListIdentityMappingsRow
	for rows.Next() {
		var item ListIdentityMappingsRow
		if err := rows.Scan(
			&item.ID,
			&item.Platform,
			&item.ProjectPath,
			&item.GitIdentityKey,
			&item.GitEmail,
			&item.GitName,
			&item.ObservedRole,
			&item.PlatformUsername,
			&item.PlatformUserID,
			&item.HeadSha,
			&item.Confidence,
			&item.Source,
			&item.Status,
			&item.LastSeenRunID,
			&item.ResolvedBy,
			&item.ResolvedAt,
			&item.ResolutionDetail,
			&item.CreatedAt,
			&item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return items, rows.Err()
}

func (q *Queries) GetIdentityMapping(ctx context.Context, id int64) (IdentityMapping, error) {
	row := q.db.QueryRowContext(ctx, `
SELECT
	id, platform, project_path, git_identity_key, git_email, git_name, observed_role,
	platform_username, platform_user_id, head_sha, confidence, source, status,
	last_seen_run_id, resolved_by, resolved_at, resolution_detail, created_at, updated_at
FROM identity_mappings
WHERE id = ?
LIMIT 1
`, id)
	var item IdentityMapping
	err := row.Scan(
		&item.ID,
		&item.Platform,
		&item.ProjectPath,
		&item.GitIdentityKey,
		&item.GitEmail,
		&item.GitName,
		&item.ObservedRole,
		&item.PlatformUsername,
		&item.PlatformUserID,
		&item.HeadSha,
		&item.Confidence,
		&item.Source,
		&item.Status,
		&item.LastSeenRunID,
		&item.ResolvedBy,
		&item.ResolvedAt,
		&item.ResolutionDetail,
		&item.CreatedAt,
		&item.UpdatedAt,
	)
	return item, err
}

func (q *Queries) GetIdentityMappingByIdentityKey(ctx context.Context, arg GetIdentityMappingByIdentityKeyParams) (IdentityMapping, error) {
	row := q.db.QueryRowContext(ctx, `
SELECT
	id, platform, project_path, git_identity_key, git_email, git_name, observed_role,
	platform_username, platform_user_id, head_sha, confidence, source, status,
	last_seen_run_id, resolved_by, resolved_at, resolution_detail, created_at, updated_at
FROM identity_mappings
WHERE platform = ? AND project_path = ? AND git_identity_key = ?
LIMIT 1
`, arg.Platform, arg.ProjectPath, arg.GitIdentityKey)
	var item IdentityMapping
	err := row.Scan(
		&item.ID,
		&item.Platform,
		&item.ProjectPath,
		&item.GitIdentityKey,
		&item.GitEmail,
		&item.GitName,
		&item.ObservedRole,
		&item.PlatformUsername,
		&item.PlatformUserID,
		&item.HeadSha,
		&item.Confidence,
		&item.Source,
		&item.Status,
		&item.LastSeenRunID,
		&item.ResolvedBy,
		&item.ResolvedAt,
		&item.ResolutionDetail,
		&item.CreatedAt,
		&item.UpdatedAt,
	)
	return item, err
}

func (q *Queries) ResolveIdentityMapping(ctx context.Context, arg ResolveIdentityMappingParams) error {
	_, err := q.db.ExecContext(ctx, `
UPDATE identity_mappings
SET platform_username = ?,
    platform_user_id = ?,
    source = 'manual',
    status = 'manual',
    resolved_by = ?,
    resolved_at = CURRENT_TIMESTAMP,
    resolution_detail = ?,
    updated_at = CURRENT_TIMESTAMP
WHERE id = ?
`, arg.PlatformUsername, arg.PlatformUserID, arg.ResolvedBy, arg.ResolutionDetail, arg.ID)
	return err
}
