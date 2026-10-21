-- Code generated from migrations/_templates for both engines (D-18). DO NOT EDIT. Engine: sqlite.
-- Automation and assignment rules (range 0100-0109, D-19 §A, tickets_plan
-- TKT-042+): team assignment strategies, the agent away flag, trigger and
-- time-based rules, their idempotency markers, and macros.

-- Assignment strategy per team. rr_last_user_id is the round-robin pointer,
-- held in memory and written back lazily (coalesced, not per assignment).
ALTER TABLE teams ADD COLUMN assignment_strategy TEXT NOT NULL DEFAULT 'manual' CHECK (assignment_strategy IN ('manual', 'round_robin', 'least_busy'));
ALTER TABLE teams ADD COLUMN rr_last_user_id TEXT NULL;

-- Staff availability: away since this time (NULL = available). Away agents
-- are skipped by the assignment strategies, never by manual assignment.
ALTER TABLE users ADD COLUMN away_since TIMESTAMP NULL;

-- Trigger rules (event ticket.created | message.created | ticket.updated)
-- and time-based rules (event time), run in position order.
CREATE TABLE automation_rules (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL,
    event           TEXT NOT NULL CHECK (event IN ('ticket.created', 'message.created', 'ticket.updated', 'time')),
    match_mode      TEXT NOT NULL DEFAULT 'all' CHECK (match_mode IN ('all', 'any')),
    conditions      JSONTEXT NOT NULL DEFAULT '[]',
    actions         JSONTEXT NOT NULL DEFAULT '[]',
    enabled         BOOLEAN NOT NULL DEFAULT TRUE,
    stop_processing BOOLEAN NOT NULL DEFAULT FALSE,
    position        INTEGER NOT NULL DEFAULT 0,
    created_by      TEXT NULL REFERENCES users (id) ON DELETE SET NULL,
    created_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX automation_rules_position_idx ON automation_rules (position, id);

-- One row per source event the automation consumer acted on: a replayed
-- event (at-least-once queue) is never applied twice. Pruned after 30 days.
CREATE TABLE automation_runs (
    event_id   TEXT PRIMARY KEY,
    ticket_id  TEXT NOT NULL REFERENCES tickets (id) ON DELETE CASCADE,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX automation_runs_created_idx ON automation_runs (created_at);
CREATE INDEX automation_runs_ticket_idx ON automation_runs (ticket_id);

-- A time-based rule fires once per ticket until the ticket changes again:
-- fired_at is the ticket's updated_at right after the rule applied.
CREATE TABLE automation_time_runs (
    rule_id   TEXT NOT NULL REFERENCES automation_rules (id) ON DELETE CASCADE,
    ticket_id TEXT NOT NULL REFERENCES tickets (id) ON DELETE CASCADE,
    fired_at  TIMESTAMP NOT NULL,
    PRIMARY KEY (rule_id, ticket_id)
);
CREATE INDEX automation_time_runs_ticket_idx ON automation_time_runs (ticket_id);

-- Macros: personal (owner_id set) or shared (owner_id NULL, supervisor+).
CREATE TABLE macros (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    owner_id    TEXT NULL REFERENCES users (id) ON DELETE CASCADE,
    reply_kind  TEXT NOT NULL DEFAULT 'public' CHECK (reply_kind IN ('public', 'internal')),
    reply_md    TEXT NOT NULL DEFAULT '' CHECK (length(reply_md) <= 65536),
    status      TEXT NULL CHECK (status IN ('new', 'open', 'pending', 'on_hold', 'resolved', 'closed')),
    priority    TEXT NULL CHECK (priority IN ('low', 'normal', 'high', 'urgent')),
    add_tags    JSONTEXT NOT NULL DEFAULT '[]',
    remove_tags JSONTEXT NOT NULL DEFAULT '[]',
    assignee    TEXT NULL,
    created_by  TEXT NULL REFERENCES users (id) ON DELETE SET NULL,
    created_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX macros_owner_idx ON macros (owner_id);
