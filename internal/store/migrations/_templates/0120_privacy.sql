-- Privacy / GDPR tooling (range 0120-0129, D-19 §C, PRV-001).
--
-- privacy_requests holds both data-subject requests:
--   export:  pending → processing → ready → expired (or failed). The ZIP lives in
--            blob storage under privacy/ (file_key) until expires_at.
--   erasure: pending → completed | rejected. Admin direct erasure writes a
--            completed row too, so the history is in one place.
-- Rows never carry PII beyond the user id: the subject's name and e-mail are
-- read from users at display time, so an erasure leaves nothing behind here.
CREATE TABLE privacy_requests (
    id               TEXT PRIMARY KEY,
    user_id          TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    type             TEXT NOT NULL CHECK (type IN ('export', 'erasure')),
    status           TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'ready', 'failed', 'expired', 'completed', 'rejected')),
    requested_by     TEXT NULL REFERENCES users (id) ON DELETE SET NULL,
    include_internal BOOLEAN NOT NULL DEFAULT FALSE,
    mode             TEXT NULL CHECK (mode IS NULL OR mode IN ('anonymize', 'anonymize_and_redact')),
    file_key         TEXT NOT NULL DEFAULT '',
    file_size        BIGINT NOT NULL DEFAULT 0,
    reason           TEXT NOT NULL DEFAULT '',
    error            TEXT NOT NULL DEFAULT '',
    decided_by       TEXT NULL REFERENCES users (id) ON DELETE SET NULL,
    decided_at       {TS} NULL,
    completed_at     {TS} NULL,
    expires_at       {TS} NULL,
    created_at       {TS} NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at       {TS} NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX privacy_requests_user_idx ON privacy_requests (user_id, created_at);
CREATE INDEX privacy_requests_status_idx ON privacy_requests (type, status, created_at);
CREATE INDEX privacy_requests_expires_idx ON privacy_requests (expires_at) WHERE status = 'ready';

-- Consent records (registration with a privacy policy URL configured). They
-- are kept after an erasure as proof of the lawful basis, with the IP cleared.
CREATE TABLE user_consents (
    id             TEXT PRIMARY KEY,
    user_id        TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind           TEXT NOT NULL DEFAULT 'privacy_policy' CHECK (kind = 'privacy_policy'),
    policy_url     TEXT NOT NULL,
    policy_version TEXT NOT NULL DEFAULT '',
    ip             TEXT NOT NULL DEFAULT '',
    accepted_at    {TS} NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX user_consents_user_idx ON user_consents (user_id, accepted_at);

-- Closed tickets anonymized by the retention policy (closed_tickets_action =
-- anonymize) are marked so the daily run and its dry-run preview skip them.
ALTER TABLE tickets ADD COLUMN anonymized_at {TS} NULL;
