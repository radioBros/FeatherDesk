-- Ticket links (M-8) and custom fields (I-9, M-9).

CREATE TABLE ticket_links (
    id             TEXT PRIMARY KEY,
    from_ticket_id TEXT NOT NULL REFERENCES tickets (id) ON DELETE CASCADE,
    to_ticket_id   TEXT NOT NULL REFERENCES tickets (id) ON DELETE CASCADE,
    type           TEXT NOT NULL CHECK (type IN ('parent_of', 'related_to', 'duplicate_of')),
    created_by     TEXT NULL REFERENCES users (id),
    created_at     {TS} NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ticket_links_self_ck CHECK (from_ticket_id <> to_ticket_id)
);
CREATE UNIQUE INDEX ticket_links_uq ON ticket_links (from_ticket_id, to_ticket_id, type);
CREATE INDEX ticket_links_to_idx ON ticket_links (to_ticket_id);
-- A ticket has at most one parent (M-8).
CREATE UNIQUE INDEX ticket_links_one_parent_uq ON ticket_links (to_ticket_id) WHERE type = 'parent_of';

CREATE TABLE custom_field_defs (
    id                TEXT PRIMARY KEY,
    key               TEXT NOT NULL,
    label             {JSON} NOT NULL DEFAULT '{}',
    type              TEXT NOT NULL CHECK (type IN ('text', 'textarea', 'number', 'date', 'select', 'multiselect', 'checkbox')),
    options           {JSON} NOT NULL DEFAULT '[]',
    required          BOOLEAN NOT NULL DEFAULT FALSE,
    customer_visible  BOOLEAN NOT NULL DEFAULT FALSE,
    customer_editable BOOLEAN NOT NULL DEFAULT FALSE,
    category_ids      {JSON} NULL,
    sort              INTEGER NOT NULL DEFAULT 0,
    active            BOOLEAN NOT NULL DEFAULT TRUE,
    created_at        {TS} NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at        {TS} NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT custom_field_defs_editable_ck CHECK (customer_editable = FALSE OR customer_visible = TRUE)
);
CREATE UNIQUE INDEX custom_field_defs_key_uq ON custom_field_defs (key);

CREATE TABLE ticket_field_values (
    ticket_id  TEXT NOT NULL REFERENCES tickets (id) ON DELETE CASCADE,
    field_id   TEXT NOT NULL REFERENCES custom_field_defs (id),
    value_text TEXT NULL,
    value_num  DOUBLE PRECISION NULL,
    value_date DATE NULL,
    value_json {JSON} NULL,
    updated_at {TS} NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (ticket_id, field_id)
);
CREATE INDEX ticket_field_values_text_idx ON ticket_field_values (field_id, value_text);
CREATE INDEX ticket_field_values_num_idx ON ticket_field_values (field_id, value_num);
CREATE INDEX ticket_field_values_date_idx ON ticket_field_values (field_id, value_date);
