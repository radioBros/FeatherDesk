// SPDX-License-Identifier: AGPL-3.0-only

package authn

import (
	"context"
	"errors"
	"flag"
	"fmt"

	"github.com/radioBros/FeatherDesk/internal/cli"
	"github.com/radioBros/FeatherDesk/internal/store"
)

func init() {
	cli.Register(cli.Command{Name: "reset-mfa", Summary: "Remove a user's two-factor authentication (lost device, break-glass recovery)",
		Usage: "reset-mfa --email <email>", Run: runResetMFA})
}

// runResetMFA is the offline counterpart of POST /admin/users/{id}/mfa/reset:
// the last admin who lost their authenticator and recovery codes can still
// get in (the break-glass path stays intact under any MFA policy).
func runResetMFA(ctx context.Context, args []string) (err error) {
	fs := flag.NewFlagSet("reset-mfa", flag.ContinueOnError)
	email := fs.String("email", "", "user email (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *email == "" {
		return errors.New("reset-mfa: --email is required")
	}
	db, err := openCLIStore(ctx)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	addr := normEmail(*email)
	err = db.Tx(ctx, func(tx *store.Tx) error {
		u, err := loadUser(ctx, tx, "u.email = ?", addr)
		if err != nil {
			return err
		}
		return ResetMFA(ctx, tx, u.ID, "", "", "cli")
	})
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("reset-mfa: no user with email %s", addr)
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(cliStdout, "two-factor authentication removed for %s\n", addr)
	return nil
}
