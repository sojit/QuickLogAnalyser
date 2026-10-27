package timeparse

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	reISO = regexp.MustCompile(`(?i)^(\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|[+-]\d{2}:?\d{2})?)`)
	reSys = regexp.MustCompile(`(?i)^((?:Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)\s+\d{1,2}\s+\d{2}:\d{2}:\d{2})`)
)

// Stamp is a lossless parse of an observed log timestamp.
type Stamp struct {
	Raw          string
	UTC          *time.Time // nil when not confidently convertible
	Precision    string     // second|fraction|unknown
	ZoneKnown    bool
	YearKnown    bool
	Confidence   string // high|medium|low
	InferredYear bool
}

// ExtractRaw returns the original timestamp text including fractions and zone when present.
func ExtractRaw(line string) string {
	if m := reISO.FindString(line); m != "" {
		return m
	}
	if m := reSys.FindString(line); m != "" {
		return m
	}
	return ""
}

// Parse interprets raw timestamp text without inventing a host timezone.
func Parse(raw string, refYear int) Stamp {
	s := Stamp{Raw: raw, Precision: "unknown", Confidence: "low"}
	if raw == "" {
		return s
	}
	if m := reISO.FindString(raw); m != "" {
		s.Raw = m
		s.YearKnown = true
		s.Precision = "second"
		if strings.Contains(m, ".") {
			s.Precision = "fraction"
		}
		norm := m
		if strings.Contains(norm, " ") && !strings.Contains(norm, "T") {
			norm = strings.Replace(norm, " ", "T", 1)
		}
		// Go needs colon in numeric zone: +0400 → +04:00 when length matches
		if zoneFix := fixZone(norm); zoneFix != "" {
			norm = zoneFix
		}
		layouts := []string{
			time.RFC3339Nano,
			time.RFC3339,
			"2006-01-02T15:04:05.999999999Z07:00",
			"2006-01-02T15:04:05Z07:00",
			"2006-01-02T15:04:05.999999999",
			"2006-01-02T15:04:05",
		}
		for _, layout := range layouts {
			if t, err := time.Parse(layout, norm); err == nil {
				utc := t.UTC()
				s.UTC = &utc
				s.ZoneKnown = strings.ContainsAny(m, "Z+-") && (strings.Contains(m, "Z") || strings.Contains(m, "+") || strings.Count(m, "-") > 2)
				s.Confidence = "high"
				if !s.ZoneKnown {
					s.Confidence = "medium"
				}
				return s
			}
		}
		s.Confidence = "medium"
		return s
	}
	if m := reSys.FindString(raw); m != "" {
		s.Raw = m
		s.YearKnown = false
		s.ZoneKnown = false
		s.Precision = "second"
		year := refYear
		if year <= 0 {
			year = time.Now().UTC().Year()
			s.InferredYear = true
		} else {
			s.InferredYear = true
		}
		withYear := m + " " + strconv.Itoa(year)
		if t, err := time.Parse("Jan 2 15:04:05 2006", withYear); err == nil {
			utc := t.UTC()
			s.UTC = &utc
			s.Confidence = "low"
		} else if t, err := time.Parse("Jan _2 15:04:05 2006", withYear); err == nil {
			utc := t.UTC()
			s.UTC = &utc
			s.Confidence = "low"
		}
		return s
	}
	return s
}

func fixZone(s string) string {
	// +0400 / -0400 at end → +04:00
	if len(s) >= 5 {
		tail := s[len(s)-5:]
		if (tail[0] == '+' || tail[0] == '-') && isDigits(tail[1:]) {
			return s[:len(s)-5] + tail[:3] + ":" + tail[3:]
		}
	}
	return s
}

func isDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return len(s) > 0
}
