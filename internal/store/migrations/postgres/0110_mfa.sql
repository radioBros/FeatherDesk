-- Code generated from migrations/_templates for both engines (D-18). DO NOT EDIT. Engine: postgres.
-- Two-factor authentication for local accounts (range 0110-0119, D-19 §B,
-- auth_plan AUTH-035+). SSO users get MFA from their IdP; PATs are unaffected.
-- Nothing here holds a usable secret in clear: TOTP secrets are sealed with
-- crypto.Box (purpose "mfa-totp"), challenge tokens and recovery codes are
-- sha256, and e-mail codes are an HMAC under FD_SECRET_KEY.

-- One row per user who has (or is enrolling) a second factor.
CREATE TABLE user_mfa (
    user_id           TEXT PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    totp_secret_enc   TEXT NOT NULL DEFAULT '',
    totp_enabled_at   TIMESTAMPTZ NULL,
    -- Last accepted RFC 6238 time step: a code is accepted only for a larger step (replay guard).
    totp_last_counter INTEGER NULL,
    -- Secret shown by /me/mfa/totp/setup and not yet confirmed with a code.
    totp_pending_enc  TEXT NOT NULL DEFAULT '',
    totp_pending_at   TIMESTAMPTZ NULL,
    email_enabled     BOOLEAN NOT NULL DEFAULT FALSE,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Ten single-use recovery codes per user (sha256 of user id + code).
CREATE TABLE mfa_recovery_codes (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    code_hash  TEXT NOT NULL,
    used_at    TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX mfa_recovery_codes_user_hash_uq ON mfa_recovery_codes (user_id, code_hash);

-- Pending second steps of a login (purpose login) or a forced enrolment
-- (purpose enroll). Five minutes, five attempts, single use. No session
-- exists until a challenge is completed.
CREATE TABLE mfa_challenges (
    id               TEXT PRIMARY KEY,
    token_hash       TEXT NOT NULL,
    user_id          TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    purpose          TEXT NOT NULL CHECK (purpose IN ('login', 'enroll')),
    login_method     TEXT NOT NULL DEFAULT 'local',
    totp_pending_enc TEXT NOT NULL DEFAULT '',
    email_code_hash  TEXT NOT NULL DEFAULT '',
    email_expires_at TIMESTAMPTZ NULL,
    email_sent_at    TIMESTAMPTZ NULL,
    email_sends      INTEGER NOT NULL DEFAULT 0,
    attempts         INTEGER NOT NULL DEFAULT 0,
    ip               TEXT NOT NULL DEFAULT '',
    expires_at       TIMESTAMPTZ NOT NULL,
    used_at          TIMESTAMPTZ NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX mfa_challenges_token_uq ON mfa_challenges (token_hash);
CREATE INDEX mfa_challenges_user_idx ON mfa_challenges (user_id);
CREATE INDEX mfa_challenges_expires_idx ON mfa_challenges (expires_at);

-- Policy: who must enrol (none | staff | all). Edited through /admin/security.
INSERT INTO settings (key, value) VALUES ('security', '{"mfa_required":"none"}');
