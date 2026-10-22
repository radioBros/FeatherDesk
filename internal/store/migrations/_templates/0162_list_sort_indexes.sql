-- Sorted lists. Directory: GET /agent/customers (?sort=name, email) and
-- /admin/staff (?sort=name) page by (lower(column), id) within one kind.
-- Without these indexes every page sorted all users of the kind.
-- Organizations by name are served by organizations_name_uq (lower(name) is
-- unique, so the id tie-break never has to sort more than one row).
CREATE INDEX users_kind_name_idx ON users (kind, lower(name), id);
CREATE INDEX users_kind_email_idx ON users (kind, lower(email), id);

-- GET /admin/inbound/messages pages by (received_at, id) in either direction
-- (?sort=received_at); the only index on received_at led with status.
CREATE INDEX inbound_messages_received_idx ON inbound_messages (received_at, id);
