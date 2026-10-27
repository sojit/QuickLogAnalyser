package detection

import (
	"strings"
	"testing"

	"quickanalyser/internal/parsers"
)

func TestExclusions(t *testing.T) {
	cases := []string{
		"Error description = null during successful update",
		"Preparing package libgpg-error",
		"boot crashkernel=128M",
		"Error Correction Type: Single-bit ECC",
	}
	for _, msg := range cases {
		f := Evaluate(parsers.Event{Message: msg, Severity: "error", LineStart: 1, LineEnd: 1})
		if f != nil {
			t.Fatalf("expected exclusion for %q, got %#v", msg, f)
		}
	}
}

func TestOOMAndNetwork(t *testing.T) {
	oom := Evaluate(parsers.Event{Message: "java.lang.OutOfMemoryError: Java heap space", Severity: "error", LineStart: 1, LineEnd: 1})
	if oom == nil || !contains(oom.Categories, "oom") {
		t.Fatalf("expected oom finding, got %#v", oom)
	}
	down := Evaluate(parsers.Event{Message: "eth0: Link is Down", Severity: "warning", LineStart: 2, LineEnd: 2})
	if down == nil || !contains(down.Categories, "network") {
		t.Fatalf("expected network finding, got %#v", down)
	}
}

func TestMultilineFingerprintStable(t *testing.T) {
	a := Fingerprint("a.log", "error", []string{"database"}, "failed id=12345")
	b := Fingerprint("a.log", "error", []string{"database"}, "failed id=99999")
	if a != b {
		t.Fatalf("expected normalized fingerprints to match: %s vs %s", a, b)
	}
}

func TestParseMultilineStack(t *testing.T) {
	r := strings.NewReader("ERROR boom\n  at com.example.Main.run(Main.java:42)\nINFO next\n")
	evs, err := parsers.ParseText(r)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) < 2 {
		t.Fatalf("expected >=2 events, got %d", len(evs))
	}
	if evs[0].LineStart != 1 || evs[0].LineEnd != 2 {
		t.Fatalf("stack should be one event lines 1-2, got %d-%d", evs[0].LineStart, evs[0].LineEnd)
	}
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
