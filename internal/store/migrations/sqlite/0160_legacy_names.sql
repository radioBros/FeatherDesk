-- Code generated from migrations/_templates for both engines (D-18). DO NOT EDIT. Engine: sqlite.
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
UPDATE settings SET value = json_set(value, '$.product_name', 'FeatherDesk')
    WHERE key = 'branding' AND json_extract(value, '$.product_name') = 'SimpleTickets';
