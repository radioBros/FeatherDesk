// SPDX-License-Identifier: AGPL-3.0-only

package directory

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/radioBros/FeatherDesk/internal/authz"
	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/problems"
	"github.com/radioBros/FeatherDesk/internal/settings"
)

// Support settings (TKT-019) over settings.KeySupport. The settings service
// caches the document in memory and invalidates it on write, so request
// paths never read it from the database.

// supportDTO is the SupportSettings schema.
type supportDTO struct {
	TierLabels               map[string]string `json:"tier_labels"`
	AutoCloseDays            int               `json:"auto_close_days"`
	ReopenWindowDays         int               `json:"reopen_window_days"`
	SelfRegistration         bool              `json:"self_registration"`
	CustomerUrgentAllowed    bool              `json:"customer_urgent_allowed"`
	UrgentRequiresSupervisor bool              `json:"urgent_requires_supervisor"`
	TriageVisibleToAgents    bool              `json:"triage_visible_to_agents"`
	PortalEnabled            bool              `json:"portal_enabled"`
	KBEnabled                bool              `json:"kb_enabled"`
	KBSitemapEnabled         bool              `json:"kb_sitemap_enabled"`
	DefaultLocale            string            `json:"default_locale"`
}

func toSupportDTO(s settings.Support) supportDTO {
	labels := map[string]string{}
	for _, k := range []string{"1", "2", "3"} {
		labels[k] = s.TierLabels[k]
	}
	return supportDTO{TierLabels: labels, AutoCloseDays: s.AutoCloseDays, ReopenWindowDays: s.ReopenWindowDays,
		SelfRegistration: s.SelfRegistration, CustomerUrgentAllowed: s.CustomerUrgentAllowed,
		UrgentRequiresSupervisor: s.UrgentRequiresSupervisor, TriageVisibleToAgents: s.TriageVisibleToAgents,
		PortalEnabled: s.PortalEnabled, KBEnabled: s.KBEnabled, KBSitemapEnabled: s.KBSitemapEnabled, DefaultLocale: s.DefaultLocale}
}

// PublicSupport is the allowlisted subset of the support settings that may
// be shown to anonymous visitors (/auth/config, AUTH-016). Nothing else of
// the document is ever public.
func PublicSupport(s settings.Support) map[string]any {
	labels := map[string]string{}
	for _, k := range []string{"1", "2", "3"} {
		labels[k] = s.TierLabels[k]
	}
	return map[string]any{
		"tier_labels":             labels,
		"self_registration":       s.SelfRegistration,
		"customer_urgent_allowed": s.CustomerUrgentAllowed,
		"kb_enabled":              s.KBEnabled,
		"default_locale":          s.DefaultLocale,
		"locales":                 s.Locales,
	}
}

func (d *Directory) getSettings(w http.ResponseWriter, r *http.Request) error {
	if err := dirCan(r, authz.ResSettings, authz.OpRead); err != nil {
		return err
	}
	dirJSON(w, http.StatusOK, toSupportDTO(d.Support(r.Context())))
	return nil
}

// patchSettings merges the given keys and validates each field (422 per field).
func (d *Directory) patchSettings(w http.ResponseWriter, r *http.Request) error {
	if err := dirCan(r, authz.ResSettings, authz.OpWrite); err != nil {
		return err
	}
	var in map[string]json.RawMessage
	if err := dirDecode(r, &in); err != nil {
		return err
	}
	cur := d.Support(r.Context())
	var errs []problems.FieldError
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	boolField := func(k string, dst *bool) {
		if err := json.Unmarshal(in[k], dst); err != nil {
			errs = append(errs, problems.Field(k, "boolean", "true or false"))
		}
	}
	intField := func(k string, dst *int, lo, hi int) {
		var n int
		if err := json.Unmarshal(in[k], &n); err != nil || n < lo || n > hi {
			errs = append(errs, problems.Field(k, "range", rangeMsg(lo, hi)))
			return
		}
		*dst = n
	}
	for _, k := range keys {
		switch k {
		case "tier_labels":
			var labels map[string]string
			if err := json.Unmarshal(in[k], &labels); err != nil {
				errs = append(errs, problems.Field(k, "type", "an object of tier → label"))
				continue
			}
			next := map[string]string{}
			for t, v := range cur.TierLabels {
				next[t] = v
			}
			for t, v := range labels {
				v = strings.TrimSpace(v)
				switch {
				case t != "1" && t != "2" && t != "3":
					errs = append(errs, problems.Field("tier_labels."+t, "invalid", "tiers are 1, 2 and 3"))
				case v == "" || utf8.RuneCountInString(v) > 40:
					errs = append(errs, problems.Field("tier_labels."+t, "length", "1..40 characters"))
				default:
					next[t] = v
				}
			}
			cur.TierLabels = next
		case "auto_close_days":
			intField(k, &cur.AutoCloseDays, 1, 90)
		case "reopen_window_days":
			intField(k, &cur.ReopenWindowDays, 0, 90)
		case "self_registration":
			boolField(k, &cur.SelfRegistration)
		case "customer_urgent_allowed":
			boolField(k, &cur.CustomerUrgentAllowed)
		case "urgent_requires_supervisor":
			boolField(k, &cur.UrgentRequiresSupervisor)
		case "triage_visible_to_agents":
			boolField(k, &cur.TriageVisibleToAgents)
		case "portal_enabled":
			boolField(k, &cur.PortalEnabled)
		case "kb_enabled":
			boolField(k, &cur.KBEnabled)
		case "kb_sitemap_enabled":
			boolField(k, &cur.KBSitemapEnabled)
		case "default_locale":
			var l string
			if err := json.Unmarshal(in[k], &l); err != nil || !domain.ValidLocale(l) {
				errs = append(errs, problems.Field(k, "enum", "one of the shipped locales"))
				continue
			}
			cur.DefaultLocale = l
		default:
			errs = append(errs, problems.Field(k, "unknown", "unknown setting"))
		}
	}
	if len(errs) > 0 {
		return problems.Validation(errs...)
	}
	p := dirPrincipal(r)
	if err := d.set.Put(r.Context(), settings.KeySupport, cur, p.UserID); err != nil {
		return err
	}
	dirJSON(w, http.StatusOK, toSupportDTO(cur))
	return nil
}

func rangeMsg(lo, hi int) string {
	return strings.Join([]string{itoa(lo), itoa(hi)}, "..")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}
