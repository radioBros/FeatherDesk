-- Code generated from migrations/_templates for both engines (D-18). DO NOT EDIT. Engine: postgres.
-- Per-project ticket sequence and refs (D-20, PRJ-002).
--
-- tickets.project_id is NOT NULL. Existing tickets join the Default project
-- with project_seq = number, so the old "#1042" and the new "HD-1042" name
-- the same ticket. The Default project's counter starts at the global
-- ticket-number counter: while it is the only project, refs and numbers
-- stay equal. Other projects start at 1.
--
-- project_seq comes from counters row "ticket_seq:<project_id>", incremented
-- in the inserting transaction. The ticket service allocates it explicitly
-- (store.NextProjectSeq); an INSERT that leaves project_seq at 0 gets one
-- from the trigger below, so raw inserts (fixtures, seeds) can never collide.

ALTER TABLE tickets ADD COLUMN project_id TEXT NOT NULL DEFAULT '00000000-0000-7000-8000-000000000001' REFERENCES projects (id);
ALTER TABLE tickets ADD COLUMN project_seq BIGINT NOT NULL DEFAULT 0;
UPDATE tickets SET project_seq = number;
CREATE UNIQUE INDEX tickets_project_seq_uq ON tickets (project_id, project_seq);
CREATE INDEX tickets_project_status_updated_idx ON tickets (project_id, status, updated_at);

-- The global counter is always >= MAX(number) (numbers are allocated from it).
INSERT INTO counters (name, int_value, text_value)
SELECT 'ticket_seq:00000000-0000-7000-8000-000000000001', int_value, '' FROM counters WHERE name = 'ticket_number';

-- +StatementBegin
CREATE FUNCTION fd_tickets_project_seq() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.project_seq IS NULL OR NEW.project_seq = 0 THEN
        INSERT INTO counters (name, int_value, text_value) VALUES ('ticket_seq:' || NEW.project_id, 1, '')
        ON CONFLICT (name) DO UPDATE SET int_value = counters.int_value + 1
        RETURNING int_value INTO NEW.project_seq;
    END IF;
    RETURN NEW;
END
$$;
-- +StatementEnd
CREATE TRIGGER tickets_project_seq BEFORE INSERT ON tickets FOR EACH ROW EXECUTE FUNCTION fd_tickets_project_seq();
