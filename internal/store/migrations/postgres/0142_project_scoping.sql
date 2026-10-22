-- Code generated from migrations/_templates for both engines (D-18). DO NOT EDIT. Engine: postgres.
-- Project scoping of support configuration (D-20, PRJ-003).
--
-- Categories and inbound mailboxes belong to exactly one project (existing
-- rows join the Default project). KB categories, automation rules, macros
-- and canned responses are project-scoped or global (NULL). Custom fields
-- may be limited to projects (project_ids, NULL = all), webhooks may filter
-- on projects (project_ids, [] = all). SLA policies gain a project: rows
-- with project_id NULL are the global defaults every project falls back to.

ALTER TABLE categories ADD COLUMN project_id TEXT NOT NULL DEFAULT '00000000-0000-7000-8000-000000000001' REFERENCES projects (id);
ALTER TABLE inbound_mailboxes ADD COLUMN project_id TEXT NOT NULL DEFAULT '00000000-0000-7000-8000-000000000001' REFERENCES projects (id);
CREATE INDEX categories_project_idx ON categories (project_id, sort);
CREATE INDEX inbound_mailboxes_project_idx ON inbound_mailboxes (project_id);

ALTER TABLE kb_categories ADD COLUMN project_id TEXT NULL REFERENCES projects (id);
CREATE INDEX kb_categories_project_idx ON kb_categories (project_id);
ALTER TABLE custom_field_defs ADD COLUMN project_ids JSONB NULL;
ALTER TABLE automation_rules ADD COLUMN project_id TEXT NULL REFERENCES projects (id) ON DELETE CASCADE;
ALTER TABLE macros ADD COLUMN project_id TEXT NULL REFERENCES projects (id) ON DELETE CASCADE;
ALTER TABLE canned_responses ADD COLUMN project_id TEXT NULL REFERENCES projects (id) ON DELETE CASCADE;
CREATE INDEX canned_responses_project_idx ON canned_responses (project_id);
ALTER TABLE webhooks ADD COLUMN project_ids JSONB NOT NULL DEFAULT '[]';

-- sla_policies: (project_id NULL, priority). The primary key moves to a
-- surrogate id; two partial unique indexes keep one global row and one
-- override per project and priority. Column order is the same on both
-- engines (TestSchemaParity): the old columns, then id, then project_id.
ALTER TABLE sla_policies ADD COLUMN id TEXT NULL;
UPDATE sla_policies SET id = 'sla-global-' || priority;
ALTER TABLE sla_policies ALTER COLUMN id SET NOT NULL;
ALTER TABLE sla_policies DROP CONSTRAINT sla_policies_pkey;
ALTER TABLE sla_policies ADD PRIMARY KEY (id);
ALTER TABLE sla_policies ADD COLUMN project_id TEXT NULL REFERENCES projects (id) ON DELETE CASCADE;
CREATE UNIQUE INDEX sla_policies_global_uq ON sla_policies (priority) WHERE project_id IS NULL;
CREATE UNIQUE INDEX sla_policies_project_uq ON sla_policies (project_id, priority) WHERE project_id IS NOT NULL;
