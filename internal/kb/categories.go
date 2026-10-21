// SPDX-License-Identifier: AGPL-3.0-only

package kb

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"sort"
	"time"
	"unicode/utf8"

	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/problems"
	"github.com/radioBros/SimpleTickets/internal/server"
	"github.com/radioBros/SimpleTickets/internal/store"
)

// slugRe is the api pattern (^[a-z0-9][a-z0-9-]{0,119}$) for articles and
// categories; category slugs are additionally capped at 80 (KB-002).
var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,119}$`)

const maxCategorySlug = 80

type category struct {
	ID, ParentID, Slug, Visibility string
	Title                          map[string]string
	Sort                           int
	UpdatedAt                      time.Time
}

func loadCategories(ctx context.Context, q store.Querier) (map[string]*category, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, COALESCE(parent_id, ''), slug, title, sort, visibility, updated_at FROM kb_categories`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]*category{}
	for rows.Next() {
		var c category
		var title store.JSON[map[string]string]
		if err := rows.Scan(&c.ID, &c.ParentID, &c.Slug, &title, &c.Sort, &c.Visibility, &c.UpdatedAt); err != nil {
			return nil, err
		}
		c.Title = title.V
		if c.Title == nil {
			c.Title = map[string]string{}
		}
		out[c.ID] = &c
	}
	return out, rows.Err()
}

// depth of a category: roots are 1.
func depth(all map[string]*category, id string) int {
	d := 0
	for seen := 0; id != "" && seen <= len(all); seen++ {
		c, ok := all[id]
		if !ok {
			break
		}
		d++
		id = c.ParentID
	}
	return d
}

// height of the subtree rooted at id: a leaf is 1.
func height(all map[string]*category, id string) int {
	h := 0
	for _, c := range all {
		if c.ParentID == id {
			h = max(h, height(all, c.ID))
		}
	}
	return h + 1
}

func isDescendant(all map[string]*category, id, ancestor string) bool {
	for seen := 0; id != "" && seen <= len(all); seen++ {
		if id == ancestor {
			return true
		}
		c, ok := all[id]
		if !ok {
			return false
		}
		id = c.ParentID
	}
	return false
}

// categoryInput is KbCategoryCreate. ParentID distinguishes absent from null.
type categoryInput struct {
	ParentID   json.RawMessage   `json:"parent_id"`
	Slug       *string           `json:"slug"`
	Title      map[string]string `json:"title"`
	Sort       *int              `json:"sort"`
	Visibility *string           `json:"visibility"`
}

func (in categoryInput) parent() (id string, set bool, err error) {
	if len(in.ParentID) == 0 {
		return "", false, nil
	}
	if string(in.ParentID) == "null" {
		return "", true, nil
	}
	if err := json.Unmarshal(in.ParentID, &id); err != nil || !validID(id) {
		return "", true, fieldErr("parent_id", "uuid", "parent_id must be a category id")
	}
	return id, true, nil
}

func validateCategory(c *category) error {
	var fields []problems.FieldError
	if !slugRe.MatchString(c.Slug) || len(c.Slug) > maxCategorySlug {
		fields = append(fields, problems.Field("slug", "slug", "lowercase letters, digits and dashes, at most 80"))
	}
	if visRank(c.Visibility) < 0 {
		fields = append(fields, problems.Field("visibility", "enum", "public, customers or internal"))
	}
	if len(c.Title) == 0 {
		fields = append(fields, problems.Field("title", "required", "at least one translated title"))
	}
	for l, t := range c.Title {
		if !isLocale(l) {
			fields = append(fields, problems.Field("title."+l, "locale", "unknown locale"))
		} else if n := utf8.RuneCountInString(t); n == 0 || n > maxTitle {
			fields = append(fields, problems.Field("title."+l, "max_length", "1 to 200 characters"))
		}
	}
	if len(fields) > 0 {
		return problems.Validation(fields...)
	}
	return nil
}

// checkTree enforces depth ≤ 3, no cycles and "never wider than the parent"
// for c placed under c.ParentID (all is the tree before the change).
func checkTree(all map[string]*category, c *category) error {
	if c.ParentID == "" {
		return checkChildren(all, c)
	}
	parent, ok := all[c.ParentID]
	if !ok {
		return fieldErr("parent_id", "not_found", "parent category does not exist")
	}
	if c.ID != "" && isDescendant(all, c.ParentID, c.ID) {
		return problems.Conflict("kb-category-cycle: a category cannot move under itself or its descendants")
	}
	h := 1
	if c.ID != "" {
		h = height(all, c.ID)
	}
	if depth(all, c.ParentID)+h > maxCategoryDepth {
		return fieldErr("parent_id", "max_depth", "categories nest at most 3 levels deep")
	}
	if visRank(c.Visibility) < visRank(parent.Visibility) {
		return fieldErr("visibility", "kb_visibility_wider", "a category cannot be more visible than its parent")
	}
	return checkChildren(all, c)
}

func checkChildren(all map[string]*category, c *category) error {
	if c.ID == "" {
		return nil
	}
	for _, ch := range all {
		if ch.ParentID == c.ID && visRank(ch.Visibility) < visRank(c.Visibility) {
			return problems.Conflict("kb-category-narrowing: a sub-category is more visible than the new visibility")
		}
	}
	return nil
}

func toCategoryDTO(c *category, count int, locale, def string) categoryDTO {
	title := make(map[string]string, len(c.Title)+1)
	for k, v := range c.Title {
		title[k] = v
	}
	if locale != "" && title[locale] == "" {
		if l, _ := pickLocale(func(l string) bool { return c.Title[l] != "" }, locale, def); l != "" {
			title[locale] = c.Title[l]
		}
	}
	var parent *string
	if c.ParentID != "" {
		p := c.ParentID
		parent = &p
	}
	return categoryDTO{ID: c.ID, ParentID: parent, Slug: c.Slug, Title: title, Sort: c.Sort, Visibility: c.Visibility, ArticleCount: count}
}

func sortCategories(out []categoryDTO) {
	sort.Slice(out, func(i, j int) bool {
		if out[i].Sort != out[j].Sort {
			return out[i].Sort < out[j].Sort
		}
		return out[i].Slug < out[j].Slug
	})
}

// listCategories returns the categories the audience can see, with the count
// of articles it can see in each (published only unless staffAll).
func (m *Module) listCategories(ctx context.Context, aud audience, locale string, staffAll bool) ([]categoryDTO, error) {
	q := m.d.DB.Read()
	all, err := loadCategories(ctx, q)
	if err != nil {
		return nil, err
	}
	statuses := []string{"published"}
	if staffAll {
		statuses = []string{"draft", "published", "archived"}
	}
	counts := map[string]int{}
	rows, err := q.QueryContext(ctx, `SELECT a.category_id, COUNT(*) FROM kb_articles a JOIN kb_categories c ON c.id = a.category_id WHERE `+
		visPredicate(aud, statuses)+` GROUP BY a.category_id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			rows.Close()
			return nil, err
		}
		counts[id] = n
	}
	rows.Close()
	def := m.defaultLocale(ctx)
	out := []categoryDTO{}
	maxRank := aud.maxRank()
	for _, c := range all {
		if visRank(c.Visibility) > maxRank || !ancestorsVisible(all, c, maxRank) {
			continue
		}
		out = append(out, toCategoryDTO(c, counts[c.ID], locale, def))
	}
	sortCategories(out)
	return out, nil
}

func ancestorsVisible(all map[string]*category, c *category, maxRank int) bool {
	for seen := 0; c.ParentID != "" && seen <= len(all); seen++ {
		p, ok := all[c.ParentID]
		if !ok {
			return true
		}
		if visRank(p.Visibility) > maxRank {
			return false
		}
		c = p
	}
	return true
}

// CreateCategory adds a category (supervisor+).
func (m *Module) CreateCategory(ctx context.Context, p *domain.Principal, in categoryInput) (categoryDTO, error) {
	if err := authorize(p, ActionCategoryManage, ""); err != nil {
		return categoryDTO{}, err
	}
	if in.Slug == nil || in.Visibility == nil || in.Title == nil {
		return categoryDTO{}, problems.Validation(problems.Field("slug", "required", ""), problems.Field("title", "required", ""), problems.Field("visibility", "required", ""))
	}
	parent, _, err := in.parent()
	if err != nil {
		return categoryDTO{}, err
	}
	c := &category{ID: store.NewID(), ParentID: parent, Slug: *in.Slug, Title: in.Title, Visibility: *in.Visibility}
	if in.Sort != nil {
		c.Sort = *in.Sort
	}
	if err := validateCategory(c); err != nil {
		return categoryDTO{}, err
	}
	err = m.d.DB.Tx(ctx, func(tx *store.Tx) error {
		all, err := loadCategories(ctx, tx)
		if err != nil {
			return err
		}
		probe := *c
		probe.ID = ""
		if err := checkTree(all, &probe); err != nil {
			return err
		}
		now := store.Now()
		c.UpdatedAt = now
		if _, err := tx.ExecContext(ctx, `INSERT INTO kb_categories (id, parent_id, slug, title, sort, visibility, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			c.ID, store.Nullable(c.ParentID), c.Slug, store.NewJSON(c.Title), c.Sort, c.Visibility, now, now); err != nil {
			if store.IsUniqueViolation(err) {
				return problems.Conflict("kb-slug-taken: the category slug is already used")
			}
			return err
		}
		tx.AfterCommit(func() { m.committed(updateEvent{Action: "category.created", CategoryID: c.ID, ActorID: p.UserID}) })
		return audit(ctx, tx, p, "kb.category.create", "kb_category", c.ID, map[string]any{"slug": c.Slug, "visibility": c.Visibility})
	})
	if err != nil {
		return categoryDTO{}, err
	}
	return toCategoryDTO(c, 0, "", ""), nil
}

// UpdateCategory replaces the editable fields of a category (supervisor+).
func (m *Module) UpdateCategory(ctx context.Context, p *domain.Principal, id string, in categoryInput) (categoryDTO, error) {
	if err := authorize(p, ActionCategoryManage, ""); err != nil {
		return categoryDTO{}, err
	}
	if !validID(id) {
		return categoryDTO{}, problems.NotFound()
	}
	parent, parentSet, err := in.parent()
	if err != nil {
		return categoryDTO{}, err
	}
	var out categoryDTO
	err = m.d.DB.Tx(ctx, func(tx *store.Tx) error {
		all, err := loadCategories(ctx, tx)
		if err != nil {
			return err
		}
		cur, ok := all[id]
		if !ok {
			return problems.NotFound()
		}
		c := *cur
		if parentSet {
			c.ParentID = parent
		}
		if in.Slug != nil {
			c.Slug = *in.Slug
		}
		if in.Title != nil {
			c.Title = in.Title
		}
		if in.Sort != nil {
			c.Sort = *in.Sort
		}
		if in.Visibility != nil {
			c.Visibility = *in.Visibility
		}
		if err := validateCategory(&c); err != nil {
			return err
		}
		if err := checkTree(all, &c); err != nil {
			return err
		}
		// M-10 pre-check: narrowing below a contained article is refused.
		if visRank(c.Visibility) > visRank(cur.Visibility) {
			var n int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM kb_articles WHERE category_id = ? AND visibility IN (`+inList(visBelow(c.Visibility))+`)`, id).Scan(&n); err != nil {
				return err
			}
			if n > 0 {
				return problems.Conflict("kb-category-narrowing: an article in this category is more visible than the new visibility (M-10)")
			}
		}
		c.UpdatedAt = store.Now()
		_, err = tx.ExecContext(ctx, `UPDATE kb_categories SET parent_id = ?, slug = ?, title = ?, sort = ?, visibility = ?, updated_at = ? WHERE id = ?`,
			store.Nullable(c.ParentID), c.Slug, store.NewJSON(c.Title), c.Sort, c.Visibility, c.UpdatedAt, id)
		switch {
		case store.IsUniqueViolation(err):
			return problems.Conflict("kb-slug-taken: the category slug is already used")
		case isM10(err):
			return problems.Conflict("kb-category-narrowing: " + err.Error())
		case err != nil:
			return err
		}
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM kb_articles WHERE category_id = ?`, id).Scan(&count); err != nil {
			return err
		}
		out = toCategoryDTO(&c, count, "", "")
		tx.AfterCommit(func() { m.committed(updateEvent{Action: "category.updated", CategoryID: id, ActorID: p.UserID}) })
		return audit(ctx, tx, p, "kb.category.update", "kb_category", id, map[string]any{"slug": c.Slug, "visibility": c.Visibility, "parent_id": c.ParentID})
	})
	return out, err
}

// DeleteCategory removes an empty category (no articles, no children).
func (m *Module) DeleteCategory(ctx context.Context, p *domain.Principal, id string) error {
	if err := authorize(p, ActionCategoryManage, ""); err != nil {
		return err
	}
	if !validID(id) {
		return problems.NotFound()
	}
	return m.d.DB.Tx(ctx, func(tx *store.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM kb_categories WHERE id = ?`, id).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return problems.NotFound()
		}
		if err := tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM kb_articles WHERE category_id = ?) + (SELECT COUNT(*) FROM kb_categories WHERE parent_id = ?)`, id, id).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			e := problems.Conflict("kb-category-not-empty: the category still contains articles or sub-categories")
			e.Extra = map[string]any{"code": "kb-category-not-empty"}
			return e
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM kb_categories WHERE id = ?`, id); err != nil {
			return err
		}
		tx.AfterCommit(func() { m.committed(updateEvent{Action: "category.deleted", CategoryID: id, ActorID: p.UserID}) })
		return audit(ctx, tx, p, "kb.category.delete", "kb_category", id, nil)
	})
}

// visBelow lists the visibilities strictly wider (lower rank) than v.
func visBelow(v string) []string {
	var out []string
	for _, x := range []string{VisPublic, VisCustomers, VisInternal} {
		if visRank(x) < visRank(v) {
			out = append(out, x)
		}
	}
	if len(out) == 0 {
		return []string{"-"}
	}
	return out
}

// ---- handlers ----

func (m *Module) handleAdminCategories(w http.ResponseWriter, r *http.Request) {
	out, err := m.listCategories(r.Context(), audStaff, "", true)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (m *Module) handleCreateCategory(w http.ResponseWriter, r *http.Request) {
	var in categoryInput
	if err := decodeJSON(r, &in); err != nil {
		problems.Write(w, r, err)
		return
	}
	out, err := m.CreateCategory(withIP(r.Context(), server.ClientIP(r)), server.PrincipalFrom(r.Context()), in)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (m *Module) handleUpdateCategory(w http.ResponseWriter, r *http.Request) {
	var in categoryInput
	if err := decodeJSON(r, &in); err != nil {
		problems.Write(w, r, err)
		return
	}
	out, err := m.UpdateCategory(withIP(r.Context(), server.ClientIP(r)), server.PrincipalFrom(r.Context()), param(r, "id"), in)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (m *Module) handleDeleteCategory(w http.ResponseWriter, r *http.Request) {
	if err := m.DeleteCategory(withIP(r.Context(), server.ClientIP(r)), server.PrincipalFrom(r.Context()), param(r, "id")); err != nil {
		problems.Write(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
