// SPDX-License-Identifier: AGPL-3.0-only

package blob

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// AWS Signature Version 4, stdlib only (D-17). Validated against the official
// AWS test vectors in sigv4_test.go.

const (
	amzDateFormat = "20060102T150405Z"
	// EmptySHA256 is the payload hash of an empty body.
	EmptySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
)

type sigV4 struct {
	accessKey, secretKey, region, service string
}

// sign adds X-Amz-Date (if missing) and Authorization to req. payloadHash is the
// hex sha256 of the body (or "UNSIGNED-PAYLOAD"). Every header present on req
// (plus host) is signed.
func (s sigV4) sign(req *http.Request, payloadHash string, now time.Time) {
	amzDate := req.Header.Get("X-Amz-Date")
	if amzDate == "" {
		amzDate = now.UTC().Format(amzDateFormat)
		req.Header.Set("X-Amz-Date", amzDate)
	}
	creq, signed := s.canonicalRequest(req, payloadHash)
	scope := amzDate[:8] + "/" + s.region + "/" + s.service + "/aws4_request"
	sts := s.stringToSign(amzDate, scope, creq)
	sig := hex.EncodeToString(hmacSHA256(s.signingKey(amzDate[:8]), sts))
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+s.accessKey+"/"+scope+", SignedHeaders="+signed+", Signature="+sig)
}

func (s sigV4) canonicalRequest(req *http.Request, payloadHash string) (string, string) {
	host := req.Host
	if host == "" {
		host = req.URL.Host
	}
	headers := map[string]string{"host": host}
	for k, vs := range req.Header {
		lk := strings.ToLower(k)
		if lk == "authorization" {
			continue
		}
		vals := make([]string, len(vs))
		for i, v := range vs {
			vals[i] = strings.Join(strings.Fields(v), " ")
		}
		headers[lk] = strings.Join(vals, ",")
	}
	names := make([]string, 0, len(headers))
	for k := range headers {
		names = append(names, k)
	}
	sort.Strings(names)
	var ch strings.Builder
	for _, n := range names {
		ch.WriteString(n + ":" + headers[n] + "\n")
	}
	signed := strings.Join(names, ";")
	path := req.URL.EscapedPath()
	if path == "" {
		path = "/"
	}
	creq := strings.Join([]string{req.Method, canonicalPath(path), canonicalQuery(req.URL), ch.String(), signed, payloadHash}, "\n")
	return creq, signed
}

func (s sigV4) stringToSign(amzDate, scope, creq string) string {
	h := sha256.Sum256([]byte(creq))
	return "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + hex.EncodeToString(h[:])
}

func (s sigV4) signingKey(date string) []byte {
	k := hmacSHA256([]byte("AWS4"+s.secretKey), date)
	k = hmacSHA256(k, s.region)
	k = hmacSHA256(k, s.service)
	return hmacSHA256(k, "aws4_request")
}

func hmacSHA256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}

// canonicalPath re-encodes each segment with RFC 3986 unreserved characters
// left alone (S3 semantics: the path is encoded exactly once).
func canonicalPath(escaped string) string {
	segs := strings.Split(escaped, "/")
	for i, s := range segs {
		if u, err := url.PathUnescape(s); err == nil {
			s = u
		}
		segs[i] = uriEncode(s)
	}
	return strings.Join(segs, "/")
}

func canonicalQuery(u *url.URL) string {
	q := u.Query()
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		vs := append([]string(nil), q[k]...)
		sort.Strings(vs)
		for _, v := range vs {
			parts = append(parts, uriEncode(k)+"="+uriEncode(v))
		}
	}
	return strings.Join(parts, "&")
}

func uriEncode(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
		} else {
			b.WriteString("%" + strings.ToUpper(hex.EncodeToString([]byte{c})))
		}
	}
	return b.String()
}
