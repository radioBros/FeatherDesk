// SPDX-License-Identifier: AGPL-3.0-only

// Package api embeds the OpenAPI 3.1 contract so the server can serve it at
// /api/v1/openapi.yaml and tests can validate against it.
package api

import _ "embed"

//go:embed openapi.yaml
var Spec []byte

// Docs is the API reference served at /api/docs, generated from Spec by
// `go test ./internal/apispec -run TestAPIDocsGenerated -update` (the OpenAPI
// libraries are test-only, D-01).
//
//go:embed docs.html
var Docs []byte

// BasePath is the server URL every non-root path in the spec is relative to.
const BasePath = "/api/v1"
