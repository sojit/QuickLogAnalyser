package diag

import (
	"fmt"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Envelope keys are reserved and set by the logger only.
var envelopeKeys = map[string]struct{}{
	"ts": {}, "level": {}, "code": {}, "app_version": {}, "instance_id": {}, "schema_version": {},
	"truncated": {}, "dropped_fields": {}, "event_bytes": {},
}

// allowFields are the only attribute keys accepted on diagnostic events.
var allowFields = map[string]struct{}{
	"job_id": {}, "source_id": {}, "result_generation": {}, "request_id": {}, "tab": {},
	"stage": {}, "reason_code": {}, "error_code": {}, "coverage_code": {},
	"files_done": {}, "sources_total": {}, "parse_done": {}, "events_committed": {},
	"elapsed_ms": {}, "duration_ms": {}, "bytes_done": {}, "queue_depth": {},
	"db_ms": {}, "worker_ms": {}, "partial": {}, "cancelled": {}, "stalled": {},
	"compatible": {}, "verified_sources": {}, "required_sources": {}, "verify_incomplete": {},
	"health": {}, "needs_setup": {}, "addr": {}, "n": {}, "count": {}, "percent": {},
	"url": {}, // sanitized to host/path without query
}

var denySubstrings = []string{
	"password", "passwd", "secret", "token", "session", "cookie", "authorization",
	"credential", "license", "apikey", "api_key", "private", "query", "search",
}

func allowKey(k string) bool {
	k = strings.ToLower(strings.TrimSpace(k))
	if k == "" {
		return false
	}
	if _, reserved := envelopeKeys[k]; reserved {
		return false // callers may not override envelope
	}
	_, ok := allowFields[k]
	return ok
}

func sanitizeCode(code string) string {
	code = strings.TrimSpace(code)
	if code == "" {
		return "event"
	}
	var b strings.Builder
	for _, r := range code {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
		if b.Len() >= 80 {
			break
		}
	}
	if b.Len() == 0 {
		return "event"
	}
	return b.String()
}

func sanitizeValue(key string, v any) any {
	return sanitizeAny(key, v, 0)
}

func sanitizeAny(key string, v any, depth int) any {
	if depth > 4 {
		return "[nested_truncated]"
	}
	switch x := v.(type) {
	case nil:
		return nil
	case bool, int, int32, int64, float64, float32:
		return x
	case string:
		return sanitizeString(key, x)
	case map[string]any:
		out := map[string]any{}
		for k, child := range x {
			lk := strings.ToLower(k)
			if keyLooksSecret(lk) {
				continue
			}
			if !allowKey(lk) && depth == 0 {
				continue
			}
			// Nested maps only keep allowlisted leaf-ish keys or sanitized scalars under known parents.
			if depth > 0 && keyLooksSecret(lk) {
				continue
			}
			if depth > 0 && !isSafeNestedKey(lk) {
				continue
			}
			out[sanitizeCode(lk)] = sanitizeAny(lk, child, depth+1)
		}
		return out
	case []any:
		if len(x) > 16 {
			x = x[:16]
		}
		out := make([]any, 0, len(x))
		for _, child := range x {
			out = append(out, sanitizeAny(key, child, depth+1))
		}
		return out
	default:
		return sanitizeString(key, fmt.Sprint(x))
	}
}

func isSafeNestedKey(k string) bool {
	switch k {
	case "code", "count", "ms", "n", "ok", "partial", "stage", "level":
		return true
	default:
		return false
	}
}

func keyLooksSecret(k string) bool {
	for _, d := range denySubstrings {
		if strings.Contains(k, d) {
			return true
		}
	}
	if strings.Contains(k, "path") || strings.Contains(k, "filename") || strings.Contains(k, "dir") {
		return true
	}
	return false
}

func sanitizeString(key string, s string) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "�")
	}
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	lower := strings.ToLower(s)
	if strings.Contains(lower, "password") || strings.Contains(lower, "secret") ||
		strings.Contains(lower, "token") || strings.Contains(lower, "bearer ") {
		return "[redacted]"
	}
	if key == "url" || strings.Contains(key, "url") {
		return redactURL(s)
	}
	if key == "reason_code" || key == "error_code" || key == "coverage_code" || key == "code" || key == "stage" {
		return sanitizeCode(s)
	}
	if looksLikePath(s) {
		return "[path_redacted]"
	}
	max := 256
	if key == "job_id" || key == "source_id" || key == "request_id" || key == "instance_id" {
		max = 64
	}
	if len(s) > max {
		// rune-safe truncate
		r := []rune(s)
		if len(r) > max {
			return string(r[:max]) + "…"
		}
	}
	return s
}

func looksLikePath(s string) bool {
	if strings.Contains(s, `:\`) || strings.Contains(s, `:/`) {
		return true
	}
	if strings.HasPrefix(s, "/") && strings.Count(s, "/") >= 2 {
		return true
	}
	if strings.HasPrefix(s, `\\`) {
		return true
	}
	return false
}

func redactURL(u string) string {
	u = strings.TrimSpace(u)
	if u == "" {
		return u
	}
	parsed, err := url.Parse(u)
	if err != nil || parsed.Scheme == "" {
		// Non-URL or opaque: strip raw userinfo pattern user:pass@ if present.
		if at := strings.Index(u, "@"); at > 0 {
			if scheme := strings.Index(u, "://"); scheme >= 0 && scheme < at {
				return u[:scheme+3] + "[userinfo_redacted]" + u[at:]
			}
		}
		if i := strings.Index(u, "?"); i >= 0 {
			return u[:i] + "?[redacted]"
		}
		return u
	}
	parsed.User = nil
	parsed.Fragment = ""
	q := parsed.Query()
	if len(q) > 0 {
		sensitive := []string{"s", "session", "token", "access_token", "refresh_token", "password", "passwd", "secret", "key", "api_key", "apikey", "auth", "authorization", "code"}
		for _, name := range sensitive {
			q.Del(name)
		}
		// Drop any remaining values that look like credentials; keep key names only as markers.
		for k := range q {
			lk := strings.ToLower(k)
			for _, s := range sensitive {
				if strings.Contains(lk, s) {
					q.Del(k)
					break
				}
			}
		}
		if len(q) == 0 {
			parsed.RawQuery = ""
		} else {
			parsed.RawQuery = q.Encode()
		}
	}
	out := parsed.String()
	// If original had userinfo, ensure it cannot remain.
	if strings.Contains(u, "@") && parsed.Host != "" {
		// parsed.String() already omits User when nil.
	}
	return out
}

// projectAttrs keeps only allowlisted sanitized attributes.
func projectAttrs(attrs map[string]any) (map[string]any, int) {
	out := map[string]any{}
	dropped := 0
	for k, v := range attrs {
		lk := strings.ToLower(strings.TrimSpace(k))
		if !allowKey(lk) {
			dropped++
			continue
		}
		out[lk] = sanitizeValue(lk, v)
	}
	return out, dropped
}
