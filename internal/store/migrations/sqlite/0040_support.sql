-- Code generated from migrations/_templates for both engines (D-18). DO NOT EDIT. Engine: sqlite.
-- Support configuration (range 0040-0059).

CREATE TABLE business_calendars (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    timezone   TEXT NOT NULL DEFAULT 'UTC',
    week       JSONTEXT NOT NULL DEFAULT '{}',
    holidays   JSONTEXT NOT NULL DEFAULT '[]',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE teams (
    id                 TEXT PRIMARY KEY,
    name               TEXT NOT NULL,
    description        TEXT NOT NULL DEFAULT '',
    notification_email TEXT NULL,
    notify_mode        TEXT NOT NULL DEFAULT 'agents' CHECK (notify_mode IN ('agents', 'mailbox', 'both')),
    calendar_id        TEXT NULL REFERENCES business_calendars (id) ON DELETE SET NULL,
    email_alias        TEXT NULL,
    idp_group          TEXT NULL,
    created_at         TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at         TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX teams_name_uq ON teams (lower(name));

CREATE TABLE team_members (
    team_id    TEXT NOT NULL REFERENCES teams (id) ON DELETE CASCADE,
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    source     TEXT NOT NULL DEFAULT 'local' CHECK (source IN ('local', 'idp')),
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (team_id, user_id)
);
CREATE INDEX team_members_user_idx ON team_members (user_id);

CREATE TABLE categories (
    id               TEXT PRIMARY KEY,
    name             TEXT NOT NULL,
    default_team_id  TEXT NULL REFERENCES teams (id),
    default_priority TEXT NULL CHECK (default_priority IN ('low', 'normal', 'high', 'urgent')),
    active           BOOLEAN NOT NULL DEFAULT TRUE,
    sort             INTEGER NOT NULL DEFAULT 0,
    created_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);


-- Instance settings edited from the admin UI. SSO/OIDC secrets come only from env.
CREATE TABLE settings (
    key        TEXT PRIMARY KEY,
    value      JSONTEXT NOT NULL,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_by TEXT NULL
);
INSERT INTO settings (key, value) VALUES ('support', '{"tier_labels":{"1":"L1","2":"L2","3":"L3"},"self_registration":true,"auto_close_days":7,"reopen_window_days":7,"customer_urgent_allowed":false,"urgent_requires_supervisor":false,"triage_visible_to_agents":true,"kb_enabled":true,"kb_sitemap_enabled":false,"default_locale":"en","locales":["en","it","de","fr","es"]}');
INSERT INTO settings (key, value) VALUES ('branding', '{"product_name":"FeatherDesk","company_name":"","support_url":"","colors":{"primary":"#3F51B5","secondary":"#607D8B","accent":"#00BFA5"},"area_colors":{},"default_theme":"system","radius":"default","font":"system","login":{"title":"","subtitle":"","background_color":""},"portal":{"welcome_md":"","new_ticket_intro_md":""},"email":{"header_color":"#3F51B5","footer_md":""},"legal":{"privacy_url":"","terms_url":"","imprint_url":""},"powered_by":true,"custom_css":"","assets":{}}');
INSERT INTO settings (key, value) VALUES ('smtp', '{"host":"","port":587,"security":"starttls","username":"","password_enc":"","auth":"plain","helo":"","timeout_seconds":10,"allow_insecure_tls":false}');
INSERT INTO settings (key, value) VALUES ('email', '{"customer_from":{"name":"","address":"","reply_to":""},"staff_from":{"name":"","address":"","reply_to":""},"disabled_templates":[],"triage_mailbox":"","plus_addressing":false,"inbound_blocklist":[]}');
INSERT INTO settings (key, value) VALUES ('setup', '{"completed":false}');

CREATE TABLE sla_policies (
    priority               TEXT PRIMARY KEY CHECK (priority IN ('low', 'normal', 'high', 'urgent')),
    first_response_minutes INTEGER NOT NULL,
    resolution_minutes     INTEGER NOT NULL,
    calendar_mode          TEXT NOT NULL DEFAULT '24x7' CHECK (calendar_mode IN ('24x7', 'team')),
    updated_at             TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO sla_policies (priority, first_response_minutes, resolution_minutes) VALUES ('low', 1440, 7200);
INSERT INTO sla_policies (priority, first_response_minutes, resolution_minutes) VALUES ('normal', 480, 2880);
INSERT INTO sla_policies (priority, first_response_minutes, resolution_minutes) VALUES ('high', 120, 1440);
INSERT INTO sla_policies (priority, first_response_minutes, resolution_minutes) VALUES ('urgent', 30, 240);

CREATE TABLE canned_responses (
    id         TEXT PRIMARY KEY,
    title      TEXT NOT NULL,
    body_md    TEXT NOT NULL,
    team_id    TEXT NULL REFERENCES teams (id) ON DELETE CASCADE,
    created_by TEXT NULL REFERENCES users (id),
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE tags (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX tags_name_uq ON tags (lower(name));

CREATE TABLE ticket_tags (
    ticket_id TEXT NOT NULL REFERENCES tickets (id) ON DELETE CASCADE,
    tag_id    TEXT NOT NULL REFERENCES tags (id) ON DELETE CASCADE,
    PRIMARY KEY (ticket_id, tag_id)
);
CREATE INDEX ticket_tags_tag_idx ON ticket_tags (tag_id);

CREATE TABLE ticket_watchers (
    ticket_id  TEXT NOT NULL REFERENCES tickets (id) ON DELETE CASCADE,
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (ticket_id, user_id)
);
CREATE INDEX ticket_watchers_user_idx ON ticket_watchers (user_id);

CREATE TABLE saved_views (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    filter     JSONTEXT NOT NULL DEFAULT '{}',
    shared     BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX saved_views_user_idx ON saved_views (user_id);

-- team_members accepts staff users only.
-- +StatementBegin
CREATE TRIGGER team_members_staff_only_ins BEFORE INSERT ON team_members
WHEN (SELECT kind FROM users WHERE id = NEW.user_id) IS NOT 'staff'
BEGIN
    SELECT RAISE(ABORT, 'team_members accepts staff users only');
END;
-- +StatementEnd
-- +StatementBegin
CREATE TRIGGER team_members_staff_only_upd BEFORE UPDATE OF user_id ON team_members
WHEN (SELECT kind FROM users WHERE id = NEW.user_id) IS NOT 'staff'
BEGIN
    SELECT RAISE(ABORT, 'team_members accepts staff users only');
END;
-- +StatementEnd

-- A customer watcher (CC) must belong to the ticket's organization.
-- +StatementBegin
CREATE TRIGGER ticket_watchers_customer_org BEFORE INSERT ON ticket_watchers
WHEN (SELECT kind FROM users WHERE id = NEW.user_id) = 'customer'
 AND (SELECT organization_id FROM users WHERE id = NEW.user_id) IS NOT (SELECT organization_id FROM tickets WHERE id = NEW.ticket_id)
BEGIN
    SELECT RAISE(ABORT, 'a customer watcher must belong to the ticket organization');
END;
-- +StatementEnd
