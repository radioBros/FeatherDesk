// SPDX-License-Identifier: AGPL-3.0-only

package blob

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Vectors from the AWS Signature Version 4 test suite (aws-sig-v4-test-suite:
// get-vanilla, post-vanilla, get-vanilla-query-order-key-case) and the S3
// developer guide example "GET Object" (examplebucket, Range: bytes=0-9).
func TestSigV4_AWSTestSuite(t *testing.T) {
	suite := sigV4{accessKey: "AKIDEXAMPLE", secretKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY", region: "us-east-1", service: "service"}
	cases := []struct {
		name, method, url string
		creqHash, sig     string
	}{
		{"get-vanilla", "GET", "https://example.amazonaws.com/",
			"bb579772317eb040ac9ed261061d46c1f17a8133879d6129b6e1c25292927e63",
			"5fa00fa31553b73ebf1942676e86291e8372ff2a2260956d9b8aae1d763fbf31"},
		{"post-vanilla", "POST", "https://example.amazonaws.com/",
			"553f88c9e4d10fc9e109e2aeb65f030801b70c2f6468faca261d401ae622fc87",
			"5da7c1a2acd57cee7505fc6676e4e544621c30862966e37dddb68e92efbe5d6b"},
		{"get-vanilla-query-order-key-case", "GET", "https://example.amazonaws.com/?Param2=value2&Param1=value1",
			"816cd5b414d056048ba4f7c5386d6e0533120fb1fcfa93762cf0fc39e2cf19e0",
			"b97d918cfa904a5beff61c982a1b6f458b799221646efd99d3219ec94cdf2500"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req, _ := http.NewRequest(c.method, c.url, nil)
			req.Header.Set("X-Amz-Date", "20150830T123600Z")
			creq, signed := suite.canonicalRequest(req, EmptySHA256)
			h := sha256.Sum256([]byte(creq))
			if got := hex.EncodeToString(h[:]); got != c.creqHash {
				t.Fatalf("canonical request hash %s, want %s\n%s", got, c.creqHash, creq)
			}
			if signed != "host;x-amz-date" {
				t.Fatalf("signed headers %q", signed)
			}
			suite.sign(req, EmptySHA256, time.Time{})
			auth := req.Header.Get("Authorization")
			want := "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/service/aws4_request, SignedHeaders=host;x-amz-date, Signature=" + c.sig
			if auth != want {
				t.Fatalf("authorization\n got %s\nwant %s", auth, want)
			}
		})
	}
}

func TestSigV4_S3GetObjectExample(t *testing.T) {
	s := sigV4{accessKey: "AKIAIOSFODNN7EXAMPLE", secretKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", region: "us-east-1", service: "s3"}
	req, _ := http.NewRequest("GET", "https://examplebucket.s3.amazonaws.com/test.txt", nil)
	req.Header.Set("Range", "bytes=0-9")
	req.Header.Set("X-Amz-Content-Sha256", EmptySHA256)
	req.Header.Set("X-Amz-Date", "20130524T000000Z")
	s.sign(req, EmptySHA256, time.Time{})
	auth := req.Header.Get("Authorization")
	if !strings.HasSuffix(auth, "SignedHeaders=host;range;x-amz-content-sha256;x-amz-date, Signature=f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41") {
		t.Fatalf("authorization %s", auth)
	}
}

func TestURIEncode(t *testing.T) {
	if got := canonicalPath("/my%20folder/a+b.txt"); got != "/my%20folder/a%2Bb.txt" {
		t.Fatal(got)
	}
}
