-- Knowledge base (range 0080-0089). Visibility rank: public < customers < internal;
-- an article is never more visible than its category (M-10).

CREATE TABLE kb_categories (
    id         TEXT PRIMARY KEY,
    parent_id  TEXT NULL REFERENCES kb_categories (id),
    slug       TEXT NOT NULL,
    title      {JSON} NOT NULL DEFAULT '{}',
    sort       INTEGER NOT NULL DEFAULT 0,
    visibility TEXT NOT NULL DEFAULT 'public' CHECK (visibility IN ('public', 'customers', 'internal')),
    created_at {TS} NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at {TS} NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX kb_categories_slug_uq ON kb_categories (slug);

CREATE TABLE kb_articles (
    id           TEXT PRIMARY KEY,
    category_id  TEXT NOT NULL REFERENCES kb_categories (id),
    slug         TEXT NOT NULL,
    status       TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'published', 'archived')),
    visibility   TEXT NOT NULL DEFAULT 'public' CHECK (visibility IN ('public', 'customers', 'internal')),
    author_id    TEXT NULL REFERENCES users (id),
    published_at {TS} NULL,
    views        BIGINT NOT NULL DEFAULT 0,
    helpful_yes  BIGINT NOT NULL DEFAULT 0,
    helpful_no   BIGINT NOT NULL DEFAULT 0,
    created_at   {TS} NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at   {TS} NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX kb_articles_slug_uq ON kb_articles (slug);
CREATE INDEX kb_articles_category_idx ON kb_articles (category_id, status);

CREATE TABLE kb_article_translations (
    article_id TEXT NOT NULL REFERENCES kb_articles (id) ON DELETE CASCADE,
    locale     TEXT NOT NULL CHECK (locale IN ('en', 'it', 'de', 'fr', 'es')),
    title      TEXT NOT NULL,
    body_md    TEXT NOT NULL DEFAULT '',
    updated_by TEXT NULL,
    updated_at {TS} NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (article_id, locale)
);

CREATE TABLE kb_article_feedback (
    id         TEXT PRIMARY KEY,
    article_id TEXT NOT NULL REFERENCES kb_articles (id) ON DELETE CASCADE,
    helpful    BOOLEAN NOT NULL,
    created_at {TS} NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX kb_article_feedback_article_idx ON kb_article_feedback (article_id);

-- Old slugs keep redirecting after a rename.
CREATE TABLE kb_slug_history (
    old_slug   TEXT PRIMARY KEY,
    article_id TEXT NOT NULL REFERENCES kb_articles (id) ON DELETE CASCADE,
    created_at {TS} NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE kb_assets (
    id           TEXT PRIMARY KEY,
    article_id   TEXT NULL REFERENCES kb_articles (id) ON DELETE SET NULL,
    uploader_id  TEXT NULL REFERENCES users (id),
    filename     TEXT NOT NULL,
    content_type TEXT NOT NULL,
    size_bytes   BIGINT NOT NULL,
    sha256       TEXT NOT NULL,
    blob_key     TEXT NOT NULL,
    created_at   {TS} NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX kb_assets_article_idx ON kb_assets (article_id);

@@sqlite
CREATE VIRTUAL TABLE kb_fts USING fts5(
    article_id UNINDEXED,
    locale UNINDEXED,
    title,
    body,
    tokenize = 'unicode61 remove_diacritics 2'
);

-- M-10: an article is never more visible than its category.
-- +StatementBegin
CREATE TRIGGER kb_articles_visibility_ins BEFORE INSERT ON kb_articles
WHEN (CASE NEW.visibility WHEN 'public' THEN 0 WHEN 'customers' THEN 1 ELSE 2 END)
   < (SELECT CASE visibility WHEN 'public' THEN 0 WHEN 'customers' THEN 1 ELSE 2 END FROM kb_categories WHERE id = NEW.category_id)
BEGIN
    SELECT RAISE(ABORT, 'kb article visibility is wider than its category');
END;
-- +StatementEnd
-- +StatementBegin
CREATE TRIGGER kb_articles_visibility_upd BEFORE UPDATE OF visibility, category_id ON kb_articles
WHEN (CASE NEW.visibility WHEN 'public' THEN 0 WHEN 'customers' THEN 1 ELSE 2 END)
   < (SELECT CASE visibility WHEN 'public' THEN 0 WHEN 'customers' THEN 1 ELSE 2 END FROM kb_categories WHERE id = NEW.category_id)
BEGIN
    SELECT RAISE(ABORT, 'kb article visibility is wider than its category');
END;
-- +StatementEnd
-- +StatementBegin
CREATE TRIGGER kb_categories_visibility_upd BEFORE UPDATE OF visibility ON kb_categories
WHEN EXISTS (
    SELECT 1 FROM kb_articles a
    WHERE a.category_id = NEW.id
      AND (CASE a.visibility WHEN 'public' THEN 0 WHEN 'customers' THEN 1 ELSE 2 END)
        < (CASE NEW.visibility WHEN 'public' THEN 0 WHEN 'customers' THEN 1 ELSE 2 END)
)
BEGIN
    SELECT RAISE(ABORT, 'kb category visibility would be narrower than one of its articles');
END;
-- +StatementEnd
@@end
@@postgres
CREATE TABLE kb_search (
    article_id TEXT NOT NULL REFERENCES kb_articles (id) ON DELETE CASCADE,
    locale     TEXT NOT NULL,
    title      TEXT NOT NULL DEFAULT '',
    tsv        TSVECTOR NOT NULL,
    PRIMARY KEY (article_id, locale)
);
CREATE INDEX kb_search_tsv_gin ON kb_search USING GIN (tsv);

-- +StatementBegin
CREATE FUNCTION fd_kb_rank(v TEXT) RETURNS INTEGER LANGUAGE sql IMMUTABLE AS $$
    SELECT CASE v WHEN 'public' THEN 0 WHEN 'customers' THEN 1 ELSE 2 END
$$;
-- +StatementEnd

-- M-10: an article is never more visible than its category.
-- +StatementBegin
CREATE FUNCTION fd_kb_article_visibility() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF fd_kb_rank(NEW.visibility) < (SELECT fd_kb_rank(visibility) FROM kb_categories WHERE id = NEW.category_id) THEN
        RAISE EXCEPTION 'kb article visibility is wider than its category';
    END IF;
    RETURN NEW;
END
$$;
-- +StatementEnd
CREATE TRIGGER kb_articles_visibility BEFORE INSERT OR UPDATE OF visibility, category_id ON kb_articles FOR EACH ROW EXECUTE FUNCTION fd_kb_article_visibility();

-- +StatementBegin
CREATE FUNCTION fd_kb_category_visibility() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF EXISTS (SELECT 1 FROM kb_articles a WHERE a.category_id = NEW.id AND fd_kb_rank(a.visibility) < fd_kb_rank(NEW.visibility)) THEN
        RAISE EXCEPTION 'kb category visibility would be narrower than one of its articles';
    END IF;
    RETURN NEW;
END
$$;
-- +StatementEnd
CREATE TRIGGER kb_categories_visibility BEFORE UPDATE OF visibility ON kb_categories FOR EACH ROW EXECUTE FUNCTION fd_kb_category_visibility();
@@end
