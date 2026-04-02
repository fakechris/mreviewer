-- +goose Up
ALTER TABLE review_runs ADD COLUMN scope_json JSON DEFAULT NULL AFTER idempotency_key;

-- +goose Down
ALTER TABLE review_runs DROP COLUMN scope_json;
