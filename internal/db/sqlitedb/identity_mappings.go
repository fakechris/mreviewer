package sqlitedb

import (
	"context"

	"github.com/mreviewer/mreviewer/internal/db"
)

func (q *Queries) UpsertIdentityMapping(ctx context.Context, arg db.UpsertIdentityMappingParams) error {
	_, err := q.db.ExecContext(ctx, `
INSERT INTO identity_mappings (
	platform, project_path, git_identity_key, git_email, git_name, observed_role,
	platform_username, platform_user_id, head_sha, confidence, source, status, last_seen_run_id
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(platform, project_path, git_identity_key) DO UPDATE SET
	git_email = excluded.git_email,
	git_name = excluded.git_name,
	observed_role = excluded.observed_role,
	head_sha = excluded.head_sha,
	confidence = CASE
		WHEN identity_mappings.confidence > excluded.confidence THEN identity_mappings.confidence
		ELSE excluded.confidence
	END,
	last_seen_run_id = excluded.last_seen_run_id,
	platform_username = CASE WHEN identity_mappings.status = 'manual' THEN identity_mappings.platform_username ELSE excluded.platform_username END,
	platform_user_id = CASE WHEN identity_mappings.status = 'manual' THEN identity_mappings.platform_user_id ELSE excluded.platform_user_id END,
	source = CASE WHEN identity_mappings.status = 'manual' THEN identity_mappings.source ELSE excluded.source END,
	status = CASE WHEN identity_mappings.status = 'manual' THEN identity_mappings.status ELSE excluded.status END,
	updated_at = CURRENT_TIMESTAMP
`, arg.Platform, arg.ProjectPath, arg.GitIdentityKey, arg.GitEmail, arg.GitName, arg.ObservedRole, arg.PlatformUsername, arg.PlatformUserID, arg.HeadSha, arg.Confidence, arg.Source, arg.Status, arg.LastSeenRunID)
	return err
}

func (q *Queries) ListIdentityMappings(ctx context.Context, arg db.ListIdentityMappingsParams) ([]db.ListIdentityMappingsRow, error) {
	rows, err := q.db.QueryContext(ctx, `
SELECT
	id, platform, project_path, git_identity_key, git_email, git_name, observed_role,
	platform_username, platform_user_id, head_sha, confidence, source, status,
	last_seen_run_id, resolved_by, resolved_at, resolution_detail, created_at, updated_at
FROM identity_mappings
WHERE (? = '' OR platform = ?)
  AND (? = '' OR status = ?)
  AND (? = '' OR project_path LIKE '%' || ? || '%')
ORDER BY updated_at DESC, id DESC
LIMIT ?
`, arg.Platform, arg.Platform, arg.Status, arg.Status, arg.ProjectPath, arg.ProjectPath, arg.LimitCount)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []db.ListIdentityMappingsRow
	for rows.Next() {
		var item db.ListIdentityMappingsRow
		if err := rows.Scan(
			&item.ID, &item.Platform, &item.ProjectPath, &item.GitIdentityKey, &item.GitEmail, &item.GitName, &item.ObservedRole,
			&item.PlatformUsername, &item.PlatformUserID, &item.HeadSha, &item.Confidence, &item.Source, &item.Status,
			&item.LastSeenRunID, &item.ResolvedBy, &item.ResolvedAt, &item.ResolutionDetail, &item.CreatedAt, &item.UpdatedAt,
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

func (q *Queries) GetIdentityMapping(ctx context.Context, id int64) (db.IdentityMapping, error) {
	row := q.db.QueryRowContext(ctx, `
SELECT
	id, platform, project_path, git_identity_key, git_email, git_name, observed_role,
	platform_username, platform_user_id, head_sha, confidence, source, status,
	last_seen_run_id, resolved_by, resolved_at, resolution_detail, created_at, updated_at
FROM identity_mappings WHERE id = ? LIMIT 1
`, id)
	return scanIdentityMapping(row)
}

func (q *Queries) GetIdentityMappingByIdentityKey(ctx context.Context, arg db.GetIdentityMappingByIdentityKeyParams) (db.IdentityMapping, error) {
	row := q.db.QueryRowContext(ctx, `
SELECT
	id, platform, project_path, git_identity_key, git_email, git_name, observed_role,
	platform_username, platform_user_id, head_sha, confidence, source, status,
	last_seen_run_id, resolved_by, resolved_at, resolution_detail, created_at, updated_at
FROM identity_mappings
WHERE platform = ? AND project_path = ? AND git_identity_key = ?
LIMIT 1
`, arg.Platform, arg.ProjectPath, arg.GitIdentityKey)
	return scanIdentityMapping(row)
}

func (q *Queries) ResolveIdentityMapping(ctx context.Context, arg db.ResolveIdentityMappingParams) error {
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

func scanIdentityMapping(scanner interface{ Scan(dest ...any) error }) (db.IdentityMapping, error) {
	var item db.IdentityMapping
	err := scanner.Scan(
		&item.ID, &item.Platform, &item.ProjectPath, &item.GitIdentityKey, &item.GitEmail, &item.GitName, &item.ObservedRole,
		&item.PlatformUsername, &item.PlatformUserID, &item.HeadSha, &item.Confidence, &item.Source, &item.Status,
		&item.LastSeenRunID, &item.ResolvedBy, &item.ResolvedAt, &item.ResolutionDetail, &item.CreatedAt, &item.UpdatedAt,
	)
	return item, err
}
