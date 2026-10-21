-- Ticket search, Postgres only (DB-023). SQLite keeps tickets_fts unchanged.
--
-- Staff search used to match an expression GIN index on
-- (public_tsv || internal_tsv || staff_fields_tsv), and every tsvector sat
-- inline in a ~1.2 KB heap row. A typical term (a few thousand hits at 100k
-- tickets) then read one random heap page per hit just to fetch ticket_id
-- (3 400 pages of a 116 MB heap), which is 4-5x SQLite on a 128 MB host.
--
-- Now the table stores public_tsv plus one combined staff_tsv (public ∪
-- internal ∪ staff fields, written by store.IndexTicket), each with a plain
-- GIN index. The combined row is wider than the 2 KB toast threshold, and
-- toast_tuple_target = 128 then moves both tsvectors out of line, so the heap
-- holds only ticket_id + toast pointers (~11 MB at 100k tickets) and stays in
-- cache. Rows still under ~2 KB (tickets with very little text) stay inline.
--
-- Rebuilt as a new table rather than ALTERed in place, so the heap is compact
-- at once (an UPDATE would leave every old wide tuple behind until VACUUM FULL).
@@postgres
CREATE TABLE ticket_search_0131 (
    ticket_id  TEXT NOT NULL,
    public_tsv TSVECTOR NOT NULL,
    staff_tsv  TSVECTOR NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ticket_search_0131_pkey PRIMARY KEY (ticket_id),
    CONSTRAINT ticket_search_0131_ticket_id_fkey FOREIGN KEY (ticket_id) REFERENCES tickets (id) ON DELETE CASCADE
) WITH (toast_tuple_target = 128);
INSERT INTO ticket_search_0131 (ticket_id, public_tsv, staff_tsv, updated_at)
    SELECT ticket_id, public_tsv, public_tsv || internal_tsv || staff_fields_tsv, updated_at FROM ticket_search;
DROP TABLE ticket_search;
ALTER TABLE ticket_search_0131 RENAME TO ticket_search;
ALTER INDEX ticket_search_0131_pkey RENAME TO ticket_search_pkey;
ALTER TABLE ticket_search RENAME CONSTRAINT ticket_search_0131_ticket_id_fkey TO ticket_search_ticket_id_fkey;
-- Customer search matches public_tsv only, so internal-only words never leak (I-2, I-9).
CREATE INDEX ticket_search_public_gin ON ticket_search USING GIN (public_tsv);
-- Staff search: same semantics as FTS5 MATCH without a column filter.
CREATE INDEX ticket_search_staff_gin ON ticket_search USING GIN (staff_tsv);
@@end
