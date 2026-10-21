-- In-app notification centre (range 0130-0139, D-19 §D, NOT-036).
-- One row per (event, recipient). Written by the notify consumer in one
-- transaction per event; event_id makes a replayed event a no-op. actor_id has
-- no foreign key: system actors (automation) are not always users rows.
-- Pruned to 90 days and 500 rows per user by the notify scheduler job.

CREATE TABLE notifications (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    type       TEXT NOT NULL,
    ticket_id  TEXT NULL REFERENCES tickets (id) ON DELETE CASCADE,
    actor_id   TEXT NULL,
    event_id   TEXT NOT NULL DEFAULT '',
    params     {JSON} NOT NULL DEFAULT '{}',
    read_at    {TS} NULL,
    created_at {TS} NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX notifications_event_user_uq ON notifications (event_id, user_id, type);
CREATE INDEX notifications_user_idx ON notifications (user_id, id);
CREATE INDEX notifications_unread_idx ON notifications (user_id, read_at);
CREATE INDEX notifications_ticket_idx ON notifications (ticket_id);
CREATE INDEX notifications_created_idx ON notifications (created_at);
