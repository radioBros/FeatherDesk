-- Project scoping of support configuration (D-20, PRJ-003).
--
-- Categories and inbound mailboxes belong to exactly one project (existing
-- rows join the Default project). KB categories, automation rules, macros
-- and canned responses are project-scoped or global (NULL). Custom fields
-- may be limited to projects (project_ids, NULL = all), webhooks may filter
-- on projects (project_ids, [] = all). SLA policies gain a project: rows
-- with project_id NULL are the global defaults every project falls back to.

@@sqlite
ALTER TABLE categories ADD COLUMN project_id TEXT NOT NULL DEFAULT '00000000-0000-7000-8000-000000000001';
ALTER TABLE inbound_mailboxes ADD COLUMN project_id TEXT NOT NULL DEFAULT '00000000-0000-7000-8000-000000000001';
@@end
@@postgres
ALTER TABLE categories ADD COLUMN project_id TEXT NOT NULL DEFAULT '00000000-0000-7000-8000-000000000001' REFERENCES projects (id);
ALTER TABLE inbound_mailboxes ADD COLUMN project_id TEXT NOT NULL DEFAULT '00000000-0000-7000-8000-000000000001' REFERENCES projects (id);
@@end
CREATE INDEX categories_project_idx ON categories (project_id, sort);
CREATE INDEX inbound_mailboxes_project_idx ON inbound_mailboxes (project_id);

ALTER TABLE kb_categories ADD COLUMN project_id TEXT NULL REFERENCES projects (id);
CREATE INDEX kb_categories_project_idx ON kb_categories (project_id);
ALTER TABLE custom_field_defs ADD COLUMN project_ids {JSON} NULL;
ALTER TABLE automation_rules ADD COLUMN project_id TEXT NULL REFERENCES projects (id) ON DELETE CASCADE;
ALTER TABLE macros ADD COLUMN project_id TEXT NULL REFERENCES projects (id) ON DELETE CASCADE;
ALTER TABLE canned_responses ADD COLUMN project_id TEXT NULL REFERENCES projects (id) ON DELETE CASCADE;
CREATE INDEX canned_responses_project_idx ON canned_responses (project_id);
ALTER TABLE webhooks ADD COLUMN project_ids {JSON} NOT NULL DEFAULT '[]';

-- sla_policies: (project_id NULL, priority). The primary key moves to a
-- surrogate id; two partial unique indexes keep one global row and one
-- override per project and priority. Column order is the same on both
-- engines (TestSchemaParity): the old columns, then id, then project_id.
@@sqlite
CREATE TABLE sla_policies_new (
    priority               TEXT NOT NULL CHECK (priority IN ('low', 'normal', 'high', 'urgent')),
    first_response_minutes INTEGER NOT NULL,
    resolution_minutes     INTEGER NOT NULL,
    calendar_mode          TEXT NOT NULL DEFAULT '24x7' CHECK (calendar_mode IN ('24x7', 'team')),
    updated_at             TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    id                     TEXT PRIMARY KEY,
    project_id             TEXT NULL REFERENCES projects (id) ON DELETE CASCADE
);
INSERT INTO sla_policies_new (priority, first_response_minutes, resolution_minutes, calendar_mode, updated_at, id, project_id)
SELECT priority, first_response_minutes, resolution_minutes, calendar_mode, updated_at, 'sla-global-' || priority, NULL FROM sla_policies;
DROP TABLE sla_policies;
ALTER TABLE sla_policies_new RENAME TO sla_policies;
@@end
@@postgres
ALTER TABLE sla_policies ADD COLUMN id TEXT NULL;
UPDATE sla_policies SET id = 'sla-global-' || priority;
ALTER TABLE sla_policies ALTER COLUMN id SET NOT NULL;
ALTER TABLE sla_policies DROP CONSTRAINT sla_policies_pkey;
ALTER TABLE sla_policies ADD PRIMARY KEY (id);
ALTER TABLE sla_policies ADD COLUMN project_id TEXT NULL REFERENCES projects (id) ON DELETE CASCADE;
@@end
CREATE UNIQUE INDEX sla_policies_global_uq ON sla_policies (priority) WHERE project_id IS NULL;
CREATE UNIQUE INDEX sla_policies_project_uq ON sla_policies (project_id, priority) WHERE project_id IS NOT NULL;

@@sqlite
-- Foreign keys of the NOT NULL project columns (see 0141).
-- +StatementBegin
CREATE TRIGGER categories_project_fk_ins BEFORE INSERT ON categories
WHEN NOT EXISTS (SELECT 1 FROM projects WHERE id = NEW.project_id)
BEGIN
    SELECT RAISE(ABORT, 'categories.project_id must reference projects');
END;
-- +StatementEnd
-- +StatementBegin
CREATE TRIGGER categories_project_fk_upd BEFORE UPDATE OF project_id ON categories
WHEN NOT EXISTS (SELECT 1 FROM projects WHERE id = NEW.project_id)
BEGIN
    SELECT RAISE(ABORT, 'categories.project_id must reference projects');
END;
-- +StatementEnd
-- +StatementBegin
CREATE TRIGGER inbound_mailboxes_project_fk_ins BEFORE INSERT ON inbound_mailboxes
WHEN NOT EXISTS (SELECT 1 FROM projects WHERE id = NEW.project_id)
BEGIN
    SELECT RAISE(ABORT, 'inbound_mailboxes.project_id must reference projects');
END;
-- +StatementEnd
-- +StatementBegin
CREATE TRIGGER inbound_mailboxes_project_fk_upd BEFORE UPDATE OF project_id ON inbound_mailboxes
WHEN NOT EXISTS (SELECT 1 FROM projects WHERE id = NEW.project_id)
BEGIN
    SELECT RAISE(ABORT, 'inbound_mailboxes.project_id must reference projects');
END;
-- +StatementEnd
-- +StatementBegin
CREATE TRIGGER projects_config_restrict BEFORE DELETE ON projects
WHEN EXISTS (SELECT 1 FROM categories WHERE project_id = OLD.id) OR EXISTS (SELECT 1 FROM inbound_mailboxes WHERE project_id = OLD.id)
BEGIN
    SELECT RAISE(ABORT, 'project has categories or mailboxes');
END;
-- +StatementEnd
@@end
