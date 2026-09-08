-- Rebuild the account table to allow multiple Sessions for one email.
-- Preserve history links before DROP TABLE applies ON DELETE SET NULL.
CREATE TEMP TABLE dreamina_attempt_links (id TEXT PRIMARY KEY, account_id TEXT NOT NULL);
INSERT INTO dreamina_attempt_links
SELECT id, account_id FROM dreamina_refresh_attempts WHERE account_id IS NOT NULL;

CREATE TABLE dreamina_accounts_new (
    id TEXT PRIMARY KEY,
    email TEXT NOT NULL,
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

INSERT INTO dreamina_accounts_new
    (id, email, password, session_id, session_expires_at, status, check_state,
     credit_balance, note, next_refresh_at, last_checked_at, last_refreshed_at,
     last_error_code, last_error_message, version, created_at, updated_at)
SELECT id, email, password, session_id, session_expires_at, status, check_state,
       credit_balance, note, next_refresh_at, last_checked_at, last_refreshed_at,
       last_error_code, last_error_message, version, created_at, updated_at
FROM dreamina_accounts;

DROP TABLE dreamina_accounts;
ALTER TABLE dreamina_accounts_new RENAME TO dreamina_accounts;

CREATE UNIQUE INDEX idx_dreamina_session_id ON dreamina_accounts(session_id) WHERE session_id <> '';
CREATE INDEX idx_dreamina_email ON dreamina_accounts(email);
CREATE INDEX idx_dreamina_status_next_refresh ON dreamina_accounts(status, next_refresh_at);
CREATE INDEX idx_dreamina_created_at ON dreamina_accounts(created_at DESC);

UPDATE dreamina_refresh_attempts
SET account_id = (SELECT account_id FROM dreamina_attempt_links WHERE id = dreamina_refresh_attempts.id)
WHERE id IN (SELECT id FROM dreamina_attempt_links);
DROP TABLE dreamina_attempt_links;
