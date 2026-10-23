-- Code generated from migrations/_templates for both engines (D-18). DO NOT EDIT. Engine: postgres.
-- Slack integration (D-21, INT-002; range 0170-0179).
--
-- The app credentials and the workspace connection live in the settings
-- document `slack` (secrets sealed). A route posts the events it selects to
-- one channel; project_ids and priorities filter ([] = all). slack_threads
-- remembers the first post about a ticket in a route's channel, so later
-- updates reply in its thread. Both go with their route or ticket.
CREATE TABLE slack_routes (
    id               TEXT PRIMARY KEY,
    channel_id       TEXT NOT NULL,
    channel_name     TEXT NOT NULL DEFAULT '',
    channel_private  BOOLEAN NOT NULL DEFAULT FALSE,
    events           JSONB NOT NULL DEFAULT '[]',
    project_ids      JSONB NOT NULL DEFAULT '[]',
    priorities       JSONB NOT NULL DEFAULT '[]',
    threads          BOOLEAN NOT NULL DEFAULT TRUE,
    include_body     BOOLEAN NOT NULL DEFAULT TRUE,
    include_internal BOOLEAN NOT NULL DEFAULT FALSE,
    active           BOOLEAN NOT NULL DEFAULT TRUE,
    last_error       TEXT NOT NULL DEFAULT '',
    last_error_at    TIMESTAMPTZ NULL,
    last_posted_at   TIMESTAMPTZ NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE slack_threads (
    route_id   TEXT NOT NULL REFERENCES slack_routes (id) ON DELETE CASCADE,
    ticket_id  TEXT NOT NULL REFERENCES tickets (id) ON DELETE CASCADE,
    channel_id TEXT NOT NULL,
    ts         TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (route_id, ticket_id)
);
CREATE INDEX slack_threads_ticket_idx ON slack_threads (ticket_id);
