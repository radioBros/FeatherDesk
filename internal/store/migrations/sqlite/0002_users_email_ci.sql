-- Code generated from migrations/_templates for both engines (D-18). DO NOT EDIT. Engine: sqlite.
-- Identity (range 0001-0019): e-mail addresses are unique case-insensitively
-- (DB-005 `users(lower(email)) UNIQUE`, DB-010). The application stores the
-- lower-cased form; this index is the database-level guard, so a write path
-- that forgets to normalise cannot create a second account for Foo@ / foo@.
-- users_email_uq stays for the exact-match lookups.

CREATE UNIQUE INDEX users_email_lower_uq ON users (lower(email));
