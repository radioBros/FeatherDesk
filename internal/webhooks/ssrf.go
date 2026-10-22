// SPDX-License-Identifier: AGPL-3.0-only

package webhooks

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// Resolver resolves host names (net.DefaultResolver satisfies it; tests fake it).
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// Guard enforces the SSRF policy (NOT-012) on URLs at create time and on
// every dial (DNS rebinding): only https (http with AllowInsecure), and no
// loopback/private/link-local/ULA/CGNAT/unspecified/multicast targets unless
// AllowPrivate.
type Guard struct {
	AllowPrivate  bool
	AllowInsecure bool
	Resolver      Resolver
}

// NewGuard builds a guard; r nil = net.DefaultResolver.
func NewGuard(allowPrivate, allowInsecure bool, r Resolver) *Guard {
	if r == nil {
		r = net.DefaultResolver
	}
	return &Guard{AllowPrivate: allowPrivate, AllowInsecure: allowInsecure, Resolver: r}
}

// ErrBlocked is returned when a target address is not allowed.
var ErrBlocked = errors.New("webhooks: destination address is not allowed (private, loopback or link-local)")

var (
	cgnat     = netip.MustParsePrefix("100.64.0.0/10")
	zero8     = netip.MustParsePrefix("0.0.0.0/8")
	bench     = netip.MustParsePrefix("198.18.0.0/15")
	reserved  = netip.MustParsePrefix("240.0.0.0/4")
	nat64     = netip.MustParsePrefix("64:ff9b::/96")
	ietf6to4  = netip.MustParsePrefix("2002::/16")
	siteLocal = netip.MustParsePrefix("fec0::/10")
)

// Blocked reports whether a is a non-public address.
func Blocked(a netip.Addr) bool {
	if !a.IsValid() {
		return true
	}
	a = a.Unmap()
	if nat64.Contains(a) {
		// NAT64 embeds an IPv4 address in the last 32 bits.
		b := a.As16()
		return Blocked(netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]}))
	}
	if ietf6to4.Contains(a) {
		b := a.As16()
		return Blocked(netip.AddrFrom4([4]byte{b[2], b[3], b[4], b[5]}))
	}
	return a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() ||
		a.IsInterfaceLocalMulticast() || a.IsMulticast() || a.IsUnspecified() ||
		cgnat.Contains(a) || zero8.Contains(a) || bench.Contains(a) || reserved.Contains(a) || siteLocal.Contains(a) ||
		a == netip.AddrFrom4([4]byte{255, 255, 255, 255})
}

func (g *Guard) allowed(a netip.Addr) bool { return g.AllowPrivate || !Blocked(a) }

// CheckURL validates a webhook URL. code is a field error code
// (invalid_url, insecure_scheme, private_address, unresolvable) or "".
func (g *Guard) CheckURL(ctx context.Context, raw string) (code string, err error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Hostname() == "" || u.User != nil {
		return "invalid_url", fmt.Errorf("invalid URL")
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
	case "http":
		if !g.AllowInsecure {
			return "insecure_scheme", errors.New("only https URLs are allowed")
		}
	default:
		return "invalid_url", errors.New("only https URLs are allowed")
	}
	host := u.Hostname()
	if a, err := netip.ParseAddr(strings.Trim(host, "[]")); err == nil {
		if !g.allowed(a) {
			return "private_address", ErrBlocked
		}
		return "", nil
	}
	if g.AllowPrivate && (strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".localhost")) {
		return "", nil
	}
	lctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ips, err := g.Resolver.LookupNetIP(lctx, "ip", host)
	if err != nil || len(ips) == 0 {
		return "unresolvable", fmt.Errorf("cannot resolve %s", host)
	}
	for _, a := range ips {
		if !g.allowed(a) {
			return "private_address", ErrBlocked
		}
	}
	return "", nil
}

// DialContext resolves through the guard's resolver, refuses blocked
// addresses, and dials with a Control hook that re-checks the final IP.
func (g *Guard) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	var ips []netip.Addr
	if a, err := netip.ParseAddr(host); err == nil {
		ips = []netip.Addr{a}
	} else {
		ips, err = g.Resolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
	}
	d := &net.Dialer{Timeout: 10 * time.Second, Control: g.control}
	var lastErr = ErrBlocked
	for _, a := range ips {
		if !g.allowed(a) {
			lastErr = fmt.Errorf("%w: %s", ErrBlocked, a)
			continue
		}
		c, err := d.DialContext(ctx, network, net.JoinHostPort(a.Unmap().String(), port))
		if err == nil {
			return c, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// control runs after resolution, on the exact address being connected.
func (g *Guard) control(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	a, err := netip.ParseAddr(host)
	if err != nil {
		return err
	}
	if !g.allowed(a) {
		return fmt.Errorf("%w: %s", ErrBlocked, a)
	}
	return nil
}

// Client returns an HTTP client that dials through the guard, follows no
// redirects and uses no proxy.
func (g *Guard) Client(timeout time.Duration) *http.Client {
	tr := &http.Transport{
		Proxy:                 nil,
		DialContext:           g.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          20,
		MaxIdleConnsPerHost:   2,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: timeout,
	}
	return &http.Client{
		Transport:     tr,
		Timeout:       timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}
