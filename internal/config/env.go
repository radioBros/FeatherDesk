// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Env reads module-owned variables (OIDC, SMTP, IMAP, …) and collects errors so
// a module reports all of its problems at once:
//
//	e := config.NewEnv(nil)
//	host := e.String("FD_SMTP_HOST", "")
//	port := e.Int("FD_SMTP_PORT", 587)
//	if err := e.Err(); err != nil { … }
type Env struct {
	get  func(string) string
	errs []error
}

func NewEnv(getenv func(string) string) *Env {
	if getenv == nil {
		getenv = os.Getenv
	}
	return &Env{get: getenv}
}

// Lookup returns the trimmed raw value and whether it was set (non-empty).
func (e *Env) Lookup(name string) (string, bool) {
	v := strings.TrimSpace(e.get(name))
	return v, v != ""
}

func (e *Env) String(name, def string) string {
	if v, ok := e.Lookup(name); ok {
		return v
	}
	return def
}

// Required records an error when the variable is empty.
func (e *Env) Required(name string) string {
	v, ok := e.Lookup(name)
	if !ok {
		e.errs = append(e.errs, fmt.Errorf("%s: required", name))
	}
	return v
}

func (e *Env) Int(name string, def int) int {
	v, ok := e.Lookup(name)
	if !ok {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		e.errs = append(e.errs, fmt.Errorf("%s: want an integer, got %q", name, v))
		return def
	}
	return n
}

func (e *Env) Bool(name string, def bool) bool {
	v, ok := e.Lookup(name)
	if !ok {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		e.errs = append(e.errs, fmt.Errorf("%s: want true/false, got %q", name, v))
		return def
	}
	return b
}

func (e *Env) Duration(name string, def time.Duration) time.Duration {
	v, ok := e.Lookup(name)
	if !ok {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		e.errs = append(e.errs, fmt.Errorf("%s: want a duration like 10s, got %q", name, v))
		return def
	}
	return d
}

// List splits a comma-separated value, dropping empty items.
func (e *Env) List(name string) []string {
	v, ok := e.Lookup(name)
	if !ok {
		return nil
	}
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Map parses `k=v,k2=v2` (e.g. FD_OIDC_STAFF_ROLE_MAP).
func (e *Env) Map(name string) map[string]string {
	out := map[string]string{}
	for _, item := range e.List(name) {
		k, v, ok := strings.Cut(item, "=")
		if !ok || strings.TrimSpace(k) == "" {
			e.errs = append(e.errs, fmt.Errorf("%s: %q is not key=value", name, item))
			continue
		}
		out[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return out
}

// Fail records a custom validation error.
func (e *Env) Fail(format string, args ...any) { e.errs = append(e.errs, fmt.Errorf(format, args...)) }

// Err returns every recorded problem as one LoadError, or nil.
func (e *Env) Err() error {
	if len(e.errs) == 0 {
		return nil
	}
	return &LoadError{Errs: e.errs}
}
