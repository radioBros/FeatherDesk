// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"

	"github.com/radioBros/SimpleTickets/internal/cli"
	"github.com/radioBros/SimpleTickets/internal/crypto"
	"github.com/radioBros/SimpleTickets/internal/settings"
)

func init() {
	cli.Register(cli.Command{Name: "rotate-secrets", Summary: "Re-encrypt stored secrets with ST_SECRET_KEY (old key in ST_SECRET_KEY_PREVIOUS)", Run: cmdRotateSecrets})
}

// cmdRotateSecrets re-seals the SMTP password, webhook secrets and inbound
// mailbox secrets with the current ST_SECRET_KEY (PLT-019). Procedure: set the
// new key as ST_SECRET_KEY and the old one as ST_SECRET_KEY_PREVIOUS, restart,
// run this command, then drop ST_SECRET_KEY_PREVIOUS and restart again.
func cmdRotateSecrets(ctx context.Context, args []string) (err error) {
	fs := flag.NewFlagSet("rotate-secrets", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if len(cfg.SecretKeyPrev) == 0 {
		return errors.New("rotate-secrets: set ST_SECRET_KEY_PREVIOUS to the old key (ST_SECRET_KEY is the new one)")
	}
	box, err := crypto.NewBox(cfg.SecretKey, cfg.SecretKeyPrev)
	if err != nil {
		return err
	}
	db, err := openDB(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	rep, err := settings.RotateSecrets(ctx, db, box)
	if err != nil {
		return err
	}
	fmt.Printf("re-encrypted %d secrets (settings %d, webhooks %d, inbound mailboxes %d); ST_SECRET_KEY_PREVIOUS can now be removed\n",
		rep.Total, rep.Settings, rep.Webhooks, rep.Mailboxes)
	return nil
}
