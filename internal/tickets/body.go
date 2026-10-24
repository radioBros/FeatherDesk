// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"unicode/utf8"

	"github.com/radioBros/FeatherDesk/internal/mailin/parse"
	"github.com/radioBros/FeatherDesk/internal/mdrender"
	"github.com/radioBros/FeatherDesk/internal/problems"
)

// MaxHTMLBytes is the raw HTML input cap (4× the Markdown body limit).
const MaxHTMLBytes = MaxBodyBytes * 4

// resolveBody accepts exactly one of body_md or body_html and returns Markdown for storage.
func resolveBody(bodyMD, bodyHTML string) (string, error) {
	hasMD := bodyMD != ""
	hasHTML := bodyHTML != ""
	switch {
	case hasMD && hasHTML:
		return "", problems.Validation(problems.Field("body_html", "not_allowed", "send body_md or body_html, not both"))
	case !hasMD && !hasHTML:
		return "", problems.Validation(problems.Field("body_md", "required", "must not be empty"))
	case hasHTML:
		if len(bodyHTML) > MaxHTMLBytes || !utf8.ValidString(bodyHTML) {
			return "", problems.Validation(problems.Field("body_html", "max_length", "at most 256 KiB of UTF-8 text"))
		}
		md := parse.HTMLToMarkdown(bodyHTML)
		return validBody("body_html", md)
	default:
		return validBody("body_md", bodyMD)
	}
}

func messageBodyHTML(md string) string {
	if md == "" {
		return ""
	}
	return mdrender.Render(md)
}

func enrichMessageDTO(m *messageDTO) {
	m.BodyHTML = messageBodyHTML(m.BodyMD)
}

func enrichCustomerMessageDTO(m *customerMessageDTO) {
	m.BodyHTML = messageBodyHTML(m.BodyMD)
}
