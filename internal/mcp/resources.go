// SPDX-License-Identifier: AGPL-3.0-only

package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/radioBros/SimpleTickets/internal/domain"
)

// Resources: ticket://{ref} (KEY-123, D-20; the ticket as get_ticket returns
// it, JSON; ticket://{number} still resolves)
// and kb://{slug} (the article as Markdown). Both read through the same REST
// routes as the tools, so a resource is visible exactly when the REST object
// is (customers get the portal view: public messages only).

const (
	schemeTicket = "ticket://"
	schemeKB     = "kb://"
	listLimit    = 20
)

func resourceTemplates() []map[string]any {
	return []map[string]any{
		{"uriTemplate": schemeTicket + "{ref}", "name": "ticket", "title": "Ticket",
			"description": "A ticket you can see, with its conversation (JSON, the get_ticket shape).", "mimeType": "application/json"},
		{"uriTemplate": schemeKB + "{slug}", "name": "kb-article", "title": "Knowledge base article",
			"description": "A knowledge base article you may read, as Markdown.", "mimeType": "text/markdown"},
	}
}

// listResources lists the caller's 20 most recently updated tickets and the
// 20 newest knowledge base articles they may read.
func (h *Handler) listResources(r *http.Request, c *caller) (map[string]any, *rpcError) {
	ctx := r.Context()
	out := []map[string]any{}
	if c.can([]string{domain.ScopeTicketsRead}) {
		q := url.Values{"limit": {fmt.Sprint(listLimit)}, "sort": {"-updated_at"}}
		if !c.staff() {
			q.Del("sort") // the portal list is newest-activity first already
		}
		var page struct {
			Items []struct {
				Number  int64  `json:"number"`
				Ref     string `json:"ref"`
				Subject string `json:"subject"`
				Status  string `json:"status"`
			} `json:"items"`
		}
		if _, err := c.api.do(ctx, http.MethodGet, c.area()+"/tickets", q, nil, &page); err != nil && !skippable(err) {
			return nil, internalError(h, r, err)
		}
		for _, t := range page.Items {
			out = append(out, map[string]any{"uri": schemeTicket + t.Ref, "name": "ticket-" + t.Ref,
				"title": t.Ref + " " + t.Subject, "description": "Status: " + t.Status, "mimeType": "application/json"})
		}
	}
	var kb struct {
		Items []struct {
			Slug  string `json:"slug"`
			Title string `json:"title"`
		} `json:"items"`
	}
	if _, err := c.api.do(ctx, http.MethodGet, "/kb/articles", url.Values{"limit": {fmt.Sprint(listLimit)}}, nil, &kb); err != nil && !skippable(err) {
		return nil, internalError(h, r, err)
	}
	for _, a := range kb.Items {
		out = append(out, map[string]any{"uri": schemeKB + a.Slug, "name": a.Slug, "title": a.Title, "mimeType": "text/markdown"})
	}
	return map[string]any{"resources": out}, nil
}

// skippable: a list the caller may not read (scope, KB switched off) is
// simply left out of resources/list.
func skippable(err error) bool {
	var ae *apiError
	return errors.As(err, &ae) && (ae.Status == http.StatusForbidden || ae.Status == http.StatusNotFound)
}

func internalError(h *Handler, r *http.Request, err error) *rpcError {
	h.opt.Log.ErrorContext(r.Context(), "mcp: resources", "err", err)
	return newError(codeInternalError, "Internal error")
}

func (h *Handler) readResource(r *http.Request, c *caller, version string, m *message) (map[string]any, *rpcError) {
	var p struct {
		URI string `json:"uri"`
	}
	if e := decodeParams(m.Params, &p); e != nil {
		return nil, e
	}
	notFound := func() *rpcError {
		code := codeInvalidParams // 2026-07-28
		if version != Version20260728 {
			code = codeResourceNotFound
		}
		return &rpcError{Code: code, Message: "Resource not found", Data: map[string]any{"uri": p.URI}}
	}
	ctx := r.Context()
	switch {
	case strings.HasPrefix(p.URI, schemeTicket):
		ref := ticketArg(strings.TrimPrefix(p.URI, schemeTicket))
		_, isRef := ref.ref()
		if _, ok := ref.number(); !ok && !isRef {
			return nil, notFound()
		}
		t, err := c.getTicket(ctx, ref)
		if err != nil {
			if skippable(err) {
				return nil, notFound()
			}
			return nil, internalError(h, r, err)
		}
		b, _ := json.MarshalIndent(t, "", "  ")
		return contents(p.URI, "application/json", string(b)), nil
	case strings.HasPrefix(p.URI, schemeKB):
		slug, err := url.PathUnescape(strings.TrimPrefix(p.URI, schemeKB))
		if err != nil || slug == "" {
			return nil, notFound()
		}
		a, err := c.kbArticle(ctx, slug, "")
		if err != nil {
			var ae *apiError
			if errors.As(err, &ae) && ae.Status < 500 {
				return nil, notFound()
			}
			return nil, internalError(h, r, err)
		}
		title, _ := a["title"].(string)
		body, _ := a["body_md"].(string)
		return contents(p.URI, "text/markdown", "# "+title+"\n\n"+body), nil
	}
	return nil, notFound()
}

func contents(uri, mime, text string) map[string]any {
	return map[string]any{"contents": []map[string]any{{"uri": uri, "mimeType": mime, "text": text}}}
}
