package detection

import (
	"regexp"
	"strings"

	"quickanalyser/internal/parsers"
)

const RuleBundleVersion = "1.0.0"

type Finding struct {
	Severity   string
	Categories []string
	RuleIDs    []string
	Message    string
	LineStart  int
	LineEnd    int
	RawTime    string
	Confidence string
}

type rule struct {
	ID         string
	Category   string
	Severity   string
	Pattern    *regexp.Regexp
	Confidence string
}

var rules = []rule{
	{ID: "sev.header", Category: "severity", Severity: "", Pattern: regexp.MustCompile(`(?i)\b(ERROR|CRITICAL|FATAL|EMERGENCY|ALERT)\b`), Confidence: "medium"},
	{ID: "sev.warn", Category: "severity", Severity: "warning", Pattern: regexp.MustCompile(`(?i)\b(WARNING|WARN)\b`), Confidence: "medium"},
	{ID: "mem.oom", Category: "oom", Severity: "critical", Pattern: regexp.MustCompile(`(?i)OutOfMemoryError|out of memory|oom-kill|oom_reaper|Cannot allocate memory`), Confidence: "high"},
	{ID: "crash.segv", Category: "crash", Severity: "critical", Pattern: regexp.MustCompile(`(?i)segfault|segmentation fault|SIGSEGV|SIGABRT|core dumped|fatal assertion`), Confidence: "high"},
	{ID: "kernel.panic", Category: "kernel", Severity: "critical", Pattern: regexp.MustCompile(`(?i)Kernel panic|Oops:|BUG:|soft lockup|hard LOCKUP|hung task`), Confidence: "high"},
	{ID: "svc.fail", Category: "service", Severity: "error", Pattern: regexp.MustCompile(`(?i)Failed to start|unit failed|watchdog: BUG|Unexpected exit`), Confidence: "medium"},
	{ID: "db.rollback", Category: "database", Severity: "error", Pattern: regexp.MustCompile(`(?i)STATUS_MARKED_ROLLBACK|deadlock|transaction rollback|database .* (corrupt|corruption)`), Confidence: "high"},
	{ID: "disk.full", Category: "storage", Severity: "error", Pattern: regexp.MustCompile(`(?i)No space left on device|Read-only file system|I/O error|filesystem .*corrupt`), Confidence: "high"},
	{ID: "tls.cert", Category: "tls", Severity: "warning", Pattern: regexp.MustCompile(`(?i)certificate (has )?expired|hostname mismatch|SSL handshake|TLS handshake|certificate verify failed`), Confidence: "high"},
	{ID: "ha.peer", Category: "ha", Severity: "error", Pattern: regexp.MustCompile(`(?i)heartbeat (lost|fail)|split[- ]brain|failover (started|triggered)|peer (unreachable|down)`), Confidence: "medium"},
	{ID: "net.link_down", Category: "network", Severity: "warning", Pattern: regexp.MustCompile(`(?i)link (is )?down|NIC Link is Down|carrier lost|network cable (is )?unplugged|operstate.*(down|lowerlayerdown)`), Confidence: "high"},
	{ID: "net.link_up", Category: "network", Severity: "info", Pattern: regexp.MustCompile(`(?i)link (is )?up|NIC Link is Up|carrier restored|link recovery`), Confidence: "high"},
	{ID: "net.conn", Category: "network", Severity: "warning", Pattern: regexp.MustCompile(`(?i)connection (refused|reset|timed out|aborted)|broken pipe|No route to host|Network is unreachable`), Confidence: "medium"},
	{ID: "net.dns", Category: "network", Severity: "warning", Pattern: regexp.MustCompile(`(?i)name resolution|DNS (lookup|resolve) (fail|error|timeout)|NXDOMAIN|SERVFAIL`), Confidence: "medium"},
	{ID: "net.dhcp", Category: "network", Severity: "warning", Pattern: regexp.MustCompile(`(?i)DHCP.*(timeout|NAK|fail)|duplicate IP|address conflict`), Confidence: "medium"},
	{ID: "collect.fail", Category: "collection", Severity: "warning", Pattern: regexp.MustCompile(`(?i)command not found|Permission denied|No such file or directory.*collect|RPC.*(fail|error)`), Confidence: "low"},
}

var exclusions = []struct {
	ID      string
	Pattern *regexp.Regexp
}{
	{"ex.null_error_desc", regexp.MustCompile(`(?i)Error description\s*=\s*null`)},
	{"ex.libgpg", regexp.MustCompile(`(?i)libgpg-error`)},
	{"ex.crashkernel", regexp.MustCompile(`(?i)crashkernel=`)},
	{"ex.ecc_type", regexp.MustCompile(`(?i)Error Correction Type`)},
	{"ex.package_error_token", regexp.MustCompile(`(?i)^(?:Preparing|Installed|package).*error`)},
}

// Evaluate applies contextual rules to a parsed event.
func Evaluate(ev parsers.Event) *Finding {
	msg := ev.Message
	for _, ex := range exclusions {
		if ex.Pattern.MatchString(msg) {
			return nil
		}
	}
	// Successful update with Error description = null already covered.
	if strings.Contains(strings.ToLower(msg), "error description") && strings.Contains(strings.ToLower(msg), "null") {
		return nil
	}

	var cats []string
	var ids []string
	sev := ev.Severity
	conf := "low"
	matched := false
	for _, r := range rules {
		if !r.Pattern.MatchString(msg) {
			continue
		}
		matched = true
		ids = append(ids, r.ID)
		if r.Category != "severity" {
			cats = append(cats, r.Category)
		}
		if r.Severity != "" {
			sev = worse(sev, r.Severity)
		}
		if rank(r.Confidence) > rank(conf) {
			conf = r.Confidence
		}
	}
	if !matched {
		// Keep explicit severity-bearing lines that look like log events.
		if sev == "error" || sev == "critical" || sev == "alert" || sev == "emergency" || sev == "warning" {
			matched = true
			cats = append(cats, "severity")
			ids = append(ids, "sev.parsed")
			conf = "medium"
		}
	}
	if !matched {
		return nil
	}
	if len(cats) == 0 {
		cats = append(cats, "severity")
	}
	// Default suspicious view excludes pure INFO recoveries unless tagged network recovery for context storage.
	if sev == "info" || sev == "notice" || sev == "debug" || sev == "trace" {
		keep := false
		for _, c := range cats {
			if c == "network" {
				keep = true
				break
			}
		}
		if !keep {
			return nil
		}
	}
	return &Finding{
		Severity:   sev,
		Categories: unique(cats),
		RuleIDs:    unique(ids),
		Message:    truncate(msg, 4000),
		LineStart:  ev.LineStart,
		LineEnd:    ev.LineEnd,
		RawTime:    ev.RawTime,
		Confidence: conf,
	}
}

func worse(a, b string) string {
	if rankSev(b) > rankSev(a) {
		return b
	}
	if a == "" || a == "unknown" {
		return b
	}
	return a
}

func rankSev(s string) int {
	switch s {
	case "emergency":
		return 90
	case "alert":
		return 80
	case "critical":
		return 70
	case "error":
		return 60
	case "warning":
		return 50
	case "notice":
		return 40
	case "info":
		return 30
	case "debug", "trace":
		return 10
	default:
		return 0
	}
}

func rank(c string) int {
	switch c {
	case "high":
		return 3
	case "medium":
		return 2
	default:
		return 1
	}
}

func unique(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// Fingerprint builds a stable grouping key.
func Fingerprint(source, severity string, cats []string, msg string) string {
	norm := regexp.MustCompile(`[0-9a-fA-F]{8,}|[0-9]+`).ReplaceAllString(msg, "#")
	if len(norm) > 240 {
		norm = norm[:240]
	}
	return strings.Join([]string{source, severity, strings.Join(cats, "+"), strings.TrimSpace(norm)}, "|")
}
