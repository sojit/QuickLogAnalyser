package archive

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"path"
	"strings"

	"quickanalyser/internal/config"
)

type MemberKind string

const (
	KindRegular MemberKind = "regular"
	KindDir     MemberKind = "dir"
	KindLink    MemberKind = "link"
	KindOther   MemberKind = "other"
)

type Member struct {
	Path     string
	Kind     MemberKind
	Size     int64
	Depth    int
	IsGzip   bool
	IsTar    bool
	Parent   string
}

type VisitFunc func(ctx context.Context, m Member, r io.Reader) error

type Walker struct {
	Limits         config.Limits
	ExpandedBytes  int64
	MemberCount    int
	OnBudgetExceed func(reason string)
}

func (w *Walker) Walk(ctx context.Context, r io.Reader, name string, depth int, visit VisitFunc) error {
	if depth > w.Limits.MaxNestingDepth {
		if w.OnBudgetExceed != nil {
			w.OnBudgetExceed(fmt.Sprintf("nesting depth exceeded at %s", name))
		}
		return nil
	}
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		gr, err := gzip.NewReader(r)
		if err != nil {
			return fmt.Errorf("gzip open %s: %w", name, err)
		}
		defer gr.Close()
		return w.walkTar(ctx, gr, name, depth, visit)
	case strings.HasSuffix(lower, ".gz") && !strings.HasSuffix(lower, ".tar.gz"):
		gr, err := gzip.NewReader(r)
		if err != nil {
			return fmt.Errorf("gzip open %s: %w", name, err)
		}
		defer gr.Close()
		inner := strings.TrimSuffix(name, ".gz")
		if strings.HasSuffix(strings.ToLower(inner), ".tar") {
			return w.walkTar(ctx, gr, name, depth, visit)
		}
		m := Member{Path: name, Kind: KindRegular, Depth: depth, IsGzip: true, Parent: name}
		br := &countingReader{R: gr, W: w}
		return visit(ctx, m, br)
	case strings.HasSuffix(lower, ".tar"):
		return w.walkTar(ctx, r, name, depth, visit)
	default:
		m := Member{Path: name, Kind: KindRegular, Depth: depth}
		br := &countingReader{R: r, W: w}
		return visit(ctx, m, br)
	}
}

func (w *Walker) walkTar(ctx context.Context, r io.Reader, parent string, depth int, visit VisitFunc) error {
	tr := tar.NewReader(r)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("tar read in %s: %w", parent, err)
		}
		w.MemberCount++
		if w.MemberCount > w.Limits.MaxMembers {
			if w.OnBudgetExceed != nil {
				w.OnBudgetExceed("member count budget exceeded")
			}
			return nil
		}
		rawPath := path.Clean("/" + hdr.Name)
		rawPath = strings.TrimPrefix(rawPath, "/")
		full := path.Join(parent, rawPath)
		if parent == "" || parent == "." {
			full = rawPath
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			_ = visit(ctx, Member{Path: full, Kind: KindDir, Depth: depth, Parent: parent}, nil)
			continue
		case tar.TypeSymlink, tar.TypeLink:
			_ = visit(ctx, Member{Path: full, Kind: KindLink, Depth: depth, Parent: parent}, nil)
			continue
		case tar.TypeReg, tar.TypeRegA:
			// ok
		default:
			_ = visit(ctx, Member{Path: full, Kind: KindOther, Size: hdr.Size, Depth: depth, Parent: parent}, nil)
			continue
		}
		lower := strings.ToLower(hdr.Name)
		nested := strings.HasSuffix(lower, ".tar") || strings.HasSuffix(lower, ".tar.gz") ||
			strings.HasSuffix(lower, ".tgz") || (strings.HasSuffix(lower, ".gz") && looksLikeNestedArchive(hdr.Name))
		if nested && depth < w.Limits.MaxNestingDepth {
			if err := w.Walk(ctx, tr, full, depth+1, visit); err != nil {
				return err
			}
			continue
		}
		m := Member{Path: full, Kind: KindRegular, Size: hdr.Size, Depth: depth, Parent: parent,
			IsGzip: strings.HasSuffix(lower, ".gz"), IsTar: strings.HasSuffix(lower, ".tar")}
		cr := &countingReader{R: tr, W: w}
		br := &limitedReader{R: cr, Limit: w.Limits.MaxMemberTextBytes}
		if err := visit(ctx, m, br); err != nil {
			// Still drain remaining member bytes so tar accounting stays correct.
			_, _ = io.Copy(io.Discard, cr)
			return err
		}
		// Count and discard unread trailing payload so skipped bytes apply to the budget.
		if _, err := io.Copy(io.Discard, cr); err != nil && !errorsIsBudget(err) {
			return err
		}
		if w.ExpandedBytes > w.Limits.MaxExpandedBytes {
			if w.OnBudgetExceed != nil {
				w.OnBudgetExceed("expanded byte budget exceeded")
			}
			return fmt.Errorf("expanded byte budget exceeded")
		}
	}
}

func errorsIsBudget(err error) bool {
	return err != nil && strings.Contains(err.Error(), "budget exceeded")
}

func looksLikeNestedArchive(name string) bool {
	l := strings.ToLower(name)
	return strings.Contains(l, ".tar.") || strings.HasSuffix(l, ".tgz")
}

type countingReader struct {
	R io.Reader
	W *Walker
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.R.Read(p)
	c.W.ExpandedBytes += int64(n)
	if c.W.ExpandedBytes > c.W.Limits.MaxExpandedBytes {
		if c.W.OnBudgetExceed != nil {
			c.W.OnBudgetExceed("expanded byte budget exceeded")
		}
		return n, fmt.Errorf("expanded byte budget exceeded")
	}
	return n, err
}

type limitedReader struct {
	R     io.Reader
	Limit int64
	read  int64
}

func (l *limitedReader) Read(p []byte) (int, error) {
	if l.read >= l.Limit {
		_, _ = io.Copy(io.Discard, l.R)
		return 0, fmt.Errorf("member text budget exceeded")
	}
	max := int64(len(p))
	if rem := l.Limit - l.read; rem < max {
		max = rem
	}
	n, err := l.R.Read(p[:max])
	l.read += int64(n)
	return n, err
}
