package archive

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"quickanalyser/internal/config"
)

func TestWalkUnknownProductFixture(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	archivePath := filepath.Join(root, "testdata", "sanitized", "unknown_product.tar.gz")
	f, err := os.Open(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := &Walker{Limits: config.DefaultLimits()}
	var paths []string
	err = w.Walk(context.Background(), f, "unknown_product.tar.gz", 1, func(ctx context.Context, m Member, r io.Reader) error {
		if m.Kind == KindRegular {
			paths = append(paths, m.Path)
			if r != nil {
				_, _ = io.Copy(io.Discard, r)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("expected members from unknown product fixture")
	}
}
