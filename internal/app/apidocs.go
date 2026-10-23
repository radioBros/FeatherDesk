// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"net/http"

	"github.com/radioBros/FeatherDesk/api"
)

// GET /api/docs (getApiDocs, CON-011, CON-018): the API reference, generated
// from api/openapi.yaml at test time (internal/apispec BuildDocs; the OpenAPI
// libraries are test-only, D-01) and embedded as api/docs.html. Every
// operation with parameters, request and response examples (validated against
// the schemas by TestAPIDocsExamplesValid) and curl. No script and no external
// asset, so it works offline and under a strict CSP; the machine-readable
// document stays at /api/v1/openapi.yaml.
func serveAPIDocs(w http.ResponseWriter, _ *http.Request) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	_, _ = w.Write(api.Docs)
}
