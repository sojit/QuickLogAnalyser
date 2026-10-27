package detection

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strings"

	"quickanalyser/internal/parsers"
)

var contLine = regexp.MustCompile(`(?i)^(Caused by:|\s+at\s+|Exception in thread|Traceback|\t|  )`)

const (
	// MaxFindingsPerSource is a soft in-memory ring for Scan()/ScanReader callers.
	// Streaming persistence via ScanEmit must not rely on this as a completeness claim.
	MaxFindingsPerSource   = 300
	MaxAssembledEventBytes = 512 << 10
)

// ScanResult reports findings plus explicit partial-coverage semantics.
type ScanResult struct {
	Findings         []Finding
	Partial          bool
	Reason           string
	BytesScanned     int64
	LinesScanned     int
	FindingsEmitted  int
}

func maybeInteresting(line string) bool {
	l := strings.ToLower(line)
	keys := []string{
		" error", "error ", "error:", "error=",
		" warn", "warning", "warn:",
		"critical", " fatal", "fatal:", "emergency",
		"outofmemory", "out of memory", "oom-kill", "oom_reaper",
		"segfault", "segmentation fault", "kernel panic", "oops:",
		"failed", "failure", "exception", "timed out", "timeout",
		"connection refused", "connection reset", "no route to host",
		"network is unreachable", "link is down", "link is up",
		"certificate has expired", "certificate verify", "ssl handshake", "tls handshake",
		"heartbeat", "deadlock", "status_marked_rollback", "no space left",
		"i/o error", "read-only file system",
	}
	for _, k := range keys {
		if strings.Contains(l, k) {
			return true
		}
	}
	return strings.HasPrefix(l, "error") || strings.HasPrefix(l, "warn") || strings.HasPrefix(l, "fatal")
}

// ScanReader streams a cached source. Limits produce Partial=true with a reason; nil error
// alone never means complete when findings were truncated in the in-memory ring.
func ScanReader(r io.Reader) ([]Finding, error) {
	res, err := Scan(r)
	if err != nil {
		return res.Findings, err
	}
	if res.Partial {
		return res.Findings, fmt.Errorf("detection partial: %s", res.Reason)
	}
	return res.Findings, nil
}

// FindingHandler receives each finding with original physical line numbers.
// Returning a non-nil error aborts the scan.
type FindingHandler func(Finding) error

// Scan performs a full stream over the reader with original physical-line numbering.
// It does not sample head/tail. An in-memory ring may mark Partial when more than
// MaxFindingsPerSource findings occur; ScanEmit delivers every occurrence.
func Scan(r io.Reader) (ScanResult, error) {
	var ring []Finding
	capped := false
	res, err := ScanEmit(r, func(f Finding) error {
		if len(ring) >= MaxFindingsPerSource {
			ring = ring[1:]
			capped = true
		}
		ring = append(ring, f)
		return nil
	})
	if capped {
		res.Partial = true
		res.Reason = joinReason(res.Reason, "findings_capped_keep_newest")
	}
	res.Findings = ring
	return res, err
}

// ScanEmit streams the entire retained source, invoking onFind for every finding
// with correct original physical line numbers. Repeated occurrences are preserved.
func ScanEmit(r io.Reader, onFind FindingHandler) (ScanResult, error) {
	if onFind == nil {
		onFind = func(Finding) error { return nil }
	}
	sc := bufio.NewScanner(r)
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 2<<20)

	var res ScanResult
	var (
		lineNo, start int
		building      bool
		sev           string
		rawTime       string
		msg           strings.Builder
		emitted       int
	)
	flush := func(end int) error {
		if !building {
			return nil
		}
		f := Evaluate(parsers.Event{
			Severity:  sev,
			RawTime:   rawTime,
			Message:   msg.String(),
			LineStart: start,
			LineEnd:   end,
		})
		building = false
		msg.Reset()
		if f == nil {
			return nil
		}
		emitted++
		return onFind(*f)
	}

	for sc.Scan() {
		lineNo++
		line := sc.Text()
		res.BytesScanned += int64(len(line) + 1)
		if building && contLine.MatchString(line) {
			if msg.Len() < MaxAssembledEventBytes {
				msg.WriteByte('\n')
				msg.WriteString(line)
			}
			continue
		}
		if building {
			if err := flush(lineNo - 1); err != nil {
				res.LinesScanned = lineNo
				res.FindingsEmitted = emitted
				res.Partial = true
				res.Reason = joinReason(res.Reason, err.Error())
				return res, err
			}
		}
		if !maybeInteresting(line) {
			continue
		}
		building = true
		start = lineNo
		sev = parsersSeverity(line)
		rawTime = parsers.ExtractTimestamp(line)
		msg.Reset()
		msg.WriteString(line)
	}
	if err := flush(lineNo); err != nil {
		res.LinesScanned = lineNo
		res.FindingsEmitted = emitted
		res.Partial = true
		res.Reason = joinReason(res.Reason, err.Error())
		return res, err
	}
	res.LinesScanned = lineNo
	res.FindingsEmitted = emitted
	if err := sc.Err(); err != nil {
		return res, err
	}
	return res, nil
}

func joinReason(a, b string) string {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	return a + "; " + b
}

var severityToken = regexp.MustCompile(`(?i)\b(EMERGENCY|ALERT|CRITICAL|CRIT|FATAL|ERROR|ERR|WARNING|WARN|NOTICE|INFO|DEBUG|TRACE)\b`)

func parsersSeverity(line string) string {
	m := severityToken.FindString(line)
	if m == "" {
		return "unknown"
	}
	switch strings.ToLower(m) {
	case "emergency":
		return "emergency"
	case "alert":
		return "alert"
	case "critical", "crit", "fatal":
		return "critical"
	case "error", "err":
		return "error"
	case "warning", "warn":
		return "warning"
	case "notice":
		return "notice"
	case "info":
		return "info"
	case "debug", "trace":
		return "debug"
	default:
		return "unknown"
	}
}
