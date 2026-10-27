package parsers

import (
	"bufio"
	"io"
	"regexp"
	"strings"
	"unicode"
)

type Event struct {
	LineStart int
	LineEnd   int
	Severity  string
	Message   string
	RawTime   string
	Lines     []string
}

var (
	reSev  = regexp.MustCompile(`(?i)\b(EMERGENCY|ALERT|CRITICAL|CRIT|FATAL|ERROR|ERR|WARNING|WARN|NOTICE|INFO|DEBUG|TRACE)\b`)
	reCont = regexp.MustCompile(`(?i)^(Caused by:|\s+at\s+|Exception in thread|Traceback|---|\.\.\.)`)
)

// ParseText assembles multiline events from a decoded text source.
func ParseText(r io.Reader) ([]Event, error) {
	sc := bufio.NewScanner(r)
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 4<<20)
	var events []Event
	var cur *Event
	lineNo := 0
	flush := func() {
		if cur != nil {
			events = append(events, *cur)
			cur = nil
		}
	}
	for sc.Scan() {
		lineNo++
		line := sc.Text()
		isCont := cur != nil && (reCont.MatchString(line) || (len(line) > 0 && (line[0] == '\t' || (len(line) > 1 && line[0] == ' ' && line[1] == ' '))))
		if isCont {
			cur.LineEnd = lineNo
			cur.Lines = append(cur.Lines, line)
			cur.Message += "\n" + line
			continue
		}
		flush()
		sev := "unknown"
		if m := reSev.FindString(line); m != "" {
			sev = normalizeSeverity(m)
		}
		rawTime := ExtractTimestamp(line)
		cur = &Event{
			LineStart: lineNo,
			LineEnd:   lineNo,
			Severity:  sev,
			Message:   line,
			RawTime:   rawTime,
			Lines:     []string{line},
		}
	}
	flush()
	return events, sc.Err()
}

func normalizeSeverity(s string) string {
	switch strings.ToLower(s) {
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

// ExtractTimestamp returns the original timestamp text including fractional seconds and zone.
func ExtractTimestamp(line string) string {
	return timeparseExtract(line)
}

func LooksLikeLogPath(p string) bool {
	l := strings.ToLower(p)
	if strings.Contains(l, "/log/") || strings.Contains(l, `\log\`) ||
		strings.HasSuffix(l, ".log") || strings.Contains(l, "messages") ||
		strings.Contains(l, "syslog") || strings.Contains(l, "dmesg") ||
		strings.Contains(l, "journal") {
		return true
	}
	base := l
	if i := strings.LastIndexAny(l, `/\`); i >= 0 {
		base = l[i+1:]
	}
	for _, r := range base {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '.' && r != '-' && r != '_' {
			return false
		}
	}
	return strings.Contains(base, "log") || strings.HasPrefix(base, "messages")
}
