-- Code generated from migrations/_templates for both engines (D-18). DO NOT EDIT. Engine: postgres.
-- Identity and auth (range 0001-0019). See development/contracts/domain_model_spec.md.

-- Rows listed here may be deleted from append-only tables (ticket hard delete, retention prune).
CREATE TABLE purge_allow (
    tbl TEXT NOT NULL,
    key TEXT NOT NULL,
    PRIMARY KEY (tbl, key)
);

CREATE TABLE organizations (
    id            TEXT PRIMARY KEY,
    name          TEXT NOT NULL,
    email_domains JSONB NOT NULL DEFAULT '[]',
    status        TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'archived')),
    notes         TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX organizations_name_uq ON organizations (lower(name));

CREATE TABLE users (
    id              TEXT PRIMARY KEY,
    kind            TEXT NOT NULL CHECK (kind IN ('staff', 'customer')),
    email           TEXT NOT NULL,
    name            TEXT NOT NULL DEFAULT '',
    role            TEXT NOT NULL,
    tier            INTEGER NULL,
    organization_id TEXT NULL REFERENCES organizations (id),
    status          TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'pending_verification', 'disabled')),
    password_hash   TEXT NULL,
    break_glass     BOOLEAN NOT NULL DEFAULT FALSE,
    idp_managed     BOOLEAN NOT NULL DEFAULT FALSE,
    locale          TEXT NOT NULL DEFAULT 'en' CHECK (locale IN ('en', 'it', 'de', 'fr', 'es')),
    signature_md    TEXT NOT NULL DEFAULT '',
    notify_prefs    JSONB NOT NULL DEFAULT '{}',
    last_login_at   TIMESTAMPTZ NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    -- M-2: staff-only and customer-only columns never mix.
    CONSTRAINT users_kind_role_ck CHECK (
        (kind = 'staff' AND role IN ('admin', 'supervisor', 'agent') AND tier IS NOT NULL AND tier BETWEEN 1 AND 3 AND organization_id IS NULL)
        OR (kind = 'customer' AND role IN ('requester', 'org_manager') AND tier IS NULL)
    ),
    CONSTRAINT users_break_glass_ck CHECK (break_glass = FALSE OR (kind = 'staff' AND role = 'admin'))
);
CREATE UNIQUE INDEX users_email_uq ON users (email);
CREATE INDEX users_org_idx ON users (organization_id);
CREATE INDEX users_kind_status_idx ON users (kind, status);

CREATE TABLE user_identities (
    id            TEXT PRIMARY KEY,
    user_id       TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    audience      TEXT NOT NULL CHECK (audience IN ('staff', 'customer')),
    issuer        TEXT NOT NULL,
    subject       TEXT NOT NULL,
    email_at_link TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX user_identities_iss_sub_uq ON user_identities (issuer, subject);
CREATE INDEX user_identities_user_idx ON user_identities (user_id);

CREATE TABLE sessions (
    id_hash       TEXT PRIMARY KEY,
    user_id       TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    audience      TEXT NOT NULL CHECK (audience IN ('staff', 'customer')),
    csrf_token    TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_seen_at  TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at    TIMESTAMPTZ NOT NULL,
    ip            TEXT NOT NULL DEFAULT '',
    user_agent    TEXT NOT NULL DEFAULT '',
    oidc_id_token TEXT NULL,
    oidc_sid      TEXT NULL
);
CREATE INDEX sessions_user_idx ON sessions (user_id);
CREATE INDEX sessions_expires_idx ON sessions (expires_at);
CREATE INDEX sessions_oidc_sid_idx ON sessions (oidc_sid);

CREATE TABLE api_tokens (
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    prefix       TEXT NOT NULL,
    token_hash   TEXT NOT NULL,
    scopes       JSONB NOT NULL DEFAULT '[]',
    last_used_at TIMESTAMPTZ NULL,
    expires_at   TIMESTAMPTZ NULL,
    revoked_at   TIMESTAMPTZ NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX api_tokens_hash_uq ON api_tokens (token_hash);
CREATE INDEX api_tokens_user_idx ON api_tokens (user_id);

CREATE TABLE auth_tokens (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    purpose    TEXT NOT NULL CHECK (purpose IN ('verify_email', 'reset_password', 'magic_link', 'invite')),
    token_hash TEXT NOT NULL,
    data       JSONB NOT NULL DEFAULT '{}',
    expires_at TIMESTAMPTZ NOT NULL,
    used_at    TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX auth_tokens_hash_uq ON auth_tokens (token_hash);
CREATE INDEX auth_tokens_user_idx ON auth_tokens (user_id, purpose);

CREATE TABLE audit_log (
    id          TEXT PRIMARY KEY,
    actor_id    TEXT NULL,
    action      TEXT NOT NULL,
    target_type TEXT NOT NULL DEFAULT '',
    target_id   TEXT NOT NULL DEFAULT '',
    data        JSONB NOT NULL DEFAULT '{}',
    ip          TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX audit_log_created_idx ON audit_log (created_at);
CREATE INDEX audit_log_target_idx ON audit_log (target_type, target_id);

-- M-1: users.kind is immutable.
-- +StatementBegin
CREATE FUNCTION fd_users_kind_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.kind <> OLD.kind THEN
        RAISE EXCEPTION 'users.kind is immutable';
    END IF;
    RETURN NEW;
END
$$;
-- +StatementEnd
CREATE TRIGGER users_kind_immutable BEFORE UPDATE OF kind ON users FOR EACH ROW EXECUTE FUNCTION fd_users_kind_immutable();

-- audit_log is append-only (deletes only through purge_allow for retention).
-- +StatementBegin
CREATE FUNCTION fd_audit_log_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' AND EXISTS (SELECT 1 FROM purge_allow WHERE tbl = 'audit_log' AND key IN ('*', OLD.id)) THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'audit_log is append-only';
END
$$;
-- +StatementEnd
CREATE TRIGGER audit_log_append_only BEFORE UPDATE OR DELETE ON audit_log FOR EACH ROW EXECUTE FUNCTION fd_audit_log_append_only();
