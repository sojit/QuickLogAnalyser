package evidence

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"quickanalyser/internal/config"
	"quickanalyser/internal/platform"
)

type Checkpoint struct {
	Line   int
	Offset int64
}

type CachedSource struct {
	ID           string
	RawPath      string
	CachePath    string
	LineCount    int
	Bytes        int64
	ContentSHA256 string
	Checkpoints  []Checkpoint
	Coverage     string
	Truncated    bool
	DecodedGzip  bool
}

type Writer struct {
	Dir    string
	Limits config.Limits
	Used   int64
}

// DecodeMaybeGzip wraps r with a gzip reader when the stream or path indicates gzip text.
// Callers must close the returned closer when non-nil (may be the gzip reader).
func DecodeMaybeGzip(r io.Reader, rawPath string, sampleHint []byte) (io.Reader, io.Closer, bool, error) {
	br := bufio.NewReaderSize(r, 256*1024)
	head, err := br.Peek(2)
	if err != nil && err != io.EOF && len(head) < 2 {
		return br, nil, false, nil
	}
	lower := strings.ToLower(rawPath)
	isGzipName := strings.HasSuffix(lower, ".gz") && !strings.HasSuffix(lower, ".tar.gz") && !strings.HasSuffix(lower, ".tgz")
	magic := len(head) >= 2 && head[0] == 0x1f && head[1] == 0x8b
	hintMagic := len(sampleHint) >= 2 && sampleHint[0] == 0x1f && sampleHint[1] == 0x8b
	if !isGzipName && !magic && !hintMagic {
		return br, nil, false, nil
	}
	gz, err := gzip.NewReader(br)
	if err != nil {
		return br, nil, false, fmt.Errorf("gzip decode %s: %w", rawPath, err)
	}
	return gz, gz, true, nil
}

// SampleAndRest reads up to n bytes for classification then returns a reader of sample+remainder.
func SampleAndRest(r io.Reader, n int) (sample []byte, rest io.Reader, err error) {
	if n <= 0 {
		n = 4096
	}
	buf := make([]byte, n)
	got, err := io.ReadFull(r, buf)
	if err == io.ErrUnexpectedEOF || err == io.EOF {
		err = nil
		buf = buf[:got]
	} else if err != nil {
		return nil, nil, err
	} else {
		buf = buf[:got]
	}
	return buf, io.MultiReader(bytes.NewReader(buf), r), nil
}

func (w *Writer) WriteMember(id, rawPath string, r io.Reader) (*CachedSource, error) {
	if err := platform.EnsureDir(w.Dir); err != nil {
		return nil, err
	}
	outPath := filepath.Join(w.Dir, id+".txt")
	f, err := os.Create(outPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	bw := bufio.NewWriterSize(f, 256*1024)
	br := bufio.NewReaderSize(r, 256*1024)
	cs := &CachedSource{ID: id, RawPath: rawPath, CachePath: outPath, Coverage: "complete"}
	var hasher hash.Hash = sha256.New()
	lineNo := 0
	var offset int64
	memberCap := w.Limits.MaxMemberTextBytes
	if memberCap <= 0 {
		memberCap = 2 << 30
	}
	maxLine := w.Limits.MaxLineBytes
	if maxLine <= 0 {
		maxLine = 1 << 20
	}
	for {
		line, truncatedLine, err := readLineBounded(br, maxLine)
		if len(line) > 0 {
			if !utf8.Valid(line) {
				line = []byte(stringsToValidUTF8(line))
			}
			if truncatedLine {
				if !bytes.HasSuffix(line, []byte("\n")) {
					line = append(line, []byte("…[line truncated]\n")...)
				} else {
					line = append(line[:len(line)-1], []byte("…[line truncated]\n")...)
				}
				cs.Truncated = true
				cs.Coverage = "partial"
			}
			lineNo++
			n, werr := bw.Write(line)
			_, _ = hasher.Write(line)
			offset += int64(n)
			w.Used += int64(n)
			cs.Bytes += int64(n)
			if werr != nil {
				return nil, werr
			}
			if lineNo%1024 == 1 {
				cs.Checkpoints = append(cs.Checkpoints, Checkpoint{Line: lineNo, Offset: offset - int64(n)})
			}
			if cs.Bytes > memberCap {
				cs.Coverage = "partial"
				cs.Truncated = true
				break
			}
			if w.Used > w.Limits.MaxEvidenceBytes {
				cs.Coverage = "partial"
				cs.Truncated = true
				break
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			cs.Coverage = "failed"
			return cs, fmt.Errorf("read member %s: %w", rawPath, err)
		}
	}
	if err := bw.Flush(); err != nil {
		return nil, err
	}
	cs.LineCount = lineNo
	cs.ContentSHA256 = hex.EncodeToString(hasher.Sum(nil))
	if lineNo == 0 {
		cs.Coverage = "empty"
	}
	return cs, nil
}

// ErrVerifyIncomplete indicates the verification budget was exhausted before a full hash.
var ErrVerifyIncomplete = fmt.Errorf("evidence verification incomplete")

// VerifyCacheFile checks size and SHA-256 of a retained evidence cache file.
func VerifyCacheFile(absPath string, expectBytes int64, expectSHA string) error {
	return VerifyCacheFileBudget(context.Background(), absPath, expectBytes, expectSHA, 0)
}

// VerifyCacheFileBudget hashes a cache file under a remaining byte budget and cancellable context.
// maxRead <= 0 means unlimited. Declared or actual size larger than maxRead yields ErrVerifyIncomplete
// without claiming a full successful verify.
func VerifyCacheFileBudget(ctx context.Context, absPath string, expectBytes int64, expectSHA string, maxRead int64) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %v", ErrVerifyIncomplete, err)
	}
	fi, err := os.Stat(absPath)
	if err != nil {
		return fmt.Errorf("unreadable evidence cache: %w", err)
	}
	size := fi.Size()
	if expectBytes > 0 && size != expectBytes {
		return fmt.Errorf("evidence cache size mismatch: have %d want %d", size, expectBytes)
	}
	if maxRead > 0 && size > maxRead {
		return fmt.Errorf("%w: source size %d exceeds remaining byte budget %d", ErrVerifyIncomplete, size, maxRead)
	}
	expectSHA = strings.ToLower(strings.TrimSpace(expectSHA))
	if expectSHA == "" {
		return fmt.Errorf("evidence content checksum unknown; reanalyse required")
	}
	f, err := os.Open(absPath)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	buf := make([]byte, 64*1024)
	var read int64
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("%w: %v", ErrVerifyIncomplete, err)
		}
		n, rerr := f.Read(buf)
		if n > 0 {
			if maxRead > 0 && read+int64(n) > maxRead {
				return fmt.Errorf("%w: read would exceed remaining byte budget", ErrVerifyIncomplete)
			}
			_, _ = h.Write(buf[:n])
			read += int64(n)
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return rerr
		}
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, expectSHA) {
		return fmt.Errorf("evidence cache checksum mismatch")
	}
	return nil
}

func stringsToValidUTF8(b []byte) string {
	return string([]rune(string(b)))
}

// readLineBounded reads up to maxBytes of a physical line without buffering an unbounded line.
// Extra bytes until newline (or EOF) are discarded when truncated.
func readLineBounded(br *bufio.Reader, maxBytes int) (line []byte, truncated bool, err error) {
	var out []byte
	for {
		chunk, e := br.ReadSlice('\n')
		if len(chunk) > 0 {
			if !truncated {
				remain := maxBytes - len(out)
				if remain <= 0 {
					truncated = true
				} else if len(chunk) > remain {
					out = append(out, chunk[:remain]...)
					truncated = true
				} else {
					out = append(out, chunk...)
				}
			}
		}
		if e == nil {
			return out, truncated, nil
		}
		if e == bufio.ErrBufferFull {
			continue
		}
		if e == io.EOF {
			return out, truncated, io.EOF
		}
		return out, truncated, e
	}
}

// ReadLines returns inclusive physical lines [start,end] from a cache file.
func ReadLines(cachePath string, start, end int, maxBytes int) ([]string, error) {
	return ReadLinesFrom(cachePath, start, end, maxBytes, nil)
}

// ReadLinesFrom uses sparse physical-line checkpoints to seek near start when available.
// Checkpoints must refer to byte offsets in the decoded cache file; line numbers are 1-based.
func ReadLinesFrom(cachePath string, start, end, maxBytes int, checkpoints []Checkpoint) ([]string, error) {
	if start < 1 {
		start = 1
	}
	if end < start {
		end = start
	}
	f, err := os.Open(cachePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	seekLine := 1
	var seekOff int64
	for _, cp := range checkpoints {
		if cp.Line <= start && cp.Line >= seekLine {
			seekLine = cp.Line
			seekOff = cp.Offset
		}
	}
	if seekOff > 0 {
		if _, err := f.Seek(seekOff, io.SeekStart); err != nil {
			if _, err2 := f.Seek(0, io.SeekStart); err2 != nil {
				return nil, err
			}
			seekLine, seekOff = 1, 0
		}
	}

	sc := bufio.NewScanner(f)
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 2<<20)
	var out []string
	n := seekLine - 1
	used := 0
	for sc.Scan() {
		n++
		if n < start {
			continue
		}
		if n > end {
			break
		}
		line := sc.Text()
		used += len(line)
		if maxBytes > 0 && used > maxBytes {
			out = append(out, line+"…[context truncated]")
			break
		}
		out = append(out, line)
	}
	return out, sc.Err()
}

// NearestCheckpoint returns the latest checkpoint at or before line.
func NearestCheckpoint(checkpoints []Checkpoint, line int) (Checkpoint, bool) {
	var best Checkpoint
	ok := false
	for _, cp := range checkpoints {
		if cp.Line <= line && (!ok || cp.Line >= best.Line) {
			best = cp
			ok = true
		}
	}
	return best, ok
}

// RelativeCachePath returns evidence/<id>.txt relative to the job directory when possible.
func RelativeCachePath(jobDir, absPath string) string {
	rel, err := filepath.Rel(jobDir, absPath)
	if err != nil {
		return filepath.Base(absPath)
	}
	return filepath.ToSlash(rel)
}
