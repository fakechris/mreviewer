-- +goose Up

ALTER TABLE review_findings
    ADD COLUMN range_start_kind VARCHAR(32) NULL AFTER new_line,
    ADD COLUMN range_start_old_line INT NULL AFTER range_start_kind,
    ADD COLUMN range_start_new_line INT NULL AFTER range_start_old_line,
    ADD COLUMN range_end_kind VARCHAR(32) NULL AFTER range_start_new_line,
    ADD COLUMN range_end_old_line INT NULL AFTER range_end_kind,
    ADD COLUMN range_end_new_line INT NULL AFTER range_end_old_line;

-- +goose Down

ALTER TABLE review_findings
    DROP COLUMN range_end_new_line,
    DROP COLUMN range_end_old_line,
    DROP COLUMN range_end_kind,
    DROP COLUMN range_start_new_line,
    DROP COLUMN range_start_old_line,
    DROP COLUMN range_start_kind;
