// SPDX-License-Identifier: AGPL-3.0-only

package notify

import "strconv"

// Template data types. Customer templates only ever receive CustomerData,
// whose fields cannot carry internal content (I-7): no internal notes, no
// escalation reason, no tier, team or assignee. TestCustomerViewAllowList
// reflects over these structs against an allow-list, so adding a field
// requires a reviewed test change.

// CustomerTicketView is the public view of a ticket for customer mail.
type CustomerTicketView struct {
	Number  int64
	Ref     string // D-20 reference "KEY-123" ("#n" for events written before projects)
	Subject string
	URL     string // portal deep link ST_PORTAL_URL/tickets/<id>
}

// CustomerMessageView is one public message.
type CustomerMessageView struct {
	AuthorName string
	Body       string // public message Markdown
}

// CustomerData is the data of customer.* templates.
type CustomerData struct {
	Brand         Brand
	RecipientName string
	Ticket        CustomerTicketView
	Message       CustomerMessageView // customer.reply: the public message that triggered it
	Signature     string              // staff author's signature (Markdown), customer.reply only
	CSATURL       string              // customer.resolved one-click rating link ("" = off)
}

func (d *CustomerData) setBrand(b Brand)  { d.Brand = b }
func (d *CustomerData) ticketRef() string { return viewRef(d.Ticket.Ref, d.Ticket.Number) }

// StaffTicketView is the staff view of a ticket.
type StaffTicketView struct {
	ID            string
	Number        int64
	Ref           string // D-20 reference "KEY-123" ("#n" for events written before projects)
	Subject       string
	Priority      string
	Status        string
	TierLabel     string
	TeamName      string
	RequesterName string
	URL           string // agent deep link ST_AGENT_URL/tickets/<id>
}

// StaffMessageView is one message (public or internal) shown to staff.
type StaffMessageView struct {
	AuthorName string
	Body       string
}

// StaffData is the data of staff.* ticket templates.
type StaffData struct {
	Brand         Brand
	RecipientName string
	Ticket        StaffTicketView
	Messages      []StaffMessageView
	ActorName     string
	Reason        string // escalation reason (staff only)
	FromTier      string
	ToTier        string
	SLAWhich      string // first_response | resolution
	SLADue        string
	SLABreached   bool
}

func (d *StaffData) setBrand(b Brand)  { d.Brand = b }
func (d *StaffData) ticketRef() string { return viewRef(d.Ticket.Ref, d.Ticket.Number) }

// GenericData is the data of auth.* and other non-ticket templates.
type GenericData struct {
	Brand         Brand
	RecipientName string
	Vars          map[string]string
}

func (d *GenericData) setBrand(b Brand)  { d.Brand = b }
func (d *GenericData) ticketRef() string { return "" }

// viewRef is the subject tag of a ticket mail: the D-20 reference, or "#n"
// when an event predates projects ("" = not a ticket mail).
func viewRef(ref string, n int64) string {
	switch {
	case ref != "":
		return ref
	case n > 0:
		return "#" + strconv.FormatInt(n, 10)
	}
	return ""
}
