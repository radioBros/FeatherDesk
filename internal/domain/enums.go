// SPDX-License-Identifier: AGPL-3.0-only

// Package domain holds the enums and core types shared by every module.
// Values mirror development/contracts/domain_model_spec.md; TestEnumsMatchSchema
// and TestEnumsMatchOpenAPI keep the SQL CHECK lists and api/openapi.yaml in sync.
package domain

// UserKind is the hard principal split (D-04). It never changes after insert.
type UserKind string

const (
	KindStaff    UserKind = "staff"
	KindCustomer UserKind = "customer"
)

func (k UserKind) Valid() bool { return k == KindStaff || k == KindCustomer }

// Role covers both staff and customer roles; which ones are legal depends on Kind.
type Role string

const (
	RoleAdmin      Role = "admin"
	RoleSupervisor Role = "supervisor"
	RoleAgent      Role = "agent"
	RoleRequester  Role = "requester"
	RoleOrgManager Role = "org_manager"
)

var StaffRoles = []Role{RoleAdmin, RoleSupervisor, RoleAgent}
var CustomerRoles = []Role{RoleRequester, RoleOrgManager}

// ValidFor reports whether the role is legal for the given kind.
func (r Role) ValidFor(k UserKind) bool {
	list := CustomerRoles
	if k == KindStaff {
		list = StaffRoles
	}
	for _, x := range list {
		if x == r {
			return true
		}
	}
	return false
}

// RoleRank orders staff roles; higher wins when several IdP groups map.
func RoleRank(r Role) int {
	switch r {
	case RoleAdmin:
		return 3
	case RoleSupervisor:
		return 2
	case RoleAgent:
		return 1
	}
	return 0
}

const (
	MinTier = 1
	MaxTier = 3
)

type UserStatus string

const (
	UserActive              UserStatus = "active"
	UserPendingVerification UserStatus = "pending_verification"
	UserDisabled            UserStatus = "disabled"
)

type TicketStatus string

const (
	StatusNew      TicketStatus = "new"
	StatusOpen     TicketStatus = "open"
	StatusPending  TicketStatus = "pending"
	StatusOnHold   TicketStatus = "on_hold"
	StatusResolved TicketStatus = "resolved"
	StatusClosed   TicketStatus = "closed"
)

var TicketStatuses = []TicketStatus{StatusNew, StatusOpen, StatusPending, StatusOnHold, StatusResolved, StatusClosed}

func (s TicketStatus) Valid() bool { return contains(TicketStatuses, s) }

// Final reports whether the status allows no further normal transitions.
func (s TicketStatus) Final() bool { return s == StatusClosed }

type Priority string

const (
	PriorityLow    Priority = "low"
	PriorityNormal Priority = "normal"
	PriorityHigh   Priority = "high"
	PriorityUrgent Priority = "urgent"
)

var Priorities = []Priority{PriorityLow, PriorityNormal, PriorityHigh, PriorityUrgent}

func (p Priority) Valid() bool { return contains(Priorities, p) }

type Channel string

const (
	ChannelPortal Channel = "portal"
	ChannelAgent  Channel = "agent"
	ChannelAPI    Channel = "api"
	ChannelEmail  Channel = "email"
)

var Channels = []Channel{ChannelPortal, ChannelAgent, ChannelAPI, ChannelEmail}

type MessageKind string

const (
	MessagePublic   MessageKind = "public"
	MessageInternal MessageKind = "internal"
	MessageSystem   MessageKind = "system"
)

var MessageKinds = []MessageKind{MessagePublic, MessageInternal, MessageSystem}

type Audience string

const (
	AudienceStaff    Audience = "staff"
	AudienceCustomer Audience = "customer"
)

type LinkType string

const (
	LinkParentOf    LinkType = "parent_of"
	LinkRelatedTo   LinkType = "related_to"
	LinkDuplicateOf LinkType = "duplicate_of"
)

var LinkTypes = []LinkType{LinkParentOf, LinkRelatedTo, LinkDuplicateOf}

type FieldType string

const (
	FieldText        FieldType = "text"
	FieldTextarea    FieldType = "textarea"
	FieldNumber      FieldType = "number"
	FieldDate        FieldType = "date"
	FieldSelect      FieldType = "select"
	FieldMultiselect FieldType = "multiselect"
	FieldCheckbox    FieldType = "checkbox"
)

var FieldTypes = []FieldType{FieldText, FieldTextarea, FieldNumber, FieldDate, FieldSelect, FieldMultiselect, FieldCheckbox}

type KBVisibility string

const (
	KBPublic    KBVisibility = "public"
	KBCustomers KBVisibility = "customers"
	KBInternal  KBVisibility = "internal"
)

// KBVisibilityRank: wider visibility has a lower rank (M-10: article rank ≥ category rank).
func KBVisibilityRank(v KBVisibility) int {
	switch v {
	case KBPublic:
		return 0
	case KBCustomers:
		return 1
	case KBInternal:
		return 2
	}
	return -1
}

type KBStatus string

const (
	KBDraft     KBStatus = "draft"
	KBPublished KBStatus = "published"
	KBArchived  KBStatus = "archived"
)

// Locales shipped in v1.0 (D-15). The first is the fallback.
var Locales = []string{"en", "it", "de", "fr", "es"}

func ValidLocale(l string) bool { return contains(Locales, l) }

func contains[T comparable](list []T, v T) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
