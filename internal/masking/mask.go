package masking

import (
	"regexp"
)

var (
	reKVEquals = regexp.MustCompile(`(?i)(password|passwd|pwd|secret|token|api[_-]?key|access[_-]?key|private[_-]?key)\s*[=:]\s*([^\s"'\\,}]+)`)
	reKVQuoted = regexp.MustCompile(`(?i)(password|passwd|pwd|secret|token|api[_-]?key)\s*:\s*"([^"]*)"`)
	reJSON     = regexp.MustCompile(`(?i)"(password|passwd|pwd|secret|token|api[_-]?key|access_token|refresh_token)"\s*:\s*"([^"]*)"`)
	reAuth     = regexp.MustCompile(`(?i)(authorization)\s*:\s*(bearer\s+)?([A-Za-z0-9\-._~+/]+=*)`)
	rePEM      = regexp.MustCompile(`(?i)(-----BEGIN [A-Z ]*PRIVATE KEY-----)([\s\S]*?)(-----END [A-Z ]*PRIVATE KEY-----)`)
)

// Line masks common secret forms while preserving line structure for display/export.
func Line(s string) string {
	out := s
	out = reJSON.ReplaceAllString(out, `"$1":"********"`)
	out = reKVQuoted.ReplaceAllString(out, `$1: "********"`)
	out = reKVEquals.ReplaceAllString(out, `$1=********`)
	out = reAuth.ReplaceAllStringFunc(out, func(m string) string {
		sub := reAuth.FindStringSubmatch(m)
		if len(sub) < 4 {
			return "Authorization: ********"
		}
		if sub[2] != "" {
			return sub[1] + ": " + sub[2] + "********"
		}
		return sub[1] + ": ********"
	})
	out = rePEM.ReplaceAllString(out, "$1\n********\n$3")
	return out
}
