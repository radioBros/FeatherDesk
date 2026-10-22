-- Code generated from migrations/_templates for both engines (D-18). DO NOT EDIT. Engine: sqlite.
-- Ticket search, SQLite only. Postgres keeps ticket_search unchanged.
--
-- tickets_fts used to get a fresh rowid on every write and was looked up by its
-- UNINDEXED ticket_id column, so replacing one ticket's entry scanned the whole
-- index: 76 ms p50 at 100k tickets, on the single writer. The rowid is now the
-- ticket number (unique, never changes), and store.IndexTicket replaces an
-- entry by rowid.
--
-- The index is rebuilt with its current text, so search keeps working through
-- the upgrade without a reindex. One entry per ticket is kept (the newest);
-- entries of tickets that no longer exist are dropped.
CREATE TABLE fts_rebuild_0161 (
    number            INTEGER PRIMARY KEY,
    ticket_id         TEXT NOT NULL,
    public_text       TEXT,
    internal_text     TEXT,
    staff_fields_text TEXT
);
INSERT OR IGNORE INTO fts_rebuild_0161 (number, ticket_id, public_text, internal_text, staff_fields_text)
    SELECT t.number, f.ticket_id, f.public_text, f.internal_text, f.staff_fields_text
    FROM tickets_fts f JOIN tickets t ON t.id = f.ticket_id
    ORDER BY f.rowid DESC;
DROP TABLE tickets_fts;
CREATE VIRTUAL TABLE tickets_fts USING fts5(
    ticket_id UNINDEXED,
    public_text,
    internal_text,
    staff_fields_text,
    tokenize = 'unicode61 remove_diacritics 2'
);
INSERT INTO tickets_fts (rowid, ticket_id, public_text, internal_text, staff_fields_text)
    SELECT number, ticket_id, public_text, internal_text, staff_fields_text FROM fts_rebuild_0161;
DROP TABLE fts_rebuild_0161;
