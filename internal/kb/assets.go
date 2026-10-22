// SPDX-License-Identifier: AGPL-3.0-only

package kb

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/radioBros/FeatherDesk/internal/blob"
	"github.com/radioBros/FeatherDesk/internal/problems"
	"github.com/radioBros/FeatherDesk/internal/server"
	"github.com/radioBros/FeatherDesk/internal/store"
)

// Article images (KB-006): PNG, JPEG, WebP or GIF, sniffed from the bytes
// (never trusted from the client), no SVG, at most FD_MAX_UPLOAD_MB, stored
// under the blob prefix `kb/` and served at /api/v1/kb/assets/{id}.
var imageTypes = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/webp": ".webp",
	"image/gif":  ".gif",
}

// linkAssets records the first article that references each asset.
func linkAssets(ctx context.Context, tx *store.Tx, articleID, body string) error {
	for _, id := range referencedAssets(body) {
		if _, err := tx.ExecContext(ctx, `UPDATE kb_assets SET article_id = ? WHERE id = ? AND article_id IS NULL`, articleID, id); err != nil {
			return err
		}
	}
	return nil
}

func (m *Module) maxUpload() int64 {
	if m.d.Cfg != nil && m.d.Cfg.MaxUploadMB > 0 {
		return int64(m.d.Cfg.MaxUploadMB) << 20
	}
	return 25 << 20
}

func (m *Module) handleUploadAsset(w http.ResponseWriter, r *http.Request) {
	ctx, p := reqCtx(r)
	if err := authorize(p, ActionDraft, ""); err != nil {
		problems.Write(w, r, err)
		return
	}
	if m.d.Blob == nil {
		problems.Write(w, r, problems.New(problems.SlugUnavailable, "blob storage is not configured"))
		return
	}
	mr, err := r.MultipartReader()
	if err != nil {
		problems.Write(w, r, fieldErr("file", "required", "multipart/form-data with a file part is required"))
		return
	}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			problems.Write(w, r, fieldErr("file", "required", "the file part is missing"))
			return
		}
		if err != nil {
			problems.Write(w, r, problems.From(err))
			return
		}
		if part.FormName() != "file" {
			_ = part.Close()
			continue
		}
		out, err := m.storeAsset(ctx, p.UserID, part.FileName(), part)
		_ = part.Close()
		if err != nil {
			problems.Write(w, r, err)
			return
		}
		writeJSON(w, http.StatusCreated, out)
		return
	}
}

type assetCreated struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

// storeAsset sniffs, size-checks and stores one image.
func (m *Module) storeAsset(ctx context.Context, uploader, filename string, r io.Reader) (assetCreated, error) {
	head := make([]byte, 512)
	n, err := io.ReadFull(r, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return assetCreated{}, problems.From(err)
	}
	head = head[:n]
	if n == 0 {
		return assetCreated{}, fieldErr("file", "required", "the file is empty")
	}
	ct := http.DetectContentType(head)
	ext, ok := imageTypes[ct]
	if !ok || strings.EqualFold(path.Ext(filename), ".svg") || bytes.Contains(bytes.ToLower(head), []byte("<svg")) {
		return assetCreated{}, fieldErr("file", "file_type", "only PNG, JPEG, WebP or GIF images (no SVG)")
	}
	limit := m.maxUpload()
	counted := &countingReader{r: io.LimitReader(io.MultiReader(bytes.NewReader(head), r), limit+1)}
	key := blob.NewKey("kb", time.Now())
	sha, err := m.d.Blob.Put(ctx, key, counted, -1, ct)
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return assetCreated{}, problems.PayloadTooLarge()
		}
		return assetCreated{}, err
	}
	if counted.n > limit {
		_ = m.d.Blob.Delete(ctx, key)
		return assetCreated{}, problems.PayloadTooLarge()
	}
	if filename == "" {
		filename = "image" + ext
	}
	id := store.NewID()
	err = m.d.DB.Tx(ctx, func(tx *store.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO kb_assets (id, uploader_id, filename, content_type, size_bytes, sha256, blob_key, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			id, store.Nullable(uploader), path.Base(filename), ct, counted.n, sha, key, store.Now())
		return err
	})
	if err != nil {
		_ = m.d.Blob.Delete(ctx, key)
		return assetCreated{}, err
	}
	return assetCreated{ID: id, URL: AssetPathPrefix + id}, nil
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// assetRank is the visibility rank an image is readable at: the most visible
// published article referencing it (article ∧ category). Unreferenced images
// are staff-only (rank 3). Cached per KB generation.
func (m *Module) assetRank(ctx context.Context, id string) (int, error) {
	key := fmt.Sprintf("%d|%s", m.gen.Load(), id)
	if v, ok := m.assetVis.Get(key); ok {
		return v, nil
	}
	rows, err := m.d.DB.Read().QueryContext(ctx, `SELECT a.visibility, c.visibility FROM kb_articles a JOIN kb_categories c ON c.id = a.category_id
		WHERE a.status = 'published' AND (a.id = (SELECT article_id FROM kb_assets WHERE id = ?)
			OR EXISTS (SELECT 1 FROM kb_article_translations t WHERE t.article_id = a.id AND t.body_md LIKE ?))`, id, "%/kb/assets/"+id+"%")
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	rank := 3
	for rows.Next() {
		var av, cv string
		if err := rows.Scan(&av, &cv); err != nil {
			return 0, err
		}
		rank = min(rank, max(visRank(av), visRank(cv)))
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	m.assetVis.Put(key, rank, 8, 0)
	return rank, nil
}

func (m *Module) handleAsset(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := strings.ToLower(param(r, "id"))
	if !validID(id) || m.d.Blob == nil {
		problems.Write(w, r, problems.NotFound())
		return
	}
	var key, ct, sha string
	err := m.d.DB.Read().QueryRowContext(ctx, `SELECT blob_key, content_type, sha256 FROM kb_assets WHERE id = ?`, id).Scan(&key, &ct, &sha)
	if err != nil {
		problems.Write(w, r, store.NotFound(err))
		return
	}
	aud := audienceOf(server.PrincipalFrom(ctx))
	rank := 0
	if aud != audStaff {
		if !m.support(ctx).KBEnabled {
			problems.Write(w, r, problems.NotFound())
			return
		}
		if rank, err = m.assetRank(ctx, id); err != nil {
			problems.Write(w, r, err)
			return
		}
		if rank > aud.maxRank() {
			problems.Write(w, r, problems.NotFound()) // never 403: existence stays hidden
			return
		}
	}
	rc, _, err := m.d.Blob.Open(ctx, key)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	defer rc.Close()
	h := w.Header()
	h.Set("Content-Type", ct)
	h.Set("ETag", `"`+sha+`"`)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'")
	if aud == audAnonymous && rank == 0 {
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		h.Set("Cache-Control", "private, max-age=3600")
	}
	http.ServeContent(w, r, "", time.Time{}, rc)
}
