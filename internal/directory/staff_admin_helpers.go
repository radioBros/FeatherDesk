// SPDX-License-Identifier: AGPL-3.0-only

package directory

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/mail"
	"strings"

	"github.com/radioBros/SimpleTickets/internal/problems"
	"github.com/radioBros/SimpleTickets/internal/store"
)

func staffJSONEncode(w io.Writer, v any) error { return json.NewEncoder(w).Encode(v) }

func staffDecode(r *http.Request, dst any) error {
	if r.Body == nil {
		return problems.Validation(problems.Field("body", "required", ""))
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(dst); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return problems.PayloadTooLarge()
		}
		return problems.Validation(problems.Field("body", "invalid_json", "malformed JSON body"))
	}
	return nil
}

func staffValidEmail(s string) bool {
	if s == "" || len(s) > 254 || strings.ContainsAny(s, " <>\r\n") {
		return false
	}
	a, err := mail.ParseAddress(s)
	return err == nil && a.Address == s
}

// staffLockAdmins serialises last-admin checks on Postgres (READ COMMITTED):
// the active admin rows are locked for the transaction. SQLite write
// transactions are already exclusive.
func staffLockAdmins(ctx context.Context, tx *store.Tx) error {
	if tx.Dialect() != store.Postgres {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM users WHERE kind = 'staff' AND role = 'admin' AND status = 'active' FOR UPDATE`)
	if err != nil {
		return err
	}
	for rows.Next() {
	}
	rows.Close()
	return rows.Err()
}
