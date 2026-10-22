-- Code generated from migrations/_templates for both engines (D-18). DO NOT EDIT. Engine: postgres.
-- Inbound email (range 0090-0099, D-16).

CREATE TABLE inbound_mailboxes (
    id                 TEXT PRIMARY KEY,
    name               TEXT NOT NULL,
    kind               TEXT NOT NULL CHECK (kind IN ('imap', 'webhook')),
    team_id            TEXT NULL REFERENCES teams (id),
    address            TEXT NOT NULL DEFAULT '',
    imap               JSONB NOT NULL DEFAULT '{}',
    webhook_token_hash TEXT NULL,
    hmac_secret_enc    TEXT NOT NULL DEFAULT '',
    active             BOOLEAN NOT NULL DEFAULT TRUE,
    last_ok_at         TIMESTAMPTZ NULL,
    last_error         TEXT NOT NULL DEFAULT '',
    created_at         TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX inbound_mailboxes_token_uq ON inbound_mailboxes (webhook_token_hash);

CREATE TABLE inbound_messages (
    id             TEXT PRIMARY KEY,
    mailbox_id     TEXT NOT NULL REFERENCES inbound_mailboxes (id) ON DELETE CASCADE,
    message_id     TEXT NOT NULL,
    from_addr      TEXT NOT NULL DEFAULT '',
    subject        TEXT NOT NULL DEFAULT '',
    status         TEXT NOT NULL CHECK (status IN ('processed', 'quarantined', 'failed', 'ignored')),
    reason         TEXT NOT NULL DEFAULT '',
    ticket_id      TEXT NULL REFERENCES tickets (id) ON DELETE SET NULL,
    message_row_id TEXT NULL,
    raw_blob_key   TEXT NOT NULL DEFAULT '',
    received_at    TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX inbound_messages_msgid_uq ON inbound_messages (mailbox_id, message_id);
CREATE INDEX inbound_messages_status_idx ON inbound_messages (status, received_at);
