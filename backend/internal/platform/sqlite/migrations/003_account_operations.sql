-- Separate credential changes from ordinary metadata edits so in-flight operations
-- cannot overwrite credentials saved by a newer operation or user edit.
ALTER TABLE tokens ADD COLUMN credential_version INTEGER NOT NULL DEFAULT 1;
ALTER TABLE dreamina_accounts ADD COLUMN credential_version INTEGER NOT NULL DEFAULT 1;
ALTER TABLE token_refresh_attempts ADD COLUMN operation_key TEXT NOT NULL DEFAULT '';
ALTER TABLE dreamina_refresh_attempts ADD COLUMN operation_key TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX idx_token_attempt_operation_key ON token_refresh_attempts(operation_key) WHERE operation_key <> '';
CREATE UNIQUE INDEX idx_dreamina_attempt_operation_key ON dreamina_refresh_attempts(operation_key) WHERE operation_key <> '';
ALTER TABLE tokens ADD COLUMN refresh_failures INTEGER NOT NULL DEFAULT 0;
ALTER TABLE dreamina_accounts ADD COLUMN refresh_failures INTEGER NOT NULL DEFAULT 0;

-- Persist job dispatch, cancellation, action limits, and idempotent results.
ALTER TABLE jobs ADD COLUMN cancel_requested INTEGER NOT NULL DEFAULT 0;
ALTER TABLE jobs ADD COLUMN last_dispatched_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE job_items ADD COLUMN available_at INTEGER NOT NULL DEFAULT 0;
CREATE INDEX idx_jobs_dispatch ON jobs(target_kind, state, last_dispatched_at, created_at);
CREATE INDEX idx_job_items_target_state ON job_items(target_id, state, job_id);
CREATE INDEX idx_job_items_available ON job_items(state, available_at, job_id);
CREATE TABLE job_action_limits (
    target_kind TEXT NOT NULL,
    action TEXT NOT NULL,
    available_at INTEGER NOT NULL DEFAULT 0,
    in_flight INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY(target_kind, action)
);
UPDATE jobs SET cancel_requested = 1 WHERE state = 'cancelled';
ALTER TABLE idempotency_keys ADD COLUMN resource_type TEXT NOT NULL DEFAULT '';
ALTER TABLE idempotency_keys ADD COLUMN resource_id TEXT NOT NULL DEFAULT '';
ALTER TABLE idempotency_keys ADD COLUMN state TEXT NOT NULL DEFAULT 'completed';
ALTER TABLE idempotency_keys ADD COLUMN lease_expires_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE idempotency_keys ADD COLUMN owner_token TEXT NOT NULL DEFAULT '';
ALTER TABLE idempotency_keys ADD COLUMN response_headers TEXT NOT NULL DEFAULT '{}';
UPDATE idempotency_keys SET resource_type = 'job', resource_id = response_body, response_body = ''
WHERE status_code = 202 AND response_body GLOB 'job_*';
UPDATE idempotency_keys
SET resource_type = 'job', resource_id = json_extract(response_body, '$.data.job.id')
WHERE CASE WHEN json_valid(response_body) THEN json_extract(response_body, '$.data.job.id') ELSE '' END GLOB 'job_*';
UPDATE idempotency_keys SET response_headers = '{"Content-Type":["application/json; charset=utf-8"]}';
UPDATE idempotency_keys SET state = 'uncertain' WHERE status_code = 0;
CREATE INDEX idx_idempotency_resource ON idempotency_keys(resource_type, resource_id);

-- Store API Key hashes, metadata, usage timestamps, and audit identity.
CREATE TABLE api_keys (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    key_hash TEXT NOT NULL UNIQUE,
    key_masked TEXT NOT NULL,
    created_by TEXT REFERENCES admins(id) ON DELETE SET NULL,
    created_at INTEGER NOT NULL,
    deleted_at INTEGER,
    last_used_at INTEGER
);
CREATE INDEX idx_api_keys_active_created ON api_keys(created_at DESC) WHERE deleted_at IS NULL;

ALTER TABLE audit_logs ADD COLUMN api_key_id TEXT REFERENCES api_keys(id);
CREATE INDEX idx_audit_api_key_created ON audit_logs(api_key_id, created_at DESC) WHERE api_key_id IS NOT NULL;
