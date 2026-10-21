// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/radioBros/SimpleTickets/internal/problems"
	"github.com/radioBros/SimpleTickets/internal/server"
	"github.com/radioBros/SimpleTickets/internal/store"
)

// coreModule serves the platform's own admin endpoints:
// GET /admin/queue (PLT-026), GET /admin/jobs + POST /admin/jobs/{id}/retry,
// GET /admin/system (version, DB, disk, WebSockets, queue; modules add
// smtp/oidc through SystemReporter), GET /admin/audit-log, GET /admin/storage,
// and the static API reference at /api/docs.
type coreModule struct {
	d *Deps
	a *App
}

func newCoreModule(d *Deps, a *App) *coreModule { return &coreModule{d: d, a: a} }

func (m *coreModule) Name() string { return "core" }

func (m *coreModule) Mount(g server.Groups) {
	g.Admin.Get("/queue", m.queueStatus)
	g.Admin.Get("/jobs", m.listJobs)
	g.Admin.Post("/jobs/{id}/retry", m.retryJob)
	g.Admin.Get("/system", m.system)
	g.Admin.Get("/audit-log", m.auditLog)
	g.Admin.Get("/storage", m.storage)
	g.Root.Get("/api/docs", serveAPIDocs)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type queueDTO struct {
	Name     string `json:"name"`
	Depth    int    `json:"depth"`
	Capacity int    `json:"capacity"`
	Workers  int    `json:"workers"`
	Durable  bool   `json:"durable"`
	Handled  int64  `json:"handled"`
	Failed   int64  `json:"failed"`
	Spilled  int64  `json:"spilled"`
	Overflow int64  `json:"overflow"`
}

func (m *coreModule) queueDoc(ctx context.Context) (map[string]any, error) {
	st, err := m.d.Queue.Status(ctx)
	if err != nil {
		return nil, err
	}
	qs := make([]queueDTO, 0, len(st.Consumers)+1)
	for _, c := range st.Consumers {
		qs = append(qs, queueDTO{Name: c.Name, Depth: c.Depth, Capacity: c.Capacity, Workers: c.Workers, Durable: c.Durable, Handled: c.Handled, Failed: c.Failed, Spilled: c.Spilled, Overflow: c.Overflow})
	}
	qs = append(qs, queueDTO{Name: "jobs", Depth: st.JobsDepth, Capacity: st.JobsCapacity, Durable: true})
	var cursor any
	if st.Cursor != "" {
		cursor = st.Cursor
	}
	return map[string]any{
		"queues":     qs,
		"deferred":   map[string]int64{"retrying": st.Deferred["retrying"], "dead": st.Deferred["dead"]},
		"dispatcher": map[string]any{"cursor": cursor, "lag_events": st.Lag, "last_round": st.LastRound, "oldest_retrying_next_run_at": st.OldestRetrying},
		"schedules":  st.Schedules,
	}, nil
}

func (m *coreModule) queueStatus(w http.ResponseWriter, r *http.Request) {
	doc, err := m.queueDoc(r.Context())
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

type jobDTO struct {
	ID        string     `json:"id"`
	Kind      string     `json:"kind"`
	Attempts  int        `json:"attempts"`
	Status    string     `json:"status"`
	NextRunAt *time.Time `json:"next_run_at"`
	LastError *string    `json:"last_error"`
	CreatedAt time.Time  `json:"created_at"`
}

func scanJob(row interface{ Scan(...any) error }) (jobDTO, error) {
	var j jobDTO
	var next time.Time
	var lastErr string
	err := row.Scan(&j.ID, &j.Kind, &j.Attempts, &j.Status, &next, &lastErr, &j.CreatedAt)
	if err != nil {
		return j, err
	}
	n := next.UTC()
	j.NextRunAt = &n
	j.CreatedAt = j.CreatedAt.UTC()
	if lastErr != "" {
		j.LastError = &lastErr
	}
	return j, nil
}

// listJobs pages deferred_jobs newest first; cursor = base64(id).
func (m *coreModule) listJobs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := 50
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			problems.Write(w, r, problems.Validation(problems.Field("limit", "range", "1..200")))
			return
		}
		limit = n
	}
	where, args := "1=1", []any{}
	if s := q.Get("status"); s != "" {
		if s != "retrying" && s != "dead" {
			problems.Write(w, r, problems.Validation(problems.Field("status", "enum", "retrying or dead")))
			return
		}
		where += " AND status = ?"
		args = append(args, s)
	}
	if c := q.Get("cursor"); c != "" {
		id, err := base64.RawURLEncoding.DecodeString(c)
		if err != nil {
			problems.Write(w, r, problems.Validation(problems.Field("cursor", "invalid", "")))
			return
		}
		where += " AND id < ?"
		args = append(args, string(id))
	}
	args = append(args, limit+1)
	rows, err := m.d.DB.Read().QueryContext(r.Context(), `SELECT id, kind, attempts, status, next_run_at, last_error, created_at FROM deferred_jobs WHERE `+where+` ORDER BY id DESC LIMIT ?`, args...)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	defer rows.Close()
	items := []jobDTO{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			problems.Write(w, r, err)
			return
		}
		items = append(items, j)
	}
	var next any
	if len(items) > limit {
		items = items[:limit]
		next = base64.RawURLEncoding.EncodeToString([]byte(items[limit-1].ID))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

func (m *coreModule) retryJob(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := m.d.Queue.Retry(r.Context(), id); err != nil {
		problems.Write(w, r, err)
		return
	}
	j, err := scanJob(m.d.DB.Read().QueryRowContext(r.Context(), `SELECT id, kind, attempts, status, next_run_at, last_error, created_at FROM deferred_jobs WHERE id = ?`, id))
	if err != nil {
		problems.Write(w, r, store.NotFound(err))
		return
	}
	writeJSON(w, http.StatusOK, j)
}

func (m *coreModule) system(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	d := m.d
	ver, _ := d.DB.SchemaVersion(ctx)
	dbDoc := map[string]any{"engine": d.DB.Dialect.String(), "schema_version": ver, "size_bytes": nil}
	// DB-017: the boot-time check (quick_check / SELECT 1) and the last backup.
	if !m.a.boot.CheckedAt.IsZero() {
		dbDoc["integrity"] = m.a.boot
	}
	var lastBackup any
	if t := store.LastBackup(m.backupDir()); !t.IsZero() {
		lastBackup = t
	}
	dbDoc["last_backup_at"] = lastBackup
	if d.DB.Dialect == store.SQLite {
		path := store.SQLiteFile(d.DB.URL)
		if st, err := os.Stat(path); err == nil {
			size := st.Size()
			if wal, err := os.Stat(path + "-wal"); err == nil {
				size += wal.Size()
			}
			dbDoc["size_bytes"] = size
		}
		if pages, wal, err := store.SQLiteStats(ctx, d.DB); err == nil {
			dbDoc["page_count"] = pages
			dbDoc["wal_bytes"] = wal
		}
	} else {
		var size int64
		if err := d.DB.Read().QueryRowContext(ctx, `SELECT pg_database_size(current_database())`).Scan(&size); err == nil {
			dbDoc["size_bytes"] = size
		}
	}
	doc := map[string]any{
		"version":        orDev(d.Version),
		"commit":         d.Commit,
		"uptime_seconds": int64(time.Since(m.a.started).Seconds()),
		"db":             dbDoc,
		"ws":             map[string]any{"connections": d.Hub.Conns()},
		"disk":           diskUsage(d.Cfg.DataDir),
	}
	if m.a.started.IsZero() {
		doc["uptime_seconds"] = 0
	}
	if qd, err := m.queueDoc(ctx); err == nil {
		doc["queue"] = qd
	}
	for _, mod := range m.a.Modules {
		if sr, ok := mod.(SystemReporter); ok {
			sr.ReportSystem(ctx, doc)
		}
	}
	writeJSON(w, http.StatusOK, doc)
}

// backupDir is where `backup` and the pre-migrate backups write by default.
func (m *coreModule) backupDir() string {
	if m.a.backups != "" {
		return m.a.backups
	}
	return filepath.Join(m.d.Cfg.DataDir, "backups")
}

func orDev(v string) string {
	if v == "" {
		return "dev"
	}
	return v
}

func diskUsage(dir string) map[string]any {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return map[string]any{"free_bytes": nil, "used_bytes": nil}
	}
	free := int64(st.Bavail) * int64(st.Bsize)
	used := (int64(st.Blocks) - int64(st.Bfree)) * int64(st.Bsize)
	return map[string]any{"free_bytes": free, "used_bytes": used}
}
