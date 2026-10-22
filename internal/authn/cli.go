// SPDX-License-Identifier: AGPL-3.0-only

package authn

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/radioBros/FeatherDesk/internal/cli"
	"github.com/radioBros/FeatherDesk/internal/config"
	"github.com/radioBros/FeatherDesk/internal/store"
)

func init() {
	cli.Register(cli.Command{Name: "create-admin", Summary: "Create a local break-glass admin",
		Usage: "create-admin --email <email> [--name <name>] [--password-stdin]", Run: runCreateAdmin})
	cli.Register(cli.Command{Name: "reset-password", Summary: "Set a new local password and revoke the user's sessions",
		Usage: "reset-password --email <email> [--password-stdin]", Run: runResetPassword})
}

// cliIO is swapped by tests.
var (
	cliStdin  io.Reader = os.Stdin
	cliStdout io.Writer = os.Stdout
	cliGetenv           = os.Getenv
)

func openCLIStore(ctx context.Context) (*store.DB, error) {
	cfg, err := config.Load(cliGetenv)
	if err != nil {
		return nil, err
	}
	db, err := store.Open(ctx, cfg.EffectiveDatabaseURL(), store.Options{Readers: cfg.DBReaders, PoolSize: cfg.DBPool})
	if err != nil {
		return nil, err
	}
	if err := db.Migrate(ctx); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	return db, nil
}

func readPassword(fromStdin bool) (string, error) {
	if !fromStdin {
		return "", nil
	}
	line, err := bufio.NewReader(cliStdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func runCreateAdmin(ctx context.Context, args []string) (err error) {
	fs := flag.NewFlagSet("create-admin", flag.ContinueOnError)
	email := fs.String("email", "", "admin email (required)")
	name := fs.String("name", "Administrator", "display name")
	stdin := fs.Bool("password-stdin", false, "read the password from the first line of stdin (else one is generated)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *email == "" {
		return errors.New("create-admin: --email is required")
	}
	pw, err := readPassword(*stdin)
	if err != nil {
		return err
	}
	if *stdin && pw == "" {
		return errors.New("create-admin: empty password on stdin")
	}
	db, err := openCLIStore(ctx)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	gen, err := CreateAdmin(ctx, db, *email, *name, pw, "cli")
	if err != nil {
		return err
	}
	fmt.Fprintf(cliStdout, "created break-glass admin %s\n", normEmail(*email))
	if gen != "" {
		fmt.Fprintf(cliStdout, "generated password: %s\n", gen)
	}
	return nil
}

func runResetPassword(ctx context.Context, args []string) (err error) {
	fs := flag.NewFlagSet("reset-password", flag.ContinueOnError)
	email := fs.String("email", "", "user email (required)")
	stdin := fs.Bool("password-stdin", false, "read the password from the first line of stdin (else one is generated)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *email == "" {
		return errors.New("reset-password: --email is required")
	}
	pw, err := readPassword(*stdin)
	if err != nil {
		return err
	}
	if *stdin && pw == "" {
		return errors.New("reset-password: empty password on stdin")
	}
	db, err := openCLIStore(ctx)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	gen, err := ResetPassword(ctx, db, *email, pw)
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("reset-password: no user with email %s", normEmail(*email))
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(cliStdout, "password reset for %s; all sessions revoked\n", normEmail(*email))
	if gen != "" {
		fmt.Fprintf(cliStdout, "generated password: %s\n", gen)
	}
	return nil
}

// SetCLIIO swaps stdin/stdout/getenv of the CLI commands (tests).
func SetCLIIO(in io.Reader, out io.Writer, getenv func(string) string) (restore func()) {
	oi, oo, og := cliStdin, cliStdout, cliGetenv
	cliStdin, cliStdout, cliGetenv = in, out, getenv
	return func() { cliStdin, cliStdout, cliGetenv = oi, oo, og }
}

// RunCLI runs a registered authn command by name (tests).
func RunCLI(ctx context.Context, name string, args []string) error {
	c, ok := cli.Lookup(name)
	if !ok {
		return fmt.Errorf("unknown command %s", name)
	}
	return c.Run(ctx, args)
}
