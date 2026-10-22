// SPDX-License-Identifier: AGPL-3.0-only

package kb

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/problems"
	"github.com/radioBros/FeatherDesk/internal/store"
)

// Locales are the article languages (schema CHECK and api Locale enum).
var Locales = []string{"en", "it", "de", "fr", "es"}

func isLocale(l string) bool { return slices.Contains(Locales, l) }

// enabledLocale: one of the five and listed in settings.support.locales.
func (m *Module) enabledLocale(ctx context.Context, l string) bool {
	if !isLocale(l) {
		return false
	}
	ls := m.support(ctx).Locales
	return len(ls) == 0 || slices.Contains(ls, l)
}

// ---- wire DTOs (api/openapi.yaml components) ----

type userRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

type translationDTO struct {
	ArticleID string     `json:"article_id"`
	Locale    string     `json:"locale"`
	Title     string     `json:"title"`
	BodyMD    string     `json:"body_md"`
	UpdatedBy *userRef   `json:"updated_by"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}

type articleDTO struct {
	ID               string           `json:"id"`
	CategoryID       string           `json:"category_id"`
	Slug             string           `json:"slug"`
	Status           string           `json:"status"`
	Visibility       string           `json:"visibility"`
	Locale           string           `json:"locale"`
	Title            string           `json:"title"`
	BodyMD           string           `json:"body_md"`
	BodyHTML         string           `json:"body_html"`
	AvailableLocales []string         `json:"available_locales"`
	Author           *userRef         `json:"author"`
	PublishedAt      *time.Time       `json:"published_at"`
	UpdatedAt        time.Time        `json:"updated_at"`
	Views            *int64           `json:"views,omitempty"`
	HelpfulYes       *int64           `json:"helpful_yes,omitempty"`
	HelpfulNo        *int64           `json:"helpful_no,omitempty"`
	Translations     []translationDTO `json:"translations,omitempty"`
	// AllowedActions are the editor actions the reader may take (KB-013);
	// empty for customers and anonymous readers.
	AllowedActions []string `json:"allowed_actions"`
	// Fallback is true when the requested locale was missing (KB-004).
	Fallback bool `json:"fallback"`
	// Robots is "noindex" for articles that are not public (KB-009).
	Robots string `json:"robots"`
}

type summaryDTO struct {
	ID         string    `json:"id"`
	CategoryID string    `json:"category_id"`
	Slug       string    `json:"slug"`
	Status     string    `json:"status"`
	Visibility string    `json:"visibility"`
	Locale     string    `json:"locale"`
	Title      string    `json:"title"`
	Excerpt    string    `json:"excerpt"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func (s summaryDTO) size() int {
	return len(s.ID)*2 + len(s.Slug) + len(s.Title) + len(s.Excerpt) + 96
}

type pageDTO struct {
	Items      []summaryDTO `json:"items"`
	NextCursor *string      `json:"next_cursor"`
}

type categoryDTO struct {
	ID           string            `json:"id"`
	ParentID     *string           `json:"parent_id"`
	Slug         string            `json:"slug"`
	Title        map[string]string `json:"title"`
	Sort         int               `json:"sort"`
	Visibility   string            `json:"visibility"`
	ArticleCount int               `json:"article_count"`
	ProjectID    *string           `json:"project_id"` // D-20: nil = global
}

// ---- rows ----

type article struct {
	ID, CategoryID, Slug, Status, Visibility string
	CatVisibility                            string
	AuthorID, AuthorName                     string
	PublishedAt                              *time.Time
	Views, Yes, No                           int64
	UpdatedAt                                time.Time
}

type translation struct {
	Locale, Title, BodyMD string
	UpdatedBy, UpdaterNm  string
	UpdatedAt             time.Time
}

const articleCols = `a.id, a.category_id, a.slug, a.status, a.visibility, c.visibility, COALESCE(a.author_id, ''), COALESCE(u.name, ''),
	a.published_at, a.views, a.helpful_yes, a.helpful_no, a.updated_at`

const articleFrom = ` FROM kb_articles a JOIN kb_categories c ON c.id = a.category_id LEFT JOIN users u ON u.id = a.author_id `

func scanArticle(row interface{ Scan(...any) error }) (*article, error) {
	var a article
	var pub store.NullTime
	if err := row.Scan(&a.ID, &a.CategoryID, &a.Slug, &a.Status, &a.Visibility, &a.CatVisibility, &a.AuthorID, &a.AuthorName,
		&pub, &a.Views, &a.Yes, &a.No, &a.UpdatedAt); err != nil {
		return nil, store.NotFound(err)
	}
	if pub.Valid {
		t := pub.Time.UTC()
		a.PublishedAt = &t
	}
	a.UpdatedAt = a.UpdatedAt.UTC()
	return &a, nil
}

// loadArticle reads one article by id with no visibility filter (editor/service).
func loadArticle(ctx context.Context, q store.Querier, id string) (*article, error) {
	if !validID(id) {
		return nil, store.ErrNotFound
	}
	return scanArticle(q.QueryRowContext(ctx, `SELECT `+articleCols+articleFrom+`WHERE a.id = ?`, id))
}

func loadTranslations(ctx context.Context, q store.Querier, articleID string) (map[string]translation, error) {
	rows, err := q.QueryContext(ctx, `SELECT t.locale, t.title, t.body_md, COALESCE(t.updated_by, ''), COALESCE(u.name, ''), t.updated_at
		FROM kb_article_translations t LEFT JOIN users u ON u.id = t.updated_by WHERE t.article_id = ?`, articleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]translation{}
	for rows.Next() {
		var t translation
		if err := rows.Scan(&t.Locale, &t.Title, &t.BodyMD, &t.UpdatedBy, &t.UpdaterNm, &t.UpdatedAt); err != nil {
			return nil, err
		}
		t.UpdatedAt = t.UpdatedAt.UTC()
		out[t.Locale] = t
	}
	return out, rows.Err()
}

// pickLocale implements the read fallback (KB-004): the requested locale, then
// the instance default, then any existing translation in Locales order.
func pickLocale(have func(string) bool, requested, def string) (string, bool) {
	if requested != "" && have(requested) {
		return requested, false
	}
	if have(def) {
		return def, requested != "" && requested != def
	}
	for _, l := range Locales {
		if have(l) {
			return l, true
		}
	}
	return "", false
}

func availableLocales(trs map[string]translation) []string {
	out := []string{}
	for _, l := range Locales {
		if _, ok := trs[l]; ok {
			out = append(out, l)
		}
	}
	return out
}

func ref(id, name string) *userRef {
	if id == "" {
		return nil
	}
	return &userRef{ID: id, Name: name, Kind: string(domain.KindStaff)}
}

// ---- misc ----

func validID(id string) bool {
	_, err := uuid.Parse(id)
	return err == nil && len(id) == 36
}

type ipKey struct{}

func withIP(ctx context.Context, ip string) context.Context {
	return context.WithValue(ctx, ipKey{}, ip)
}

// audit writes one audit_log row inside the caller's transaction.
func audit(ctx context.Context, tx *store.Tx, p *domain.Principal, action, targetType, targetID string, data map[string]any) error {
	ip, _ := ctx.Value(ipKey{}).(string)
	var actor any
	if p != nil && p.UserID != "" {
		actor = p.UserID
	}
	if data == nil {
		data = map[string]any{}
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO audit_log (id, actor_id, action, target_type, target_id, data, ip, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		store.NewID(), actor, action, targetType, targetID, store.MustJSON(data), ip, store.Now())
	return err
}

// isM10 recognises the M-10 trigger error on either engine.
func isM10(err error) bool {
	return err != nil && strings.Contains(err.Error(), "kb ") && strings.Contains(err.Error(), "visibility")
}

func notFoundIs(err error) bool {
	return errors.Is(err, store.ErrNotFound) || errors.Is(err, sql.ErrNoRows)
}

func fieldErr(field, code, msg string) error {
	return problems.Validation(problems.Field(field, code, msg))
}
