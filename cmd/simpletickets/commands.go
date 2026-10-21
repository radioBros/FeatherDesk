// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/radioBros/SimpleTickets/internal/app"
	"github.com/radioBros/SimpleTickets/internal/blob"
	"github.com/radioBros/SimpleTickets/internal/cli"
	"github.com/radioBros/SimpleTickets/internal/config"
	"github.com/radioBros/SimpleTickets/internal/demo"
	"github.com/radioBros/SimpleTickets/internal/store"
)

func init() {
	cli.Register(cli.Command{Name: "serve", Summary: "Run the server (default)", Run: cmdServe})
	cli.Register(cli.Command{Name: "migrate", Summary: "Apply pending migrations, or `migrate status`", Run: cmdMigrate})
	cli.Register(cli.Command{Name: "healthcheck", Summary: "Exit 0 when /readyz answers 200 (container HEALTHCHECK)", Run: cmdHealthcheck})
	cli.Register(cli.Command{Name: "version", Summary: "Print version and commit", Run: cmdVersion})
	cli.Register(cli.Command{Name: "backup", Summary: "Write a consistent backup (SQLite file + attachments + manifest)", Run: cmdBackup})
	cli.Register(cli.Command{Name: "restore", Summary: "Restore a backup directory (offline)", Run: cmdRestore})
	cli.Register(cli.Command{Name: "seed", Summary: "Fill an empty database with demo data (`seed --demo`: every feature, see docs)", Run: cmdSeed})
	cli.Register(cli.Command{Name: "migrate-engine", Summary: "Copy all data between SQLite and Postgres (offline)", Run: cmdMigrateEngine})
	cli.Register(cli.Command{Name: "blob-migrate", Summary: "Copy blobs between local storage and S3", Run: cmdBlobMigrate})
	cli.Register(cli.Command{Name: "config", Summary: "Validate configuration (`config check`) or print docs (`config docs`)", Run: cmdConfig})
}

func loadConfig() (*config.Config, error) { return config.Load(os.Getenv) }

func openDB(ctx context.Context, cfg *config.Config) (*store.DB, error) {
	return store.Open(ctx, cfg.EffectiveDatabaseURL(), store.Options{Readers: cfg.DBReaders, PoolSize: cfg.DBPool})
}

func cmdServe(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	log := app.NewLogger(cfg)
	if lim := app.ApplyMemoryLimit(os.Getenv); lim > 0 {
		log.Info("memory limit derived from cgroup", "memlimit_mib", lim>>20)
	}
	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return err
	}
	unlock, err := lockDataDir(cfg.DataDir)
	if err != nil {
		return err
	}
	defer unlock()
	a, err := app.Build(ctx, cfg, app.BuildOptions{Logger: log, Version: version, Commit: commit,
		LockInstance: true, BackupDir: filepath.Join(cfg.DataDir, "backups")})
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		return errors.Join(err, a.Close())
	}
	return a.Run(ctx, ln)
}

func cmdMigrate(ctx context.Context, args []string) (err error) {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	db, err := openDB(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	ms, err := store.Migrations(db.Dialect)
	if err != nil {
		return err
	}
	if len(args) > 0 && args[0] == "status" {
		v, _ := db.SchemaVersion(ctx)
		head := 0
		if len(ms) > 0 {
			head = ms[len(ms)-1].Version
		}
		pending, err := db.PendingMigrations(ctx)
		if err != nil {
			return err
		}
		fmt.Printf("engine=%s schema=%d head=%d pending=%t\n", db.Dialect, v, head, len(pending) > 0)
		return nil
	}
	if err := db.Migrate(ctx); err != nil {
		return err
	}
	v, _ := db.SchemaVersion(ctx)
	fmt.Printf("migrated: engine=%s schema=%d\n", db.Dialect, v)
	return nil
}

// cmdHealthcheck hits the local /readyz; distroless has no curl.
func cmdHealthcheck(ctx context.Context, args []string) error {
	addr := os.Getenv("ST_LISTEN_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	c := &http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get("http://" + net.JoinHostPort(host, port) + "/readyz")
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("readyz: %s", resp.Status)
	}
	return nil
}

func cmdVersion(ctx context.Context, args []string) error {
	fmt.Printf("simpletickets %s %s\n", version, commit)
	return nil
}

func cmdBackup(ctx context.Context, args []string) (err error) {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	out := fs.String("out", "", "parent directory (default $ST_DATA_DIR/backups)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if *out == "" {
		*out = filepath.Join(cfg.DataDir, "backups")
	}
	db, err := openDB(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	res, err := store.Backup(ctx, db, store.BackupOptions{OutDir: *out, BlobRoot: localBlobRoot(cfg)})
	if err != nil {
		return err
	}
	fmt.Println("backup written to", res.Dir)
	if res.Manifest.PGDumpHint != "" {
		fmt.Println("Postgres data is not in this backup. Run:\n  " + res.Manifest.PGDumpHint)
	}
	if res.Manifest.BlobsSkipped {
		fmt.Println("Attachments live in S3: use bucket versioning/lifecycle for them.")
	}
	return nil
}

func cmdRestore(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	from := fs.String("from", "", "backup directory (required)")
	force := fs.Bool("force", false, "overwrite an existing database file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *from == "" {
		return fmt.Errorf("restore: --from is required")
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	unlock, err := lockDataDir(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("restore must run while the server is stopped: %w", err)
	}
	defer unlock()
	// DB-016: the server's own single-instance lock (store.LockInstance) too,
	// which also covers a database outside the data directory and Postgres.
	dbURL := cfg.EffectiveDatabaseURL()
	release, err := lockURL(ctx, dbURL)
	if err != nil {
		return fmt.Errorf("restore: %w", err)
	}
	defer release()
	dbPath := ""
	if !strings.HasPrefix(dbURL, "postgres") {
		dbPath = store.SQLiteFile(dbURL)
	}
	m, err := store.Restore(ctx, store.RestoreOptions{BackupDir: *from, DBPath: dbPath, BlobRoot: localBlobRoot(cfg), Force: *force})
	if err != nil {
		return err
	}
	fmt.Printf("restored %s backup from %s (schema %d)\n", m.Engine, m.CreatedAt.Format(time.RFC3339), m.SchemaVersion)
	fmt.Println("Note: queue work in flight at backup time is not in the backup; events after the restored cursor are re-dispatched (duplicate emails are possible).")
	return nil
}

func cmdSeed(ctx context.Context, args []string) (err error) {
	fs := flag.NewFlagSet("seed", flag.ContinueOnError)
	n := fs.Int("tickets", 0, "number of tickets (default 50; with --demo default 60, and the 31 scripted scenarios are always created)")
	demoMode := fs.Bool("demo", false, "fill every part of the app with demo data (organizations, staff, customers, teams, tickets, knowledge base, settings, ...)")
	mailpit := fs.Bool("smtp-mailpit", false, "with --demo: point the email settings at Mailpit (localhost:1025, no auth); otherwise email stays disabled")
	force := fs.Bool("force", false, "with --demo: wipe and re-seed a database that an earlier `seed --demo` filled (never touches other data)")
	password := fs.String("password", demo.DefaultPassword, "with --demo: password of every demo account")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *demoMode {
		sum, err := demo.Run(ctx, os.Getenv, demo.Options{Tickets: *n, SMTPMailpit: *mailpit, Force: *force, Password: *password, Progress: os.Stderr})
		if err != nil {
			return err
		}
		sum.Print(os.Stdout)
		return nil
	}
	var misused []string
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "smtp-mailpit" || f.Name == "force" || f.Name == "password" {
			misused = append(misused, "--"+f.Name)
		}
	})
	if len(misused) > 0 {
		return fmt.Errorf("seed: %s only apply with --demo", strings.Join(misused, ", "))
	}
	if *n == 0 {
		*n = 50
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	db, err := openDB(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	if err := db.Migrate(ctx); err != nil {
		return err
	}
	res, err := store.SeedDemo(ctx, db, store.SeedOptions{Tickets: *n})
	if err != nil {
		return err
	}
	fmt.Printf("seeded %d tickets; accounts:\n", res.Tickets)
	for email, role := range res.Accounts {
		fmt.Printf("  %-36s %s\n", email, role)
	}
	return nil
}

func cmdMigrateEngine(ctx context.Context, args []string) (err error) {
	fs := flag.NewFlagSet("migrate-engine", flag.ContinueOnError)
	from := fs.String("from", "", "source URL (sqlite:///path or postgres://…)")
	to := fs.String("to", "", "target URL")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *from == "" || *to == "" {
		return fmt.Errorf("migrate-engine: --from and --to are required")
	}
	// DB-031: offline only. Hold both engines' single-instance locks for the
	// whole copy, so neither a server nor a second copy can write meanwhile.
	for _, u := range []string{*from, *to} {
		release, err := lockURL(ctx, u)
		if err != nil {
			return fmt.Errorf("migrate-engine: %w", err)
		}
		defer release()
	}
	src, err := store.Open(ctx, *from, store.Options{})
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, src.Close()) }()
	dst, err := store.Open(ctx, *to, store.Options{})
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, dst.Close()) }()
	rep, err := store.MigrateEngine(ctx, src, dst)
	if err != nil {
		return err
	}
	fmt.Printf("%+v\n", rep)
	return nil
}

func cmdBlobMigrate(ctx context.Context, args []string) (err error) {
	fs := flag.NewFlagSet("blob-migrate", flag.ContinueOnError)
	to := fs.String("to", "", "target blob URL (s3://… or file:///path)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *to == "" {
		return fmt.Errorf("blob-migrate: --to is required")
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	src, err := app.OpenBlob(cfg)
	if err != nil {
		return err
	}
	dst, err := blob.NewFromURL(*to)
	if err != nil {
		return err
	}
	db, err := openDB(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	keys, err := blobKeys(ctx, db)
	if err != nil {
		return err
	}
	res, err := blob.Migrate(ctx, src, dst, func(yield func(string) bool) {
		for _, k := range keys {
			if !yield(k) {
				return
			}
		}
	})
	if err != nil {
		return err
	}
	fmt.Printf("copied %d blobs (%d bytes); set ST_BLOB_URL=%s and restart\n", res.Copied, res.Bytes, *to)
	return nil
}

func cmdConfig(ctx context.Context, args []string) error {
	if len(args) > 0 && args[0] == "docs" {
		fmt.Print(config.Markdown())
		return nil
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	for k, v := range cfg.Redacted() {
		fmt.Printf("%s=%s\n", k, v)
	}
	fmt.Println("configuration OK")
	return nil
}

// blobKeys lists every blob the database references.
func blobKeys(ctx context.Context, db *store.DB) ([]string, error) {
	var keys []string
	for _, q := range []string{
		`SELECT storage_key FROM attachments`,
		`SELECT blob_key FROM branding_assets`,
		`SELECT blob_key FROM kb_assets`,
		`SELECT raw_blob_key FROM inbound_messages WHERE raw_blob_key <> ''`,
	} {
		rows, err := db.Read().QueryContext(ctx, q)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var k string
			if err := rows.Scan(&k); err != nil {
				rows.Close()
				return nil, err
			}
			keys = append(keys, k)
		}
		rows.Close()
	}
	return keys, nil
}
