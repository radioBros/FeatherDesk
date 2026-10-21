// SPDX-License-Identifier: AGPL-3.0-only

package privacy

import (
	"context"
	"strconv"
	"unicode/utf8"

	"github.com/radioBros/SimpleTickets/internal/problems"
	"github.com/radioBros/SimpleTickets/internal/settings"
)

// Settings keys (D-19 §C "retention settings keys"): `retention` holds the
// policy, `privacy` the erasure and consent options.
const (
	KeyRetention = "retention"
	KeyPrivacy   = "privacy"
)

// Retention actions for closed tickets.
const (
	ActionPurge     = "purge"
	ActionAnonymize = "anonymize"
)

// Retention is the policy run by the daily job. The platform's built-in
// prunes (raw inbound mail after 90 days, audit rows after
// ST_AUDIT_RETENTION_DAYS) keep running; the policy can only shorten them.
type Retention struct {
	ClosedTicketsMonths int    `json:"closed_tickets_months"` // 0 = keep forever
	ClosedTicketsAction string `json:"closed_tickets_action"` // purge | anonymize
	InboundEmailDays    int    `json:"inbound_email_days"`    // 1…90
	AuditLogMonths      int    `json:"audit_log_months"`      // 0 = off (ST_AUDIT_RETENTION_DAYS only), 1…120
	ExportDays          int    `json:"export_days"`           // 1…30
}

// Options are the erasure and consent options.
type Options struct {
	ErasureAutoApproveDays int    `json:"erasure_auto_approve_days"` // 0 = admins decide
	ErasureAutoApproveMode string `json:"erasure_auto_approve_mode"`
	PolicyVersion          string `json:"policy_version"`
}

// Settings is the API shape of GET/PUT /admin/privacy/settings.
type Settings struct {
	Retention              Retention `json:"retention"`
	ErasureAutoApproveDays int       `json:"erasure_auto_approve_days"`
	ErasureAutoApproveMode string    `json:"erasure_auto_approve_mode"`
	PolicyVersion          string    `json:"policy_version"`
}

// DefaultRetention: closed tickets are kept, raw inbound mail 90 days (the
// built-in prune), the audit log as long as ST_AUDIT_RETENTION_DAYS says (the
// policy's audit rule is off until an admin sets it, so an upgrade never
// deletes audit rows the operator chose to keep), exports 7 days.
func DefaultRetention() Retention {
	return Retention{ClosedTicketsMonths: 0, ClosedTicketsAction: ActionAnonymize, InboundEmailDays: 90, AuditLogMonths: 0, ExportDays: 7}
}

// DefaultOptions: no auto-approval.
func DefaultOptions() Options {
	return Options{ErasureAutoApproveDays: 0, ErasureAutoApproveMode: ModeAnonymize}
}

// LoadSettings reads both keys, falling back to the defaults field by field.
func LoadSettings(ctx context.Context, s *settings.Service) (Settings, error) {
	r, o := DefaultRetention(), DefaultOptions()
	if s != nil {
		if err := s.Get(ctx, KeyRetention, &r); err != nil {
			return Settings{}, err
		}
		if err := s.Get(ctx, KeyPrivacy, &o); err != nil {
			return Settings{}, err
		}
	}
	r = r.normalized()
	if !validMode(o.ErasureAutoApproveMode) {
		o.ErasureAutoApproveMode = ModeAnonymize
	}
	return Settings{Retention: r, ErasureAutoApproveDays: o.ErasureAutoApproveDays, ErasureAutoApproveMode: o.ErasureAutoApproveMode, PolicyVersion: o.PolicyVersion}, nil
}

// normalized replaces out-of-range stored values with the defaults (a value
// stored by an older or hand-edited row must never widen the policy).
func (r Retention) normalized() Retention {
	d := DefaultRetention()
	if r.ClosedTicketsMonths < 0 || r.ClosedTicketsMonths > 240 {
		r.ClosedTicketsMonths = d.ClosedTicketsMonths
	}
	if r.ClosedTicketsAction != ActionPurge && r.ClosedTicketsAction != ActionAnonymize {
		r.ClosedTicketsAction = d.ClosedTicketsAction
	}
	if r.InboundEmailDays < 1 || r.InboundEmailDays > 90 {
		r.InboundEmailDays = d.InboundEmailDays
	}
	if r.AuditLogMonths < 0 || r.AuditLogMonths > 120 {
		r.AuditLogMonths = d.AuditLogMonths
	}
	if r.ExportDays < 1 || r.ExportDays > 30 {
		r.ExportDays = d.ExportDays
	}
	return r
}

func validMode(m string) bool { return m == ModeAnonymize || m == ModeRedact }

func rangeField(fe []problems.FieldError, field string, v, lo, hi int) []problems.FieldError {
	if v < lo || v > hi {
		return append(fe, problems.Field(field, "range", strconv.Itoa(lo)+" to "+strconv.Itoa(hi)))
	}
	return fe
}

// validate checks a PUT body.
func (in Settings) validate() []problems.FieldError {
	var fe []problems.FieldError
	r := in.Retention
	fe = rangeField(fe, "retention.closed_tickets_months", r.ClosedTicketsMonths, 0, 240)
	if r.ClosedTicketsAction != ActionPurge && r.ClosedTicketsAction != ActionAnonymize {
		fe = append(fe, problems.Field("retention.closed_tickets_action", "enum", "purge or anonymize"))
	}
	fe = rangeField(fe, "retention.inbound_email_days", r.InboundEmailDays, 1, 90)
	fe = rangeField(fe, "retention.audit_log_months", r.AuditLogMonths, 0, 120)
	fe = rangeField(fe, "retention.export_days", r.ExportDays, 1, 30)
	fe = rangeField(fe, "erasure_auto_approve_days", in.ErasureAutoApproveDays, 0, 365)
	if !validMode(in.ErasureAutoApproveMode) {
		fe = append(fe, problems.Field("erasure_auto_approve_mode", "enum", "anonymize or anonymize_and_redact"))
	}
	if utf8.RuneCountInString(in.PolicyVersion) > 64 {
		fe = append(fe, problems.Field("policy_version", "max_length", "at most 64 characters"))
	}
	return fe
}

// save stores both keys.
func saveSettings(ctx context.Context, s *settings.Service, in Settings, actorID string) error {
	if err := s.Put(ctx, KeyRetention, in.Retention, actorID); err != nil {
		return err
	}
	return s.Put(ctx, KeyPrivacy, Options{ErasureAutoApproveDays: in.ErasureAutoApproveDays,
		ErasureAutoApproveMode: in.ErasureAutoApproveMode, PolicyVersion: in.PolicyVersion}, actorID)
}

func itoa(n int) string { return strconv.Itoa(n) }
