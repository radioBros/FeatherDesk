-- FeatherDesk was called SimpleTickets up to 1.0.0, and that release created its
-- Postgres functions with an st_ prefix (a fresh database gets fd_). Rename them,
-- so every database has the same names and later migrations can refer to fd_.
-- Triggers point at functions by OID and keep working through the rename. The
-- two KB trigger functions call the rank function by name, so they are
-- re-created with the fd_ name. A fresh database only gets those two functions
-- re-created, with the same body. SQLite has no such functions.
--
-- Both engines: an install that never changed the product name still has the
-- SimpleTickets default stored in its branding; it becomes FeatherDesk. A name
-- the admin chose is kept.
@@sqlite
UPDATE settings SET value = json_set(value, '$.product_name', 'FeatherDesk')
    WHERE key = 'branding' AND json_extract(value, '$.product_name') = 'SimpleTickets';
@@end
@@postgres
UPDATE settings SET value = jsonb_set(value, '{product_name}', '"FeatherDesk"')
    WHERE key = 'branding' AND value->>'product_name' = 'SimpleTickets';

-- +StatementBegin
DO $$
DECLARE
    f TEXT;
BEGIN
    FOREACH f IN ARRAY ARRAY[
        'users_kind_immutable()', 'audit_log_append_only()', 'tickets_principals()',
        'ticket_events_append_only()', 'team_members_staff_only()', 'ticket_watchers_customer_org()',
        'kb_rank(text)', 'kb_article_visibility()', 'kb_category_visibility()',
        'project_members_staff_only()', 'tickets_project_seq()'
    ] LOOP
        IF to_regprocedure('st_' || f) IS NOT NULL AND to_regprocedure('fd_' || f) IS NULL THEN
            EXECUTE format('ALTER FUNCTION %s RENAME TO %I', 'st_' || f, 'fd_' || split_part(f, '(', 1));
        END IF;
    END LOOP;
END
$$;
-- +StatementEnd

-- +StatementBegin
CREATE OR REPLACE FUNCTION fd_kb_article_visibility() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF fd_kb_rank(NEW.visibility) < (SELECT fd_kb_rank(visibility) FROM kb_categories WHERE id = NEW.category_id) THEN
        RAISE EXCEPTION 'kb article visibility is wider than its category';
    END IF;
    RETURN NEW;
END
$$;
-- +StatementEnd

-- +StatementBegin
CREATE OR REPLACE FUNCTION fd_kb_category_visibility() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF EXISTS (SELECT 1 FROM kb_articles a WHERE a.category_id = NEW.id AND fd_kb_rank(a.visibility) < fd_kb_rank(NEW.visibility)) THEN
        RAISE EXCEPTION 'kb category visibility would be narrower than one of its articles';
    END IF;
    RETURN NEW;
END
$$;
-- +StatementEnd
@@end
