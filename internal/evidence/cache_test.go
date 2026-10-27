package evidence

import (
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"

	"quickanalyser/internal/config"
)

func TestDecodeGzipLogAndWrite(t *testing.T) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write([]byte("line one\nline two keyword\n"))
	_ = zw.Close()

	decoded, closer, ok, err := DecodeMaybeGzip(bytes.NewReader(buf.Bytes()), "var/log/app.log.gz", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected gzip")
	}
	defer closer.Close()

	jobDir := t.TempDir()
	dir := filepath.Join(jobDir, "evidence")
	w := &Writer{Dir: dir, Limits: config.DefaultLimits()}
	cs, err := w.WriteMember("abc", "var/log/app.log.gz", decoded)
	if err != nil {
		t.Fatal(err)
	}
	if cs.LineCount != 2 {
		t.Fatalf("lines=%d", cs.LineCount)
	}
	body, _ := os.ReadFile(cs.CachePath)
	if !bytes.Contains(body, []byte("keyword")) {
		t.Fatalf("body=%s", body)
	}
	rel := RelativeCachePath(jobDir, cs.CachePath)
	if rel != "evidence/abc.txt" && rel != `evidence\abc.txt` {
		t.Fatalf("rel=%q", rel)
	}
}
