-- Code generated from migrations/_templates for both engines (D-18). DO NOT EDIT. Engine: sqlite.
-- Projects (range 0140-0149, D-20, PRJ-001).
--
-- A project groups tickets under a key (KEY-123 refs), with its own
-- categories, team mapping, SLA overrides, mailboxes, KB categories and
-- portal branding. The migration creates one Default project (fixed id
-- 00000000-0000-7000-8000-000000000001, key HD, named after the branding
-- product name), makes every staff user a member and maps every team to it,
-- so a single-project install behaves exactly as before.

CREATE TABLE projects (
    id              TEXT PRIMARY KEY,
    key             TEXT NOT NULL CHECK (length(key) BETWEEN 2 AND 10),
    name            TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
    description     TEXT NOT NULL DEFAULT '',
    color           TEXT NOT NULL DEFAULT '',
    icon            TEXT NOT NULL DEFAULT '',
    active          BOOLEAN NOT NULL DEFAULT TRUE,
    default_team_id TEXT NULL REFERENCES teams (id) ON DELETE SET NULL,
    portal_visible  BOOLEAN NOT NULL DEFAULT TRUE,
    portal_title    TEXT NULL,
    portal_intro_md TEXT NULL,
    logo_asset_id   TEXT NULL REFERENCES branding_assets (id) ON DELETE SET NULL,
    primary_color   TEXT NULL,
    sort            INTEGER NOT NULL DEFAULT 0,
    created_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX projects_key_uq ON projects (key);

-- Staff membership (I-11): agents see only tickets of their projects.
-- Admins and supervisors see every project whatever their rows say.
CREATE TABLE project_members (
    project_id TEXT NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role       TEXT NOT NULL DEFAULT 'member' CHECK (role IN ('member', 'project_admin')),
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (project_id, user_id)
);
CREATE INDEX project_members_user_idx ON project_members (user_id);

-- Teams serving a project (routing, transfer, assignment and automation).
CREATE TABLE project_teams (
    project_id TEXT NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    team_id    TEXT NOT NULL REFERENCES teams (id) ON DELETE CASCADE,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (project_id, team_id)
);
CREATE INDEX project_teams_team_idx ON project_teams (team_id);

-- Projects an organization may open tickets in. No row = every
-- portal-visible project.
CREATE TABLE organization_projects (
    organization_id TEXT NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    project_id      TEXT NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    created_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (organization_id, project_id)
);
CREATE INDEX organization_projects_project_idx ON organization_projects (project_id);

-- The agent panel's project switcher, persisted per staff user (NULL = all).
ALTER TABLE users ADD COLUMN current_project_id TEXT NULL REFERENCES projects (id) ON DELETE SET NULL;

-- The Default project.
INSERT INTO projects (id, key, name, description, color, icon, active, portal_visible, sort, created_at, updated_at)
SELECT '00000000-0000-7000-8000-000000000001', 'HD',
       substr(COALESCE(NULLIF(trim(json_extract(value, '$.product_name')), ''), 'Help Desk'), 1, 100),
       '', '', '', TRUE, TRUE, 0, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
FROM settings WHERE key = 'branding';
INSERT INTO projects (id, key, name, created_at, updated_at)
SELECT '00000000-0000-7000-8000-000000000001', 'HD', 'Help Desk', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
WHERE NOT EXISTS (SELECT 1 FROM projects);
INSERT INTO project_members (project_id, user_id, role, created_at)
SELECT '00000000-0000-7000-8000-000000000001', id, 'member', CURRENT_TIMESTAMP FROM users WHERE kind = 'staff';
INSERT INTO project_teams (project_id, team_id, created_at)
SELECT '00000000-0000-7000-8000-000000000001', id, CURRENT_TIMESTAMP FROM teams;

-- project_members accepts staff users only.
-- +StatementBegin
CREATE TRIGGER project_members_staff_only_ins BEFORE INSERT ON project_members
WHEN (SELECT kind FROM users WHERE id = NEW.user_id) IS NOT 'staff'
BEGIN
    SELECT RAISE(ABORT, 'project_members accepts staff users only');
END;
-- +StatementEnd
-- +StatementBegin
CREATE TRIGGER project_members_staff_only_upd BEFORE UPDATE OF user_id ON project_members
WHEN (SELECT kind FROM users WHERE id = NEW.user_id) IS NOT 'staff'
BEGIN
    SELECT RAISE(ABORT, 'project_members accepts staff users only');
END;
-- +StatementEnd
