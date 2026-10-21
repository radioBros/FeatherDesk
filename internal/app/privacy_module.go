// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"

	"github.com/radioBros/SimpleTickets/internal/authn"
	"github.com/radioBros/SimpleTickets/internal/privacy"
	"github.com/radioBros/SimpleTickets/internal/tickets"
)

// privacyModule adapts Deps for the GDPR tooling (D-19 §C; privacy cannot
// import app). It wires:
//   - the registration consent recorder into authn (authn cannot import privacy);
//   - the synchronous search reindex of redacted tickets through the tickets
//     service built earlier in the same Build (builtModules, read without
//     deleting: mailinModule consumes the entry last).
func privacyModule(d *Deps) (Module, error) {
	authn.SetRegisterConsent(privacy.RecordConsent)
	var svc *tickets.Service
	if prev, ok := builtModules.Load(d); ok {
		list, _ := prev.([]Module)
		for _, m := range list {
			if p, ok := m.(interface{ Service() *tickets.Service }); ok {
				svc = p.Service()
				break
			}
		}
	}
	var reindex func(ctx context.Context, ids []string) error
	if svc != nil {
		reindex = func(ctx context.Context, ids []string) error {
			for _, id := range ids {
				if err := svc.IndexOne(ctx, id); err != nil {
					return err
				}
			}
			return nil
		}
	}
	return privacy.New(privacy.Deps{Cfg: d.Cfg, DB: d.DB, Log: d.Log, Queue: d.Queue, Blob: d.Blob, Settings: d.Settings,
		Server: d.Server, Reindex: reindex})
}
