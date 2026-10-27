package evidence

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"quickanalyser/internal/config"
)

func TestCheckpointsEnableLateLineNavigation(t *testing.T) {
	dir := t.TempDir()
	w := &Writer{Dir: dir, Limits: config.DefaultLimits()}
	var b strings.Builder
	for i := 1; i <= 5000; i++ {
		b.WriteString("INFO line ")
		b.WriteString(strings.Repeat("x", 20))
		b.WriteByte('\n')
	}
	b.WriteString("2026-10-23 11:38:00 ERROR late-marker-line\n")
	cs, err := w.WriteMember("src1", "var/log/big.log", strings.NewReader(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	if len(cs.Checkpoints) < 4 {
		t.Fatalf("expected sparse checkpoints, got %d", len(cs.Checkpoints))
	}
	start := cs.LineCount
	lines, err := ReadLinesFrom(cs.CachePath, start, start, 64<<10, cs.Checkpoints)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || !strings.Contains(lines[0], "late-marker-line") {
		t.Fatalf("late line via checkpoint failed: %v (lineCount=%d cps=%d)", lines, cs.LineCount, len(cs.Checkpoints))
	}
	cp, ok := NearestCheckpoint(cs.Checkpoints, start)
	if !ok || cp.Line > start {
		t.Fatalf("nearest checkpoint invalid: %+v ok=%v", cp, ok)
	}
	// Ensure checkpoint file exists on disk for the job evidence path.
	if _, err := os.Stat(filepath.Join(dir, "src1.txt")); err != nil {
		t.Fatal(err)
	}
}
