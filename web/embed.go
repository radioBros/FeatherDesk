// SPDX-License-Identifier: AGPL-3.0-only

// Package web embeds the built SPA (web/dist) into the Go binary (D-03).
// The frontend build writes dist/; a placeholder index.html keeps `go build`
// working before the first frontend build.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Dist returns the built SPA rooted at dist/.
func Dist() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
