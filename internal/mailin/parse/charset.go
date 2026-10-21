// SPDX-License-Identifier: AGPL-3.0-only

package parse

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// cp1252 maps bytes 0x80..0x9F of windows-1252; 0 means undefined (kept as U+FFFD).
var cp1252 = [32]rune{
	0x20AC, 0, 0x201A, 0x0192, 0x201E, 0x2026, 0x2020, 0x2021, 0x02C6, 0x2030, 0x0160, 0x2039, 0x0152, 0, 0x017D, 0,
	0, 0x2018, 0x2019, 0x201C, 0x201D, 0x2022, 0x2013, 0x2014, 0x02DC, 0x2122, 0x0161, 0x203A, 0x0153, 0, 0x017E, 0x0178,
}

// iso885915 lists the code points that differ from ISO-8859-1.
var iso885915 = map[byte]rune{
	0xA4: 0x20AC, 0xA6: 0x0160, 0xA8: 0x0161, 0xB4: 0x017D, 0xB8: 0x017E, 0xBC: 0x0152, 0xBD: 0x0153, 0xBE: 0x0178,
}

func normCharset(cs string) string {
	cs = strings.ToLower(strings.TrimSpace(cs))
	cs = strings.Trim(cs, `"'`)
	switch cs {
	case "", "utf-8", "utf8", "unicode-1-1-utf-8":
		return "utf-8"
	case "us-ascii", "ascii", "ansi_x3.4-1968", "iso646-us":
		return "us-ascii"
	case "iso-8859-1", "iso8859-1", "latin1", "latin-1", "l1", "iso_8859-1", "cp819":
		return "iso-8859-1"
	case "iso-8859-15", "iso8859-15", "latin-9", "latin9", "iso_8859-15", "l9":
		return "iso-8859-15"
	case "windows-1252", "cp1252", "x-cp1252", "win-1252":
		return "windows-1252"
	}
	return cs
}

// DecodeCharset decodes b from the given charset into UTF-8. Supported:
// utf-8, us-ascii, iso-8859-1, iso-8859-15, windows-1252. For anything else
// it returns the bytes as-is (invalid UTF-8 replaced) and ok=false.
func DecodeCharset(charset string, b []byte) (string, bool) {
	switch normCharset(charset) {
	case "utf-8", "us-ascii":
		if utf8.Valid(b) {
			return string(b), true
		}
		return strings.ToValidUTF8(string(b), "�"), true
	case "iso-8859-1":
		return decodeSingle(b, func(c byte) rune { return rune(c) }), true
	case "iso-8859-15":
		return decodeSingle(b, func(c byte) rune {
			if r, ok := iso885915[c]; ok {
				return r
			}
			return rune(c)
		}), true
	case "windows-1252":
		return decodeSingle(b, func(c byte) rune {
			if c >= 0x80 && c <= 0x9F {
				if r := cp1252[c-0x80]; r != 0 {
					return r
				}
				return utf8.RuneError
			}
			return rune(c)
		}), true
	}
	return strings.ToValidUTF8(string(b), "�"), false
}

func decodeSingle(b []byte, m func(byte) rune) string {
	var sb strings.Builder
	sb.Grow(len(b) + len(b)/4)
	for _, c := range b {
		if c < 0x80 {
			sb.WriteByte(c)
			continue
		}
		sb.WriteRune(m(c))
	}
	return sb.String()
}

// charsetReader is the mime.WordDecoder CharsetReader.
func charsetReader(charset string, input io.Reader) (io.Reader, error) {
	b, err := io.ReadAll(io.LimitReader(input, 1<<20))
	if err != nil {
		return nil, err
	}
	s, ok := DecodeCharset(charset, b)
	if !ok {
		return nil, fmt.Errorf("unsupported charset %q", charset)
	}
	return bytes.NewReader([]byte(s)), nil
}
