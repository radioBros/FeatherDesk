-- Tickets core (range 0020-0039): counters, tickets, messages, attachments,
-- the append-only event log (also the queue outbox, D-11) and the search index.

CREATE TABLE counters (
    name       TEXT PRIMARY KEY,
    int_value  BIGINT NOT NULL DEFAULT 0,
    text_value TEXT NOT NULL DEFAULT ''
);
INSERT INTO counters (name, int_value, text_value) VALUES ('ticket_number', 1000, '');
INSERT INTO counters (name, int_value, text_value) VALUES ('dispatch_cursor', 0, '');

CREATE TABLE tickets (
    id                        TEXT PRIMARY KEY,
    number                    BIGINT NOT NULL,
    subject                   TEXT NOT NULL CHECK (length(subject) BETWEEN 1 AND 200),
    status                    TEXT NOT NULL DEFAULT 'new' CHECK (status IN ('new', 'open', 'pending', 'on_hold', 'resolved', 'closed')),
    priority                  TEXT NOT NULL DEFAULT 'normal' CHECK (priority IN ('low', 'normal', 'high', 'urgent')),
    tier                      INTEGER NOT NULL DEFAULT 1 CHECK (tier BETWEEN 1 AND 3),
@@sqlite
    team_id                   TEXT NULL REFERENCES teams (id),
    category_id               TEXT NULL REFERENCES categories (id),
@@end
@@postgres
    team_id                   TEXT NULL,
    category_id               TEXT NULL,
@@end
    assignee_id               TEXT NULL REFERENCES users (id),
    requester_id              TEXT NOT NULL REFERENCES users (id),
    organization_id           TEXT NULL REFERENCES organizations (id),
    channel                   TEXT NOT NULL CHECK (channel IN ('portal', 'agent', 'api', 'email')),
    first_response_due_at     {TS} NULL,
    resolution_due_at         {TS} NULL,
    first_responded_at        {TS} NULL,
    resolved_at               {TS} NULL,
    closed_at                 {TS} NULL,
    sla_paused_at             {TS} NULL,
    sla_paused_minutes        INTEGER NOT NULL DEFAULT 0,
    sla_state                 {JSON} NOT NULL DEFAULT '{}',
    last_customer_activity_at {TS} NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_staff_activity_at    {TS} NULL,
    followup_of_id            TEXT NULL REFERENCES tickets (id),
    csat_score                INTEGER NULL CHECK (csat_score BETWEEN 1 AND 5),
    csat_comment              TEXT NULL,
    created_at                {TS} NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at                {TS} NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX tickets_number_uq ON tickets (number);
CREATE INDEX tickets_status_updated_idx ON tickets (status, updated_at);
CREATE INDEX tickets_team_status_updated_idx ON tickets (team_id, status, updated_at);
CREATE INDEX tickets_assignee_status_idx ON tickets (assignee_id, status);
CREATE INDEX tickets_requester_idx ON tickets (requester_id, updated_at);
CREATE INDEX tickets_org_idx ON tickets (organization_id, updated_at);
CREATE INDEX tickets_updated_idx ON tickets (updated_at);
CREATE INDEX tickets_resolved_idx ON tickets (status, resolved_at);

CREATE TABLE ticket_messages (
    id                   TEXT PRIMARY KEY,
    ticket_id            TEXT NOT NULL REFERENCES tickets (id) ON DELETE CASCADE,
    author_id            TEXT NULL REFERENCES users (id),
    kind                 TEXT NOT NULL CHECK (kind IN ('public', 'internal', 'system')),
    channel              TEXT NOT NULL DEFAULT 'portal' CHECK (channel IN ('portal', 'agent', 'api', 'email')),
    body_md              TEXT NOT NULL CHECK (length(body_md) <= 65536),
    inbound_message_id   TEXT NULL,
    moved_from_ticket_id TEXT NULL,
    split_to_ticket_id   TEXT NULL,
    created_at           {TS} NOT NULL DEFAULT CURRENT_TIMESTAMP,
    edited_at            {TS} NULL
);
CREATE INDEX ticket_messages_ticket_idx ON ticket_messages (ticket_id, created_at);

CREATE TABLE attachments (
    id           TEXT PRIMARY KEY,
    ticket_id    TEXT NULL REFERENCES tickets (id) ON DELETE CASCADE,
    message_id   TEXT NULL REFERENCES ticket_messages (id) ON DELETE CASCADE,
    uploader_id  TEXT NOT NULL REFERENCES users (id),
    filename     TEXT NOT NULL,
    content_type TEXT NOT NULL,
    size_bytes   BIGINT NOT NULL,
    sha256       TEXT NOT NULL,
    storage_key  TEXT NOT NULL,
    created_at   {TS} NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX attachments_message_idx ON attachments (message_id);
CREATE INDEX attachments_ticket_idx ON attachments (ticket_id);
CREATE INDEX attachments_drafts_idx ON attachments (created_at) WHERE message_id IS NULL;

-- Append-only event log (M-5). id is a UUIDv7, so ORDER BY id is time order and
-- doubles as the dispatcher cursor / WebSocket resume position.
CREATE TABLE ticket_events (
    id         TEXT PRIMARY KEY,
    ticket_id  TEXT NOT NULL REFERENCES tickets (id),
    actor_id   TEXT NULL,
    type       TEXT NOT NULL,
    data       {JSON} NOT NULL DEFAULT '{}',
    snapshot   {JSON} NOT NULL DEFAULT '{}',
    created_at {TS} NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX ticket_events_ticket_idx ON ticket_events (ticket_id, id);

@@sqlite
-- M-3: the requester is a customer; M-4 (partial): an assignee is staff.
-- Team membership and tier of the assignee are checked by the ticket service.
-- +StatementBegin
CREATE TRIGGER tickets_requester_customer_ins BEFORE INSERT ON tickets
WHEN (SELECT kind FROM users WHERE id = NEW.requester_id) IS NOT 'customer'
BEGIN
    SELECT RAISE(ABORT, 'tickets.requester_id must be a customer');
END;
-- +StatementEnd
-- +StatementBegin
CREATE TRIGGER tickets_requester_customer_upd BEFORE UPDATE OF requester_id ON tickets
WHEN (SELECT kind FROM users WHERE id = NEW.requester_id) IS NOT 'customer'
BEGIN
    SELECT RAISE(ABORT, 'tickets.requester_id must be a customer');
END;
-- +StatementEnd
-- +StatementBegin
CREATE TRIGGER tickets_assignee_staff_ins BEFORE INSERT ON tickets
WHEN NEW.assignee_id IS NOT NULL AND (SELECT kind FROM users WHERE id = NEW.assignee_id) IS NOT 'staff'
BEGIN
    SELECT RAISE(ABORT, 'tickets.assignee_id must be staff');
END;
-- +StatementEnd
-- +StatementBegin
CREATE TRIGGER tickets_assignee_staff_upd BEFORE UPDATE OF assignee_id ON tickets
WHEN NEW.assignee_id IS NOT NULL AND (SELECT kind FROM users WHERE id = NEW.assignee_id) IS NOT 'staff'
BEGIN
    SELECT RAISE(ABORT, 'tickets.assignee_id must be staff');
END;
-- +StatementEnd

-- +StatementBegin
CREATE TRIGGER ticket_events_no_update BEFORE UPDATE ON ticket_events
BEGIN
    SELECT RAISE(ABORT, 'ticket_events is append-only');
END;
-- +StatementEnd
-- +StatementBegin
CREATE TRIGGER ticket_events_no_delete BEFORE DELETE ON ticket_events
WHEN NOT EXISTS (SELECT 1 FROM purge_allow WHERE tbl = 'ticket_events' AND key IN ('*', OLD.ticket_id))
BEGIN
    SELECT RAISE(ABORT, 'ticket_events is append-only');
END;
-- +StatementEnd

-- Search index, maintained asynchronously by the index worker (D-11). Customer
-- search matches public_text only, so internal-only words never leak (I-2, I-9).
CREATE VIRTUAL TABLE tickets_fts USING fts5(
    ticket_id UNINDEXED,
    public_text,
    internal_text,
    staff_fields_text,
    tokenize = 'unicode61 remove_diacritics 2'
);
@@end
@@postgres
-- M-3: the requester is a customer; M-4 (partial): an assignee is staff.
-- Team membership and tier of the assignee are checked by the ticket service.
-- +StatementBegin
CREATE FUNCTION fd_tickets_principals() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF (SELECT kind FROM users WHERE id = NEW.requester_id) IS DISTINCT FROM 'customer' THEN
        RAISE EXCEPTION 'tickets.requester_id must be a customer';
    END IF;
    IF NEW.assignee_id IS NOT NULL AND (SELECT kind FROM users WHERE id = NEW.assignee_id) IS DISTINCT FROM 'staff' THEN
        RAISE EXCEPTION 'tickets.assignee_id must be staff';
    END IF;
    RETURN NEW;
END
$$;
-- +StatementEnd
CREATE TRIGGER tickets_principals BEFORE INSERT OR UPDATE OF requester_id, assignee_id ON tickets FOR EACH ROW EXECUTE FUNCTION fd_tickets_principals();

-- +StatementBegin
CREATE FUNCTION fd_ticket_events_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' AND EXISTS (SELECT 1 FROM purge_allow WHERE tbl = 'ticket_events' AND key IN ('*', OLD.ticket_id)) THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'ticket_events is append-only';
END
$$;
-- +StatementEnd
CREATE TRIGGER ticket_events_append_only BEFORE UPDATE OR DELETE ON ticket_events FOR EACH ROW EXECUTE FUNCTION fd_ticket_events_append_only();

-- Search index, maintained asynchronously by the index worker (D-11). Customer
-- search matches public_tsv only, so internal-only words never leak (I-2, I-9).
CREATE TABLE ticket_search (
    ticket_id        TEXT PRIMARY KEY REFERENCES tickets (id) ON DELETE CASCADE,
    public_tsv       TSVECTOR NOT NULL,
    internal_tsv     TSVECTOR NOT NULL,
    staff_fields_tsv TSVECTOR NOT NULL,
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX ticket_search_public_gin ON ticket_search USING GIN (public_tsv);
-- Staff search matches across all three columns at once (same semantics as FTS5 without a column filter).
CREATE INDEX ticket_search_all_gin ON ticket_search USING GIN ((public_tsv || internal_tsv || staff_fields_tsv));
@@end
