// SPDX-License-Identifier: AGPL-3.0-only

package parse

import (
	"net/mail"
	"strings"
)

// AutoReply reports loop/noise headers (NOT-029). reason is one of
// auto_submitted, precedence, x_autoreply, list_id, null_return_path.
// The caller decides whether list_id is allowed for a mailbox.
func AutoReply(h mail.Header) (reason string, auto bool) {
	if h == nil {
		return "", false
	}
	if v := strings.ToLower(strings.TrimSpace(h.Get("Auto-Submitted"))); v != "" {
		// "no" may carry parameters: "no; foo=bar".
		base, _, _ := strings.Cut(v, ";")
		if strings.TrimSpace(base) != "no" {
			return "auto_submitted", true
		}
	}
	switch strings.ToLower(strings.TrimSpace(h.Get("Precedence"))) {
	case "bulk", "list", "junk":
		return "precedence", true
	}
	if len(h["X-Autoreply"]) > 0 || len(h["X-Autorespond"]) > 0 || len(h["X-Autoresponder"]) > 0 {
		return "x_autoreply", true
	}
	if len(h["List-Id"]) > 0 {
		return "list_id", true
	}
	if rp, ok := h["Return-Path"]; ok && len(rp) > 0 {
		v := strings.TrimSpace(rp[0])
		if v == "<>" || v == "" || strings.ReplaceAll(v, " ", "") == "<>" {
			return "null_return_path", true
		}
	}
	return "", false
}
