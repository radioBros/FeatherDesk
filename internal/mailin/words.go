// SPDX-License-Identifier: AGPL-3.0-only

package mailin

import (
	"io"
	"mime"
	"strings"

	"github.com/radioBros/FeatherDesk/internal/mailin/parse"
)

// mailWordDecoder decodes RFC 2047 encoded words with the parser's charsets.
type mailWordDecoder struct{}

func (mailWordDecoder) decode(s string) string {
	d := mime.WordDecoder{CharsetReader: func(charset string, in io.Reader) (io.Reader, error) {
		b, err := io.ReadAll(in)
		if err != nil {
			return nil, err
		}
		out, _ := parse.DecodeCharset(charset, b)
		return strings.NewReader(out), nil
	}}
	if out, err := d.DecodeHeader(s); err == nil {
		return out
	}
	return s
}
