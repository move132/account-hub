CREATE TABLE IF NOT EXISTS admins (
    id TEXT PRIMARY KEY,
    username TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS admin_sessions (
    id_hash TEXT PRIMARY KEY,
    admin_id TEXT NOT NULL,
    csrf_hash TEXT NOT NULL,
    expires_at INTEGER NOT NULL,
    created_at INTEGER NOT NULL,
    last_seen_at INTEGER NOT NULL,
    FOREIGN KEY (admin_id) REFERENCES admins(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_admin_sessions_expires_at ON admin_sessions(expires_at);

CREATE TABLE IF NOT EXISTS tokens (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL DEFAULT '',
    email TEXT NOT NULL DEFAULT '',
    access_token TEXT NOT NULL,
    session_token TEXT NOT NULL DEFAULT '',
    access_expires_at TEXT,
    status TEXT NOT NULL DEFAULT 'enabled' CHECK(status IN ('enabled', 'disabled')),
    check_state TEXT NOT NULL DEFAULT 'unchecked' CHECK(check_state IN ('unchecked', 'valid', 'invalid', 'error')),
    video_eligible INTEGER NOT NULL DEFAULT 0,
    note TEXT NOT NULL DEFAULT '',
    next_refresh_at INTEGER,
    last_checked_at INTEGER,
    last_refreshed_at INTEGER,
    last_error_code TEXT,
    last_error_message TEXT,
    version INTEGER NOT NULL DEFAULT 1,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_tokens_access_token ON tokens(access_token);
CREATE UNIQUE INDEX IF NOT EXISTS idx_tokens_session_token ON tokens(session_token) WHERE session_token <> '';
CREATE INDEX IF NOT EXISTS idx_tokens_email ON tokens(email);
CREATE INDEX IF NOT EXISTS idx_tokens_status_next_refresh ON tokens(status, next_refresh_at);
CREATE INDEX IF NOT EXISTS idx_tokens_created_at ON tokens(created_at DESC);

CREATE TABLE IF NOT EXISTS token_refresh_attempts (
    id TEXT PRIMARY KEY,
    token_id TEXT,
    target_label TEXT NOT NULL,
    operation TEXT NOT NULL CHECK(operation IN ('check', 'refresh')),
    status TEXT NOT NULL CHECK(status IN ('running', 'succeeded', 'failed')),
    error_code TEXT,
    error_message TEXT,
    started_at INTEGER NOT NULL,
    completed_at INTEGER,
    FOREIGN KEY (token_id) REFERENCES tokens(id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS idx_token_attempts_token_started ON token_refresh_attempts(token_id, started_at DESC);

CREATE TABLE IF NOT EXISTS dreamina_accounts (
    id TEXT PRIMARY KEY,
    email TEXT NOT NULL UNIQUE,
    password TEXT NOT NULL,
    session_id TEXT NOT NULL DEFAULT '',
    session_expires_at TEXT,
    status TEXT NOT NULL DEFAULT 'enabled' CHECK(status IN ('enabled', 'disabled')),
    check_state TEXT NOT NULL DEFAULT 'unchecked' CHECK(check_state IN ('unchecked', 'valid', 'invalid', 'error')),
    credit_balance TEXT NOT NULL DEFAULT '',
    note TEXT NOT NULL DEFAULT '',
    next_refresh_at INTEGER,
    last_checked_at INTEGER,
    last_refreshed_at INTEGER,
    last_error_code TEXT,
    last_error_message TEXT,
    version INTEGER NOT NULL DEFAULT 1,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_dreamina_session_id ON dreamina_accounts(session_id) WHERE session_id <> '';
CREATE INDEX IF NOT EXISTS idx_dreamina_status_next_refresh ON dreamina_accounts(status, next_refresh_at);
CREATE INDEX IF NOT EXISTS idx_dreamina_created_at ON dreamina_accounts(created_at DESC);

CREATE TABLE IF NOT EXISTS dreamina_refresh_attempts (
    id TEXT PRIMARY KEY,
    account_id TEXT,
    target_label TEXT NOT NULL,
    operation TEXT NOT NULL CHECK(operation IN ('check', 'refresh', 'credit', 'credit_claim')),
    status TEXT NOT NULL CHECK(status IN ('running', 'succeeded', 'failed')),
    error_code TEXT,
    error_message TEXT,
    started_at INTEGER NOT NULL,
    completed_at INTEGER,
    FOREIGN KEY (account_id) REFERENCES dreamina_accounts(id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS idx_dreamina_attempts_account_started ON dreamina_refresh_attempts(account_id, started_at DESC);

CREATE TABLE IF NOT EXISTS jobs (
    id TEXT PRIMARY KEY,
    target_kind TEXT NOT NULL CHECK(target_kind IN ('token', 'dreamina')),
    action TEXT NOT NULL,
    state TEXT NOT NULL CHECK(state IN ('queued', 'running', 'succeeded', 'partially_succeeded', 'failed', 'cancelled')),
    total INTEGER NOT NULL DEFAULT 0,
    pending INTEGER NOT NULL DEFAULT 0,
    running INTEGER NOT NULL DEFAULT 0,
    succeeded INTEGER NOT NULL DEFAULT 0,
    failed INTEGER NOT NULL DEFAULT 0,
    cancelled INTEGER NOT NULL DEFAULT 0,
    created_by TEXT,
    created_at INTEGER NOT NULL,
    started_at INTEGER,
    completed_at INTEGER,
    FOREIGN KEY (created_by) REFERENCES admins(id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS idx_jobs_created_at ON jobs(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_jobs_state ON jobs(state);

CREATE TABLE IF NOT EXISTS job_items (
    id TEXT PRIMARY KEY,
    job_id TEXT NOT NULL,
    target_id TEXT,
    target_label TEXT NOT NULL DEFAULT '',
    payload_json TEXT NOT NULL DEFAULT '{}',
    state TEXT NOT NULL CHECK(state IN ('queued', 'running', 'succeeded', 'failed', 'cancelled')),
    attempts INTEGER NOT NULL DEFAULT 0,
    error_code TEXT,
    error_message TEXT,
    started_at INTEGER,
    completed_at INTEGER,
    FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_job_items_job_state ON job_items(job_id, state);

CREATE TABLE IF NOT EXISTS idempotency_keys (
    actor_id TEXT NOT NULL,
    route TEXT NOT NULL,
    key TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    status_code INTEGER NOT NULL,
    response_body TEXT NOT NULL,
    expires_at INTEGER NOT NULL,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (actor_id, route, key)
);
CREATE INDEX IF NOT EXISTS idx_idempotency_expires_at ON idempotency_keys(expires_at);

CREATE TABLE IF NOT EXISTS settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    value_type TEXT NOT NULL CHECK(value_type IN ('boolean', 'integer', 'string')),
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS audit_logs (
    id TEXT PRIMARY KEY,
    admin_id TEXT,
    action TEXT NOT NULL,
    target_kind TEXT NOT NULL,
    target_id TEXT,
    summary TEXT NOT NULL,
    request_id TEXT NOT NULL,
    remote_addr TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    FOREIGN KEY (admin_id) REFERENCES admins(id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS idx_audit_logs_created_at ON audit_logs(created_at DESC);
