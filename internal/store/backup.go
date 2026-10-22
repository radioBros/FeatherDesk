// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// BlobPrefixes are the blob store sub-directories archived by Backup when the
// blob backend is local (mirrors blob.Prefixes; kept here to avoid an import cycle).
var BlobPrefixes = []string{"attachments", "branding", "kb", "inbound"}

const (
	backupDBFile    = "featherdesk.db"
	backupBlobsFile = "blobs.tar.gz"
	backupManifest  = "manifest.json"
)

type BackupOptions struct {
	OutDir   string    // parent directory; a timestamped sub-directory is created
	BlobRoot string    // local blob root to archive; "" when blobs live in S3
	Now      time.Time // defaults to time.Now
}

// Manifest describes a backup directory; every file is checksummed.
type Manifest struct {
	CreatedAt     time.Time         `json:"created_at"`
	Engine        string            `json:"engine"`
	SchemaVersion int               `json:"schema_version"`
	Files         map[string]string `json:"files"` // name -> sha256
	PGDumpHint    string            `json:"pg_dump_hint,omitempty"`
	BlobsSkipped  bool              `json:"blobs_skipped,omitempty"`
}

type BackupResult struct {
	Dir      string
	Manifest Manifest
}

// Backup writes a consistent backup (DB-015, DB-030):
//   - SQLite: `VACUUM INTO` (consistent while the app keeps running) + blobs tar + manifest.
//   - Postgres: blobs tar + manifest + a pg_dump hint (the distroless image ships no pg_dump).
//
// With BlobRoot == "" (S3 backend) blobs are skipped: bucket versioning is the operator's job (D-17).
func Backup(ctx context.Context, db *DB, opt BackupOptions) (BackupResult, error) {
	if opt.Now.IsZero() {
		opt.Now = time.Now()
	}
	dir := filepath.Join(opt.OutDir, "featherdesk-"+opt.Now.UTC().Format("20060102-150405"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return BackupResult{}, err
	}
	ver, err := db.SchemaVersion(ctx)
	if err != nil {
		return BackupResult{}, err
	}
	m := Manifest{CreatedAt: opt.Now.UTC(), Engine: db.Dialect.String(), SchemaVersion: ver, Files: map[string]string{}}
	if db.Dialect == SQLite {
		dbPath := filepath.Join(dir, backupDBFile)
		if _, err := db.W.ExecContext(ctx, `VACUUM INTO ?`, dbPath); err != nil {
			return BackupResult{}, fmt.Errorf("backup: vacuum into: %w", err)
		}
	} else {
		m.PGDumpHint = PGDumpHint(db.URL)
	}
	if opt.BlobRoot != "" {
		if err := tarBlobs(ctx, opt.BlobRoot, filepath.Join(dir, backupBlobsFile)); err != nil {
			return BackupResult{}, err
		}
	} else {
		m.BlobsSkipped = true
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		s, err := fileSHA256(filepath.Join(dir, e.Name()))
		if err != nil {
			return BackupResult{}, err
		}
		m.Files[e.Name()] = s
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, backupManifest), b, 0o600); err != nil {
		return BackupResult{}, err
	}
	return BackupResult{Dir: dir, Manifest: m}, nil
}

// PGDumpHint is the command printed for Postgres backups (password redacted).
func PGDumpHint(rawURL string) string {
	u, err := url.Parse(rawURL)
	db, user := "featherdesk", "featherdesk"
	if err == nil {
		if p := strings.TrimPrefix(u.Path, "/"); p != "" {
			db = p
		}
		if u.User != nil && u.User.Username() != "" {
			user = u.User.Username()
		}
	}
	return fmt.Sprintf("docker compose exec -T postgres pg_dump -U %s -Fc %s > featherdesk.pgdump   (restore: pg_restore -U %s -d %s --clean featherdesk.pgdump)", user, db, user, db)
}

type RestoreOptions struct {
	BackupDir string
	DBPath    string // SQLite target file (ignored for Postgres backups)
	BlobRoot  string // local blob root; "" to skip blobs
	Force     bool   // overwrite an existing DB file
}

// Restore verifies every checksum in the manifest, then puts the SQLite file and
// blobs back (DB-016). It is an offline operation: stop the app first.
func Restore(ctx context.Context, opt RestoreOptions) (Manifest, error) {
	var m Manifest
	b, err := os.ReadFile(filepath.Join(opt.BackupDir, backupManifest))
	if err != nil {
		return m, fmt.Errorf("restore: manifest: %w", err)
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, fmt.Errorf("restore: manifest: %w", err)
	}
	for name, want := range m.Files {
		got, err := fileSHA256(filepath.Join(opt.BackupDir, name))
		if err != nil {
			return m, err
		}
		if got != want {
			return m, fmt.Errorf("restore: %s checksum mismatch (backup corrupted or tampered)", name)
		}
	}
	if ms, err := Migrations(SQLite); err == nil && len(ms) > 0 && m.SchemaVersion > ms[len(ms)-1].Version {
		return m, fmt.Errorf("restore: backup schema version %d is newer than this binary (%d); restore it with the release that wrote it", m.SchemaVersion, ms[len(ms)-1].Version)
	}
	if m.Engine == SQLite.String() {
		if opt.DBPath == "" {
			return m, errors.New("restore: DBPath is required for a SQLite backup")
		}
		_, statErr := os.Stat(opt.DBPath)
		exists := statErr == nil
		if exists && !opt.Force {
			return m, fmt.Errorf("restore: %s exists (use --force to overwrite)", opt.DBPath)
		}
		if err := checkBackupDB(ctx, filepath.Join(opt.BackupDir, backupDBFile), filepath.Dir(opt.DBPath)); err != nil {
			return m, err
		}
		if exists {
			// Keep the replaced database next to it (DB-016); one generation.
			for _, sfx := range []string{"", "-wal", "-shm"} {
				_ = os.Remove(opt.DBPath + ".pre-restore" + sfx)
				if err := os.Rename(opt.DBPath+sfx, opt.DBPath+".pre-restore"+sfx); err != nil && !errors.Is(err, fs.ErrNotExist) {
					return m, fmt.Errorf("restore: keep previous database: %w", err)
				}
			}
		}
		if err := copyFileAtomic(filepath.Join(opt.BackupDir, backupDBFile), opt.DBPath); err != nil {
			return m, err
		}
		os.Remove(opt.DBPath + "-wal")
		os.Remove(opt.DBPath + "-shm")
	}
	if opt.BlobRoot != "" && !m.BlobsSkipped {
		if err := untarBlobs(ctx, filepath.Join(opt.BackupDir, backupBlobsFile), opt.BlobRoot); err != nil {
			return m, err
		}
	}
	return m, nil
}

// checkBackupDB runs the integrity check on a scratch copy of a backed-up
// SQLite file (opening it would otherwise create WAL files inside the backup).
// The scratch copy lives next to the target database: the data volume is
// writable even in the read-only container, /tmp may be a small tmpfs.
func checkBackupDB(ctx context.Context, src, scratchParent string) (err error) {
	if err := os.MkdirAll(scratchParent, 0o700); err != nil {
		return err
	}
	dir, err := os.MkdirTemp(scratchParent, ".fd-restore-check-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	p := filepath.Join(dir, backupDBFile)
	if err := copyFileAtomic(src, p); err != nil {
		return err
	}
	db, err := Open(ctx, "sqlite://"+p, Options{Readers: 1})
	if err != nil {
		return fmt.Errorf("restore: backup database does not open: %w", err)
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	if err := IntegrityCheck(ctx, db); err != nil {
		return fmt.Errorf("restore: backup database: %w", err)
	}
	return nil
}

// IntegrityCheck runs PRAGMA quick_check on SQLite and a schema probe on Postgres (DB-017).
func IntegrityCheck(ctx context.Context, db *DB) error {
	if db.Dialect == Postgres {
		var n int
		return db.R.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&n)
	}
	rows, err := db.R.QueryContext(ctx, `PRAGMA quick_check`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var problems []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return err
		}
		if s != "ok" {
			problems = append(problems, s)
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("integrity check failed: %s", strings.Join(problems, "; "))
	}
	return rows.Err()
}

func fileSHA256(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func copyFileAtomic(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".restore-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dst)
}

func tarBlobs(ctx context.Context, root, out string) error {
	f, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, prefix := range BlobPrefixes {
		base := filepath.Join(root, prefix)
		if _, err := os.Stat(base); errors.Is(err, fs.ErrNotExist) {
			continue
		}
		var files []string
		err := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.Type().IsRegular() {
				files = append(files, p)
			}
			return nil
		})
		if err != nil {
			return err
		}
		sort.Strings(files)
		for _, p := range files {
			if err := ctx.Err(); err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, p)
			st, err := os.Stat(p)
			if err != nil {
				return err
			}
			if err := tw.WriteHeader(&tar.Header{Name: filepath.ToSlash(rel), Mode: 0o600, Size: st.Size(), ModTime: st.ModTime(), Typeflag: tar.TypeReg}); err != nil {
				return err
			}
			src, err := os.Open(p)
			if err != nil {
				return err
			}
			_, err = io.Copy(tw, src)
			src.Close()
			if err != nil {
				return err
			}
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	return f.Close()
}

func untarBlobs(ctx context.Context, archive, root string) error {
	f, err := os.Open(archive)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		name := filepath.FromSlash(h.Name)
		if h.Typeflag != tar.TypeReg || filepath.IsAbs(name) || strings.Contains(h.Name, "..") {
			return fmt.Errorf("restore: refusing archive entry %q", h.Name)
		}
		dst := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return err
		}
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
		_, err = io.Copy(out, io.LimitReader(tr, h.Size))
		out.Close()
		if err != nil {
			return err
		}
	}
}
