// SPDX-License-Identifier: AGPL-3.0-only

package blob

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// S3 is an S3-compatible backend (AWS S3, MinIO, R2, B2, Garage) using a
// hand-written SigV4 client (D-17): single-part PUT, ranged GET, HEAD, DELETE.
type S3 struct {
	Endpoint  *url.URL // scheme + host (+ optional port)
	Bucket    string
	Prefix    string // key prefix inside the bucket, without leading/trailing "/"
	PathStyle bool   // https://endpoint/bucket/key instead of https://bucket.endpoint/key
	Presign   bool   // opt-in presigned redirects (handlers decide; see PresignGet)
	signer    sigV4
	Client    *http.Client
	now       func() time.Time
}

// NewS3 builds an S3 backend. endpoint defaults to https://s3.<region>.amazonaws.com.
func NewS3(bucket, prefix, endpoint, region, accessKey, secretKey string, pathStyle bool) (*S3, error) {
	if bucket == "" {
		return nil, errors.New("blob: s3 bucket is required")
	}
	if region == "" {
		region = "us-east-1"
	}
	if endpoint == "" {
		endpoint = "https://s3." + region + ".amazonaws.com"
	}
	ep, err := url.Parse(endpoint)
	if err != nil || ep.Host == "" {
		return nil, fmt.Errorf("blob: bad s3 endpoint %q", endpoint)
	}
	if accessKey == "" || secretKey == "" {
		return nil, errors.New("blob: FD_S3_ACCESS_KEY and FD_S3_SECRET_KEY are required for s3://")
	}
	return &S3{
		Endpoint: ep, Bucket: bucket, Prefix: strings.Trim(prefix, "/"), PathStyle: pathStyle,
		signer: sigV4{accessKey: accessKey, secretKey: secretKey, region: region, service: "s3"},
		Client: &http.Client{Timeout: 5 * time.Minute},
		now:    time.Now,
	}, nil
}

func (s *S3) Backend() string { return "s3" }

func (s *S3) objectURL(key string) (*url.URL, string) {
	full := key
	if s.Prefix != "" {
		full = s.Prefix + "/" + key
	}
	u := *s.Endpoint
	host := u.Host
	if s.PathStyle {
		u.Path = "/" + s.Bucket + "/" + full
	} else {
		host = s.Bucket + "." + u.Host
		u.Host = host
		u.Path = "/" + full
	}
	u.RawPath = ""
	return &u, host
}

func (s *S3) do(ctx context.Context, method, key string, body io.Reader, size int64, payloadHash string, hdr http.Header) (*http.Response, error) {
	if err := ValidKey(key); err != nil {
		return nil, err
	}
	u, host := s.objectURL(key)
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, err
	}
	req.Host = host
	if size >= 0 && body != nil {
		req.ContentLength = size
	}
	for k, vs := range hdr {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	s.signer.sign(req, payloadHash, s.now())
	return s.Client.Do(req)
}

// Put spools the body to a temp file to learn its size and sha256 (SigV4 signs
// the payload hash), then uploads it in one request.
func (s *S3) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) (string, error) {
	tmp, err := os.CreateTemp("", "s3put-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	h := sha256.New()
	src := io.Reader(ctxReader{ctx, r})
	if size >= 0 {
		src = io.LimitReader(src, size+1)
	}
	n, err := io.Copy(io.MultiWriter(tmp, h), src)
	if err != nil {
		return "", err
	}
	if size >= 0 && n != size {
		return "", fmt.Errorf("blob: short read: %d of %d bytes", n, size)
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	sum := hex.EncodeToString(h.Sum(nil))
	hdr := http.Header{}
	if contentType != "" {
		hdr.Set("Content-Type", contentType)
	}
	resp, err := s.do(ctx, http.MethodPut, key, tmp, n, sum, hdr)
	if err != nil {
		return "", err
	}
	defer drain(resp)
	if resp.StatusCode/100 != 2 {
		return "", s3Error(resp)
	}
	return sum, nil
}

func (s *S3) Stat(ctx context.Context, key string) (Info, error) {
	resp, err := s.do(ctx, http.MethodHead, key, nil, -1, EmptySHA256, nil)
	if err != nil {
		return Info{}, err
	}
	defer drain(resp)
	if resp.StatusCode == http.StatusNotFound {
		return Info{}, ErrNotFound
	}
	if resp.StatusCode/100 != 2 {
		return Info{}, s3Error(resp)
	}
	mod, _ := http.ParseTime(resp.Header.Get("Last-Modified"))
	return Info{Key: key, Size: resp.ContentLength, ContentType: resp.Header.Get("Content-Type"), ModTime: mod}, nil
}

// Open returns a lazy reader: HEAD for the size, then a ranged GET from the
// current offset on the first Read after each Seek. Nothing is buffered in
// memory beyond the HTTP stream, so http.ServeContent range requests work.
func (s *S3) Open(ctx context.Context, key string) (io.ReadSeekCloser, Info, error) {
	info, err := s.Stat(ctx, key)
	if err != nil {
		return nil, Info{}, err
	}
	return &s3Reader{s: s, ctx: ctx, key: key, size: info.Size}, info, nil
}

func (s *S3) Delete(ctx context.Context, key string) error {
	resp, err := s.do(ctx, http.MethodDelete, key, nil, -1, EmptySHA256, nil)
	if err != nil {
		return err
	}
	defer drain(resp)
	if resp.StatusCode/100 != 2 && resp.StatusCode != http.StatusNotFound {
		return s3Error(resp)
	}
	return nil
}

// PresignGet returns a time-limited GET URL (query-string SigV4). Only used when
// the operator opts in with presign=true; otherwise downloads stream through
// the app so visibility checks stay in one place.
func (s *S3) PresignGet(key string, ttl time.Duration) (string, error) {
	if err := ValidKey(key); err != nil {
		return "", err
	}
	u, host := s.objectURL(key)
	now := s.now().UTC()
	amzDate := now.Format(amzDateFormat)
	scope := amzDate[:8] + "/" + s.signer.region + "/s3/aws4_request"
	q := url.Values{}
	q.Set("X-Amz-Algorithm", "AWS4-HMAC-SHA256")
	q.Set("X-Amz-Credential", s.signer.accessKey+"/"+scope)
	q.Set("X-Amz-Date", amzDate)
	q.Set("X-Amz-Expires", strconv.Itoa(int(ttl.Seconds())))
	q.Set("X-Amz-SignedHeaders", "host")
	u.RawQuery = q.Encode()
	creq := strings.Join([]string{"GET", canonicalPath(u.EscapedPath()), canonicalQuery(u), "host:" + host + "\n", "host", "UNSIGNED-PAYLOAD"}, "\n")
	sig := hex.EncodeToString(hmacSHA256(s.signer.signingKey(amzDate[:8]), s.signer.stringToSign(amzDate, scope, creq)))
	return u.String() + "&X-Amz-Signature=" + sig, nil
}

type s3Reader struct {
	s      *S3
	ctx    context.Context
	key    string
	size   int64
	offset int64
	body   io.ReadCloser
}

func (r *s3Reader) Read(p []byte) (int, error) {
	if r.offset >= r.size {
		return 0, io.EOF
	}
	if r.body == nil {
		hdr := http.Header{}
		hdr.Set("Range", "bytes="+strconv.FormatInt(r.offset, 10)+"-")
		resp, err := r.s.do(r.ctx, http.MethodGet, r.key, nil, -1, EmptySHA256, hdr)
		if err != nil {
			return 0, err
		}
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
			defer drain(resp)
			return 0, s3Error(resp)
		}
		r.body = resp.Body
	}
	n, err := r.body.Read(p)
	r.offset += int64(n)
	return n, err
}

func (r *s3Reader) Seek(offset int64, whence int) (int64, error) {
	var abs int64
	switch whence {
	case io.SeekStart:
		abs = offset
	case io.SeekCurrent:
		abs = r.offset + offset
	case io.SeekEnd:
		abs = r.size + offset
	}
	if abs < 0 {
		return 0, errors.New("blob: negative seek")
	}
	if abs != r.offset && r.body != nil {
		r.body.Close()
		r.body = nil
	}
	r.offset = abs
	return abs, nil
}

func (r *s3Reader) Close() error {
	if r.body != nil {
		return r.body.Close()
	}
	return nil
}

func drain(resp *http.Response) {
	// Best effort: draining only lets the connection be reused.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
}

func s3Error(resp *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	return fmt.Errorf("blob: s3 %s: %s", resp.Status, strings.TrimSpace(string(b)))
}
