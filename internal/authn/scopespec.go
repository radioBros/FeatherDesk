// SPDX-License-Identifier: AGPL-3.0-only

package authn

import (
	"bufio"
	"bytes"
	"errors"
	"strings"

	"github.com/radioBros/SimpleTickets/api"
)

// scopeIndex maps API operations to the PAT scopes declared in
// api/openapi.yaml (`x-scopes`). yaml.v3 is test-only (D-01), so the spec is
// read with a small indentation-based scanner; TestScopeIndex_MatchesSpec
// compares it with a full YAML parse.
type scopeIndex struct {
	ops []scopeOp
}

type scopeOp struct {
	method string
	path   string   // spec path, e.g. /agent/tickets/{id}
	segs   []string // "" = path parameter
	scopes []string // nil when the operation declares none
}

var httpMethods = map[string]string{"get": "GET", "put": "PUT", "post": "POST", "delete": "DELETE", "patch": "PATCH"}

func loadScopeIndex() (*scopeIndex, error) { return parseScopeIndex(api.Spec) }

func parseScopeIndex(spec []byte) (*scopeIndex, error) {
	idx := &scopeIndex{}
	sc := bufio.NewScanner(bytes.NewReader(spec))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	inPaths, inScopes := false, false
	var cur *scopeOp
	path := ""
	flush := func() {
		if cur != nil {
			idx.ops = append(idx.ops, *cur)
			cur = nil
		}
	}
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), " \t")
		if line == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		text := strings.TrimSpace(line)
		if indent == 0 {
			flush()
			inPaths = text == "paths:"
			continue
		}
		if !inPaths {
			continue
		}
		switch {
		case indent == 2 && strings.HasSuffix(text, ":"):
			flush()
			path = strings.Trim(strings.TrimSuffix(text, ":"), `"`)
			inScopes = false
		case indent == 4 && strings.HasSuffix(text, ":"):
			flush()
			inScopes = false
			if m, ok := httpMethods[strings.TrimSuffix(text, ":")]; ok {
				cur = &scopeOp{method: m, path: path, segs: splitTemplate(path)}
			}
		case cur != nil && indent == 6:
			inScopes = text == "x-scopes:"
			if inScopes && cur.scopes == nil {
				cur.scopes = []string{}
			}
		case cur != nil && inScopes && indent == 8 && strings.HasPrefix(text, "- "):
			cur.scopes = append(cur.scopes, strings.Trim(strings.TrimSpace(text[2:]), `"'`))
		case indent <= 6:
			inScopes = false
		}
	}
	flush()
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(idx.ops) == 0 {
		return nil, errors.New("openapi: no operations found for x-scopes")
	}
	return idx, nil
}

func splitTemplate(p string) []string {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	for i, s := range parts {
		if strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}") {
			parts[i] = ""
		}
	}
	return parts
}

// match finds the operation for method + path (relative to /api/v1). Literal
// segments win over parameters (/agent/tickets/counts over /agent/tickets/{id}).
func (x *scopeIndex) match(method, rel string) *scopeOp {
	segs := strings.Split(strings.Trim(rel, "/"), "/")
	var best *scopeOp
	bestParams := -1
	for i := range x.ops {
		op := &x.ops[i]
		if op.method != method || len(op.segs) != len(segs) {
			continue
		}
		params, ok := 0, true
		for j, s := range op.segs {
			if s == "" {
				if segs[j] == "" {
					ok = false
					break
				}
				params++
				continue
			}
			if s != segs[j] {
				ok = false
				break
			}
		}
		if ok && (best == nil || params < bestParams) {
			best, bestParams = op, params
		}
	}
	return best
}
