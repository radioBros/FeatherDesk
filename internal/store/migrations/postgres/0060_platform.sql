-- Code generated from migrations/_templates for both engines (D-18). DO NOT EDIT. Engine: postgres.
-- Platform (range 0060-0079). No jobs table: the queue is in-process (D-11);
-- deferred_jobs holds only jobs whose first in-memory attempt failed.

CREATE TABLE deferred_jobs (
    id           TEXT PRIMARY KEY,
    kind         TEXT NOT NULL,
    payload      JSONB NOT NULL DEFAULT '{}',
    attempts     INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 8,
    next_run_at  TIMESTAMPTZ NOT NULL,
    status       TEXT NOT NULL DEFAULT 'retrying' CHECK (status IN ('retrying', 'dead')),
    last_error   TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX deferred_jobs_status_next_run_idx ON deferred_jobs (status, next_run_at);

CREATE TABLE branding_assets (
    id           TEXT PRIMARY KEY,
    slot         TEXT NOT NULL CHECK (slot IN ('logo_light', 'logo_dark', 'icon', 'favicon', 'email_logo', 'login_background')),
    variant      TEXT NOT NULL DEFAULT '',
    blob_key     TEXT NOT NULL,
    content_type TEXT NOT NULL,
    width        INTEGER NOT NULL DEFAULT 0,
    height       INTEGER NOT NULL DEFAULT 0,
    size_bytes   BIGINT NOT NULL DEFAULT 0,
    sha256       TEXT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX branding_assets_slot_variant_uq ON branding_assets (slot, variant);
CREATE INDEX branding_assets_sha_idx ON branding_assets (sha256);

CREATE TABLE idempotency_keys (
    principal_id  TEXT NOT NULL,
    key           TEXT NOT NULL,
    request_hash  TEXT NOT NULL,
    status_code   INTEGER NOT NULL DEFAULT 0,
    response_body TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (principal_id, key)
);
CREATE INDEX idempotency_keys_created_idx ON idempotency_keys (created_at);

CREATE TABLE webhooks (
    id                   TEXT PRIMARY KEY,
    name                 TEXT NOT NULL DEFAULT '',
    url                  TEXT NOT NULL,
    secret_enc           TEXT NOT NULL,
    events               JSONB NOT NULL DEFAULT '[]',
    include_internal     BOOLEAN NOT NULL DEFAULT FALSE,
    active               BOOLEAN NOT NULL DEFAULT TRUE,
    consecutive_failures INTEGER NOT NULL DEFAULT 0,
    disabled_reason      TEXT NOT NULL DEFAULT '',
    created_at           TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE webhook_deliveries (
    id          TEXT PRIMARY KEY,
    webhook_id  TEXT NOT NULL REFERENCES webhooks (id) ON DELETE CASCADE,
    event_id    TEXT NOT NULL,
    event_type  TEXT NOT NULL,
    attempt     INTEGER NOT NULL DEFAULT 1,
    success     BOOLEAN NOT NULL DEFAULT FALSE,
    status_code INTEGER NULL,
    response_ms INTEGER NULL,
    error       TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX webhook_deliveries_webhook_idx ON webhook_deliveries (webhook_id, created_at);
