// SPDX-License-Identifier: AGPL-3.0-only

package kb

import (
	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/problems"
)

// KB authorisation (KB-013, permissions_spec §3 "KB" row and §5 scopes).
// Only domain.Principal helpers are used, so the rules stay pure functions.
//
//	action               anonymous  customer  agent  supervisor  admin
//	kb.read (I-10)       public     +customers  all    all         all
//	kb.draft             ✗          ✗          ✓      ✓           ✓
//	kb.publish/archive   ✗          ✗          ✗      ✓           ✓
//	kb.category.manage   ✗          ✗          ✗      ✓           ✓
//	kb.delete (drafts)   ✗          ✗          ✗      ✗           ✓
//
// PAT scopes: reads need kb:read (implied by tickets:read); writes need
// kb:write (admin implies everything). Effective = role ∩ scopes (I-6).

// Action names, shared with authz's action vocabulary.
const (
	ActionRead           = "kb.read"
	ActionDraft          = "kb.draft"
	ActionPublish        = "kb.publish"
	ActionArchive        = "kb.archive"
	ActionCategoryManage = "kb.category.manage"
	ActionDelete         = "kb.delete"
)

// Visibility values and their rank (public < customers < internal).
const (
	VisPublic    = "public"
	VisCustomers = "customers"
	VisInternal  = "internal"
)

func visRank(v string) int {
	switch v {
	case VisPublic:
		return 0
	case VisCustomers:
		return 1
	case VisInternal:
		return 2
	}
	return -1
}

// audience is the I-10 reader class of a principal.
type audience int

const (
	audAnonymous audience = iota
	audCustomer
	audStaff
)

func (a audience) String() string {
	switch a {
	case audCustomer:
		return "customer"
	case audStaff:
		return "staff"
	}
	return "anonymous"
}

// visibilities lists the article/category visibilities the audience may read.
func (a audience) visibilities() []string {
	switch a {
	case audStaff:
		return []string{VisPublic, VisCustomers, VisInternal}
	case audCustomer:
		return []string{VisPublic, VisCustomers}
	}
	return []string{VisPublic}
}

// maxRank is the widest visibility rank the audience can read.
func (a audience) maxRank() int { return len(a.visibilities()) - 1 }

// audienceOf maps a principal to its I-10 class. A token without kb:read
// reads like an anonymous visitor.
func audienceOf(p *domain.Principal) audience {
	if p == nil || !p.HasScope(domain.ScopeKBRead) {
		return audAnonymous
	}
	if p.IsStaff() {
		return audStaff
	}
	if p.IsCustomer() {
		return audCustomer
	}
	return audAnonymous
}

// Can reports whether p may perform a KB write action. status is the current
// article status for article actions ("" otherwise).
func Can(p *domain.Principal, action, status string) bool {
	if !p.IsStaff() {
		return action == ActionRead // public reads; I-10 filters what is visible
	}
	switch action {
	case ActionRead:
		return p.HasScope(domain.ScopeKBRead)
	case ActionDraft:
		if !p.HasScope(domain.ScopeKBWrite) {
			return false
		}
		// Agents edit drafts only; published or archived content is supervisor+.
		return status == "" || status == "draft" || p.IsSupervisorOrAdmin()
	case ActionPublish, ActionArchive, ActionCategoryManage:
		return p.IsSupervisorOrAdmin() && p.HasScope(domain.ScopeKBWrite)
	case ActionDelete:
		return p.IsAdmin() && p.HasScope(domain.ScopeKBWrite)
	}
	return false
}

func authorize(p *domain.Principal, action, status string) error {
	if Can(p, action, status) {
		return nil
	}
	return problems.Forbidden(action)
}

// statusAction maps a status change to the action it needs.
func statusAction(to string) string {
	if to == "archived" {
		return ActionArchive
	}
	return ActionPublish // publish, unpublish (→ draft), restore
}

// Values of KbArticle.allowed_actions (OpenAPI KbAllowedAction).
const (
	AllowedEdit      = "edit"
	AllowedPublish   = "publish"
	AllowedUnpublish = "unpublish"
	AllowedArchive   = "archive"
	AllowedDelete    = "delete"
)

// AllowedActions lists what p may do with an article in status, so the
// editor shows Publish/Archive/Delete without deciding permissions itself.
// Deleting is for drafts only (published or archived content is archived).
func AllowedActions(p *domain.Principal, status string) []string {
	out := []string{}
	if p == nil || !p.IsStaff() {
		return out
	}
	if Can(p, ActionDraft, status) {
		out = append(out, AllowedEdit)
	}
	if Can(p, ActionPublish, status) {
		if status == "published" {
			out = append(out, AllowedUnpublish)
		} else {
			out = append(out, AllowedPublish)
		}
	}
	if status != "archived" && Can(p, ActionArchive, status) {
		out = append(out, AllowedArchive)
	}
	if status == "draft" && Can(p, ActionDelete, status) {
		out = append(out, AllowedDelete)
	}
	return out
}
