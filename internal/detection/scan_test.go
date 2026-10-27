package detection

import (
	"strings"
	"testing"
)

func TestScanReaderSkipsNoiseFindsOOM(t *testing.T) {
	in := strings.NewReader(strings.Repeat("INFO heartbeat ok\n", 1000) +
		"ERROR OutOfMemoryError: Java heap space\n  at com.example.Main.run(Main.java:1)\n" +
		"INFO recovered\n")
	fs, err := ScanReader(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) == 0 {
		t.Fatal("expected findings")
	}
	found := false
	for _, f := range fs {
		for _, c := range f.Categories {
			if c == "oom" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("expected oom category in %#v", fs)
	}
}
