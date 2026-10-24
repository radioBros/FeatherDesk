// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image/png"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/radioBros/FeatherDesk/internal/authz"
	"github.com/radioBros/FeatherDesk/internal/problems"
	"github.com/radioBros/FeatherDesk/internal/server"
	"github.com/radioBros/FeatherDesk/internal/settings"
	"github.com/radioBros/FeatherDesk/internal/store"
)

const (
	pwaVariant192      = "pwa-192"
	pwaVariant512      = "pwa-512"
	pwaVariantMaskable = "pwa-maskable-512"
	pwaVariantApple    = "pwa-apple-180"
)

var pwaIconFields = []struct {
	form    string
	variant string
	size    int
}{
	{"icon_192", pwaVariant192, 192},
	{"icon_512", pwaVariant512, 512},
	{"maskable_512", pwaVariantMaskable, 512},
	{"apple_180", pwaVariantApple, 180},
}

func (m *Module) uploadPwaIcons(w http.ResponseWriter, r *http.Request) error {
	if authz.CanAdmin(principal(r), authz.ResBranding, authz.OpWrite) != nil {
		return problems.Forbidden("")
	}
	parts, source, err := readPwaUpload(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	s := m.svc
	p := principal(r)
	dto, err := s.storePwaIcons(ctx, parts, source, p.UserID, server.ClientIP(r))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, dto)
	return nil
}

func (m *Module) deletePwaIcons(w http.ResponseWriter, r *http.Request) error {
	if authz.CanAdmin(principal(r), authz.ResBranding, authz.OpWrite) != nil {
		return problems.Forbidden("")
	}
	ctx := r.Context()
	s := m.svc
	if err := s.deletePwaIcons(ctx, principal(r).UserID, server.ClientIP(r)); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func readPwaUpload(r *http.Request) (map[string][]byte, string, error) {
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, "", problems.Validation(problems.Field("file", "required", "multipart/form-data"))
	}
	out := map[string][]byte{}
	var source string
	for {
		part, err := mr.NextPart()
		if err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				return nil, "", problems.PayloadTooLarge()
			}
			break
		}
		name := part.FormName()
		if name == "source" {
			b, _ := io.ReadAll(io.LimitReader(part, 256))
			_ = part.Close()
			source = strings.TrimSpace(string(b))
			continue
		}
		found := false
		for _, f := range pwaIconFields {
			if f.form == name {
				found = true
				data, err := io.ReadAll(io.LimitReader(part, MaxBrandingAsset+1))
				_ = part.Close()
				if err != nil {
					var mbe *http.MaxBytesError
					if errors.As(err, &mbe) {
						return nil, "", problems.PayloadTooLarge()
					}
					return nil, "", err
				}
				if len(data) > MaxBrandingAsset {
					return nil, "", problems.PayloadTooLarge()
				}
				out[f.form] = data
				break
			}
		}
		if !found {
			_ = part.Close()
		}
	}
	for _, f := range pwaIconFields {
		if out[f.form] == nil {
			return nil, "", problems.Validation(problems.Field(f.form, "required", "a PNG file is required"))
		}
		clean, err := validatePwaPNG(f.form, out[f.form], f.size)
		if err != nil {
			return nil, "", err
		}
		out[f.form] = clean
	}
	if source == "" {
		source = "default"
	}
	return out, source, nil
}

// validatePwaPNG checks the exact size and re-encodes the PNG like every other
// branding raster, so nothing but pixels is stored (no text chunks or trailing data).
func validatePwaPNG(field string, data []byte, size int) ([]byte, error) {
	if http.DetectContentType(data) != "image/png" {
		return nil, problems.Validation(problems.Field(field, "invalid_image", "the image must be a PNG"))
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, problems.Validation(problems.Field(field, "invalid_image", "the image could not be decoded"))
	}
	if cfg.Width != size || cfg.Height != size {
		return nil, problems.Validation(problems.Field(field, "invalid_image", fmt.Sprintf("image must be exactly %d×%d pixels", size, size)))
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, problems.Validation(problems.Field(field, "invalid_image", "the image could not be decoded"))
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (s *Service) storePwaIcons(ctx context.Context, parts map[string][]byte, source, actor, ip string) (*brandingPwaIconsDTO, error) {
	var oldKeys []string
	stored := settings.BrandingPwaIcons{Source: source, GeneratedAt: time.Now().UTC().Format(time.RFC3339)}
	err := s.db.Tx(ctx, func(tx *store.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT blob_key FROM branding_assets WHERE slot = 'icon' AND variant LIKE 'pwa-%'`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var k string
			_ = rows.Scan(&k)
			oldKeys = append(oldKeys, k)
		}
		rows.Close()
		for _, f := range pwaIconFields {
			data := parts[f.form]
			sum := sha256.Sum256(data)
			sha := hex.EncodeToString(sum[:])
			key := "branding/" + sha + ".png"
			if _, err := s.blob.Put(ctx, key, bytes.NewReader(data), int64(len(data)), "image/png"); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO branding_assets (id, slot, variant, blob_key, content_type, width, height, size_bytes, sha256, created_at)
				VALUES (?, 'icon', ?, ?, 'image/png', ?, ?, ?, ?, ?)
				ON CONFLICT (slot, variant) DO UPDATE SET blob_key = excluded.blob_key, content_type = excluded.content_type, width = excluded.width,
					height = excluded.height, size_bytes = excluded.size_bytes, sha256 = excluded.sha256, created_at = excluded.created_at`,
				store.NewID(), f.variant, key, f.size, f.size, len(data), sha, store.Now()); err != nil {
				return err
			}
			fname := sha + ".png"
			switch f.variant {
			case pwaVariant192:
				stored.Icon192 = fname
			case pwaVariant512:
				stored.Icon512 = fname
			case pwaVariantMaskable:
				stored.Maskable512 = fname
			case pwaVariantApple:
				stored.Apple180 = fname
			}
		}
		return auditBranding(ctx, tx, actor, ip, "branding.pwa_icons_updated", map[string]any{"source": source})
	})
	if err != nil {
		return nil, err
	}
	b, err := s.set.Branding(ctx)
	if err != nil {
		return nil, err
	}
	b.PwaIcons = stored
	if err := s.set.Put(ctx, settings.KeyBranding, b, actor); err != nil {
		return nil, err
	}
	s.deleteUnreferencedBlobs(ctx, oldKeys)
	return &brandingPwaIconsDTO{
		Icon192: stored.Icon192, Icon512: stored.Icon512, Maskable512: stored.Maskable512,
		Apple180: stored.Apple180, Source: stored.Source, GeneratedAt: stored.GeneratedAt,
	}, nil
}

func (s *Service) deletePwaIcons(ctx context.Context, actor, ip string) error {
	var keys []string
	err := s.db.Tx(ctx, func(tx *store.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT blob_key FROM branding_assets WHERE slot = 'icon' AND variant LIKE 'pwa-%'`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var k string
			_ = rows.Scan(&k)
			keys = append(keys, k)
		}
		rows.Close()
		if _, err := tx.ExecContext(ctx, `DELETE FROM branding_assets WHERE slot = 'icon' AND variant LIKE 'pwa-%'`); err != nil {
			return err
		}
		return auditBranding(ctx, tx, actor, ip, "branding.pwa_icons_reset", nil)
	})
	if err != nil {
		return err
	}
	b, err := s.set.Branding(ctx)
	if err != nil {
		return err
	}
	b.PwaIcons = settings.BrandingPwaIcons{}
	if err := s.set.Put(ctx, settings.KeyBranding, b, actor); err != nil {
		return err
	}
	s.deleteUnreferencedBlobs(ctx, keys)
	return nil
}

func auditBranding(ctx context.Context, tx *store.Tx, actor, ip, action string, data map[string]any) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO audit_log (id, actor_id, action, target_type, target_id, data, ip, created_at) VALUES (?, ?, ?, 'branding', 'pwa_icons', ?, ?, ?)`,
		store.NewID(), store.Nullable(actor), action, store.MustJSON(data), ip, store.Now())
	return err
}
