package web_test

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// Structure regression for Log Highlights layout (design 0.2.0-draft.22).
// Executes the same two-issue/one-file contract as scripts/highlights_markup_test.cjs
// without requiring Node: mirrors the JS helpers' markup shape and asserts hrefs.
// This is not a pixel/browser acceptance test.

func TestHighlightsLayoutTwoIssuesOneFile(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	root := filepath.Dir(thisFile)
	jsPath := filepath.Join(root, "scripts", "highlights_layout.js")
	js, err := os.ReadFile(jsPath)
	if err != nil {
		t.Fatalf("read layout helper: %v", err)
	}
	src := string(js)
	for _, need := range []string{
		"renderFileBlock",
		"renderLineRangeLink",
		"hl-file-name",
		"hl-line-range",
		"analyzeMarkup",
		"occurrence(s) on page",
	} {
		if !strings.Contains(src, need) {
			t.Fatalf("highlights_layout.js missing %q", need)
		}
	}
	// Issue renderer must not embed shared fileLink / file-name blocks.
	issueFn := extractFunctionBody(src, "renderIssue")
	if issueFn == "" {
		t.Fatal("could not extract renderIssue")
	}
	if strings.Contains(issueFn, "file-link") || strings.Contains(issueFn, "file-name") || strings.Contains(issueFn, "fileLink") {
		t.Fatal("renderIssue must not embed fileLink/file-name components")
	}

	jobID := "fixturejobfixturejobfixturejob12"
	sourceID := "fixturesourcefixturesourcefix01"
	sourcePath := "NX_snapshot.tar.gz/NX_snapshot/nxserver/log/FastUpdateServer.log"
	groups := []hlGroup{
		{severity: "error", categories: "severity", count: 4, lineStart: 8898, lineEnd: 8898, when: "2026-10-23 11:38:12", message: "ERROR sanitized fixture line one", sourceID: sourceID, path: sourcePath},
		{severity: "error", categories: "severity", count: 4, lineStart: 8899, lineEnd: 9015, when: "2026-10-23 11:38:12", message: "ERROR sanitized fixture\nmultiline body", sourceID: sourceID, path: sourcePath},
	}
	html := renderFileBlockGo(jobID, sourceID, sourcePath, groups, func(g hlGroup) string {
		return "/viewer.html?job=" + jobID + "&source=" + g.sourceID + "&line=" + itoa(g.lineStart) + "&line_end=" + itoa(g.lineEnd)
	})

	analysis := analyzeMarkupGo(html)
	if analysis.filenameBlocks != 1 {
		t.Fatalf("filenameBlocks=%d want 1", analysis.filenameBlocks)
	}
	if analysis.pathBlocks != 1 {
		t.Fatalf("pathBlocks=%d want 1", analysis.pathBlocks)
	}
	if analysis.lineRanges != 2 {
		t.Fatalf("lineRanges=%d want 2", analysis.lineRanges)
	}
	if analysis.issues != 2 {
		t.Fatalf("issues=%d want 2", analysis.issues)
	}
	if analysis.fileLinksInMeta || analysis.fileNameInMeta {
		t.Fatal("filename/path components must not appear inside issue metadata")
	}
	if !analysis.ok {
		t.Fatalf("structure contract failed: %+v", analysis)
	}
	if !strings.Contains(html, "line=8898") || !strings.Contains(html, "line_end=8898") {
		t.Fatal("first range href missing")
	}
	if !strings.Contains(html, "line=8899") || !strings.Contains(html, "line_end=9015") {
		t.Fatal("second range href missing")
	}
	if regexp.MustCompile(`hl-issue-meta[\s\S]*FastUpdateServer\.log`).MatchString(html) {
		t.Fatal("basename must not repeat in metadata")
	}
	if !strings.Contains(html, "occurrence(s) on page") {
		t.Fatal("page-local count label missing")
	}

	appJS, err := os.ReadFile(filepath.Join(root, "scripts", "app.js"))
	if err != nil {
		t.Fatalf("read app.js: %v", err)
	}
	app := string(appJS)
	if !strings.Contains(app, "QAHighlightsLayout") || !strings.Contains(app, "renderFileBlock") {
		t.Fatal("app.js must wire QAHighlightsLayout.renderFileBlock for Highlights")
	}
	// Guard against regressing to inline fileLink inside the highlights issue loop.
	if regexp.MustCompile(`hl-issue-meta[\s\S]{0,400}fileLink\(`).MatchString(app) {
		t.Fatal("app.js highlights path must not call fileLink inside hl-issue-meta")
	}
}

type hlGroup struct {
	severity, categories, when, message, sourceID, path string
	count, lineStart, lineEnd                           int
}

type hlAnalysis struct {
	filenameBlocks, pathBlocks, lineRanges, issues int
	fileLinksInMeta, fileNameInMeta, ok            bool
}

func renderFileBlockGo(jobID, sourceID, path string, groups []hlGroup, rangeHref func(hlGroup) string) string {
	name, rel := pathParts(path)
	var b strings.Builder
	b.WriteString(`<section class="file-block panel hl-file-block">`)
	b.WriteString(`<div class="hl-file-header"><div class="hl-file-identity">`)
	b.WriteString(`<a class="hl-file-name" href="/viewer.html?job=` + jobID + `&source=` + sourceID + `" target="_blank" rel="noopener" title="` + path + `">` + name + `</a>`)
	if rel != "" {
		b.WriteString(`<div class="hl-file-path" title="` + path + `">` + rel + `</div>`)
	}
	b.WriteString(`</div><button type="button" class="btn open-log hl-file-open">Open</button></div>`)
	b.WriteString(`<div class="hl-file-summary muted">` + itoa(len(groups)) + ` issue group(s) on this page (page-local grouping)</div>`)
	for _, g := range groups {
		href := rangeHref(g)
		b.WriteString(`<article class="hl-issue"><div class="hl-issue-meta">`)
		b.WriteString(`<span class="sev-` + g.severity + `">` + g.severity + `</span>`)
		if g.categories != "" {
			b.WriteString(` · ` + g.categories)
		}
		b.WriteString(`<span class="hl-issue-count"> · ` + itoa(g.count) + ` occurrence(s) on page · </span>`)
		b.WriteString(`<a class="hl-line-range" href="` + href + `" target="_blank" rel="noopener">Lines ` + itoa(g.lineStart) + `–` + itoa(g.lineEnd) + `</a>`)
		b.WriteString(`</div><div class="hl-issue-time muted">` + g.when + `</div>`)
		b.WriteString(`<pre class="hl-issue-snippet">` + g.message + `</pre></article>`)
	}
	b.WriteString(`</section>`)
	return b.String()
}

func analyzeMarkupGo(html string) hlAnalysis {
	a := hlAnalysis{
		filenameBlocks:  len(regexp.MustCompile(`class="hl-file-name"`).FindAllString(html, -1)),
		pathBlocks:      len(regexp.MustCompile(`class="hl-file-path"`).FindAllString(html, -1)),
		lineRanges:      len(regexp.MustCompile(`class="hl-line-range"`).FindAllString(html, -1)),
		issues:          len(regexp.MustCompile(`class="hl-issue"`).FindAllString(html, -1)),
		fileLinksInMeta: regexp.MustCompile(`hl-issue-meta[\s\S]*?class="file-link"`).MatchString(html),
		fileNameInMeta:  regexp.MustCompile(`hl-issue-meta[\s\S]*?class="file-name"`).MatchString(html),
	}
	a.ok = a.filenameBlocks == 1 && a.pathBlocks == 1 && a.lineRanges == 2 && a.issues == 2 && !a.fileLinksInMeta && !a.fileNameInMeta
	return a
}

func pathParts(full string) (name, rel string) {
	p := strings.ReplaceAll(full, `\`, `/`)
	i := strings.LastIndex(p, "/")
	if i < 0 {
		return p, ""
	}
	return p[i+1:], p[:i]
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d [20]byte
	i := len(d)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		d[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		d[i] = '-'
	}
	return string(d[i:])
}

func extractFunctionBody(src, name string) string {
	re := regexp.MustCompile(`function\s+` + name + `\s*\([^)]*\)\s*\{`)
	loc := re.FindStringIndex(src)
	if loc == nil {
		return ""
	}
	start := loc[1]
	depth := 1
	for i := start; i < len(src); i++ {
		switch src[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return src[start:i]
			}
		}
	}
	return ""
}
