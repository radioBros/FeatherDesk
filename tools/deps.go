// SPDX-License-Identifier: AGPL-3.0-only

//go:build tools

// Package tools pins every allowed module in go.mod so parallel work never
// edits go.mod. Runtime dependencies are capped at 15 (CONVENTIONS §10, D-18);
// libopenapi-validator and yaml.v3 are test-only.
package tools

import (
	_ "github.com/coder/websocket"
	_ "github.com/coreos/go-oidc/v3/oidc"
	_ "github.com/emersion/go-imap/v2/imapclient"
	_ "github.com/go-chi/chi/v5"
	_ "github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "github.com/microcosm-cc/bluemonday"
	_ "github.com/pb33f/libopenapi-validator"
	_ "github.com/wneessen/go-mail"
	_ "github.com/yuin/goldmark"
	_ "golang.org/x/crypto/argon2"
	_ "golang.org/x/oauth2"
	_ "gopkg.in/yaml.v3"
	_ "modernc.org/sqlite"
)
