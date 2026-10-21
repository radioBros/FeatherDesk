// SPDX-License-Identifier: AGPL-3.0-only

package parse

import (
	"regexp"
	"strings"

	"github.com/radioBros/SimpleTickets/internal/notify/thread"
)

// Attribution lines that introduce quoted history ("On … wrote:" and friends),
// matched on one line or on two joined lines (clients wrap long ones).
var attributionRes = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^\s*On\s.+\swrote:\s*$`),                       // en
	regexp.MustCompile(`(?i)^\s*Le\s.+\sa\s+[ée]crit\s*:\s*$`),             // fr
	regexp.MustCompile(`(?i)^\s*Il\s.+\sha\s+scritto\s*:\s*$`),             // it
	regexp.MustCompile(`(?i)^\s*Am\s.+\sschrieb\b.*:\s*$`),                 // de
	regexp.MustCompile(`(?i)^\s*El\s.+\sescribi[óo]\s*:\s*$`),              // es
	regexp.MustCompile(`(?i)^\s*\S+@\S+\s+(wrote|schrieb|scrive)\s*:\s*$`), // bare address
}

var originalRe = regexp.MustCompile(`(?i)^\s*-{2,}\s*(original message|forwarded message|messaggio originale|messaggio inoltrato|ursprüngliche nachricht|weitergeleitete nachricht|message d'origine|message transféré|mensaje original|mensaje reenviado)\s*-{2,}\s*$`)

// Outlook-style header blocks: a "From:" line followed within a few lines by
// a "Sent:"/"Date:" line, in en/fr/it/de/es.
var (
	outlookFromRe = regexp.MustCompile(`(?i)^\s*\**(from|de|da|von)\s*:\**\s*\S`)
	outlookSentRe = regexp.MustCompile(`(?i)^\s*\**(sent|date|envoyé|inviato|data|gesendet|datum|enviado|fecha)\s*:\**\s*\S`)
	underscoreRe  = regexp.MustCompile(`^\s*_{10,}\s*$`)
)

func isMarker(line string) bool {
	l := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "> "))
	for _, m := range thread.ReplyMarkers {
		if strings.Contains(l, m) {
			return true
		}
	}
	return false
}

func isAttribution(line string) bool {
	for _, re := range attributionRes {
		if re.MatchString(line) {
			return true
		}
	}
	return false
}

func isQuoted(line string) bool { return strings.HasPrefix(strings.TrimLeft(line, " \t"), ">") }

func isBlank(line string) bool { return strings.TrimSpace(line) == "" }

// StripQuotes cuts quoted history from a reply. Order: our localized
// "reply above this line" marker; otherwise the earliest client pattern
// (attribution line in en/fr/it/de/es, "-----Original Message-----"
// variants, Outlook From:/Sent: header blocks, signature delimiter "-- ");
// then a trailing ">"-quoted block. Text with no marker and no known pattern
// is returned whole. A cut that would leave nothing returns the original.
func StripQuotes(text string) (body string, stripped bool) {
	norm := strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(norm, "\n")
	cut := -1
	for i, l := range lines {
		if isMarker(l) {
			cut = i
			break
		}
	}
	if cut < 0 {
		cut = patternCut(lines)
	}
	if cut >= 0 {
		lines = trimAttributionTail(lines[:cut])
	}
	lines, tail := trimQuotedTail(lines)
	if cut < 0 && !tail {
		return strings.TrimRight(norm, " \t\n"), false
	}
	out := strings.TrimRight(strings.Join(lines, "\n"), " \t\n")
	if strings.TrimSpace(out) == "" {
		return strings.TrimRight(norm, " \t\n"), false
	}
	return out, true
}

// patternCut returns the index of the first line that starts quoted history.
func patternCut(lines []string) int {
	for i, l := range lines {
		switch {
		case isAttribution(l):
			return i
		case i+1 < len(lines) && !isBlank(l) && !isBlank(lines[i+1]) && isAttribution(strings.TrimSpace(l)+" "+strings.TrimSpace(lines[i+1])) && !isQuoted(l):
			return i
		case originalRe.MatchString(l):
			return i
		case outlookFromRe.MatchString(l) && outlookBlock(lines, i):
			if i > 0 && underscoreRe.MatchString(lines[i-1]) {
				return i - 1
			}
			if i > 1 && isBlank(lines[i-1]) && underscoreRe.MatchString(lines[i-2]) {
				return i - 2
			}
			return i
		case l == "-- " || l == "--":
			return i
		}
	}
	return -1
}

func outlookBlock(lines []string, i int) bool {
	for j := i + 1; j < len(lines) && j <= i+4; j++ {
		if outlookSentRe.MatchString(lines[j]) {
			return true
		}
	}
	return false
}

// trimQuotedTail removes a trailing block of ">"-quoted (and blank) lines and
// the attribution line right above it.
func trimQuotedTail(lines []string) ([]string, bool) {
	end := len(lines)
	for end > 0 && isBlank(lines[end-1]) {
		end--
	}
	if end == 0 || !isQuoted(lines[end-1]) {
		return lines, false
	}
	start := end
	for start > 0 && (isQuoted(lines[start-1]) || isBlank(lines[start-1])) {
		start--
	}
	// Drop an attribution right above (possibly wrapped over two lines).
	k := start
	for k > 0 && isBlank(lines[k-1]) {
		k--
	}
	if k > 0 && isAttribution(lines[k-1]) {
		start = k - 1
	} else if k > 1 && isAttribution(strings.TrimSpace(lines[k-2])+" "+strings.TrimSpace(lines[k-1])) {
		start = k - 2
	}
	return lines[:start], true
}

// trimAttributionTail drops trailing blank lines and a trailing attribution
// line (possibly wrapped over two lines) left above a cut point.
func trimAttributionTail(lines []string) []string {
	end := len(lines)
	for end > 0 && isBlank(lines[end-1]) {
		end--
	}
	if end > 0 && isAttribution(lines[end-1]) {
		end--
	} else if end > 1 && isAttribution(strings.TrimSpace(lines[end-2])+" "+strings.TrimSpace(lines[end-1])) {
		end -= 2
	}
	return lines[:end]
}
