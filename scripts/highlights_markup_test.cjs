#!/usr/bin/env node
/**
 * Structure regression for Log Highlights layout (draft.22).
 * Not a pixel/browser acceptance test.
 */
const path = require("path");
const assert = require("assert");
const HL = require("../web/scripts/highlights_layout.js");

const jobId = "fixturejobfixturejobfixturejob12";
const sourceId = "fixturesourcefixturesourcefix01";
const sourcePath = "NX_snapshot.tar.gz/NX_snapshot/nxserver/log/FastUpdateServer.log";
const groups = [
  {
    severity: "error",
    categories: "severity",
    count: 4,
    line_start: 8898,
    line_end: 8898,
    raw_time: "2026-10-23 11:38:12",
    message: "ERROR sanitized fixture line one",
    source_id: sourceId,
    path: sourcePath,
  },
  {
    severity: "error",
    categories: "severity",
    count: 4,
    line_start: 8899,
    line_end: 9015,
    raw_time: "2026-10-23 11:38:12",
    message: "ERROR sanitized fixture\nmultiline body",
    source_id: sourceId,
    path: sourcePath,
  },
];

function rangeHref(g) {
  return `/viewer.html?job=${jobId}&source=${g.source_id}&line=${g.line_start}&line_end=${g.line_end}`;
}

const html = HL.renderFileBlock({
  jobId,
  sourceId,
  path: sourcePath,
  groups,
  fileHref: `/viewer.html?job=${jobId}&source=${sourceId}`,
  rangeHrefFor: rangeHref,
  esc: HL.defaultEsc,
});

const analysis = HL.analyzeMarkup(html);
console.log(JSON.stringify({ kind: "highlights_layout_structure", analysis, sample_href_checks: true }, null, 2));

assert.strictEqual(analysis.filenameBlocks, 1, "one filename header");
assert.strictEqual(analysis.pathBlocks, 1, "one path header");
assert.strictEqual(analysis.lineRanges, 2, "two compact line-range links");
assert.strictEqual(analysis.issues, 2, "two issue blocks");
assert.strictEqual(analysis.fileLinksInMeta, false, "no file-link inside metadata");
assert.strictEqual(analysis.fileNameInMeta, false, "no file-name inside metadata");
assert.ok(analysis.ok, "structure contract");

assert.ok(html.includes("line=8898"), "first range keeps start");
assert.ok(html.includes("line_end=8898"), "first range keeps end");
assert.ok(html.includes("line=8899") && html.includes("line_end=9015"), "second range keeps span");
assert.ok(!/hl-issue-meta[\s\S]*FastUpdateServer\.log/.test(html), "basename not repeated in metadata");
assert.ok(html.includes("occurrence(s) on page"), "page-local count label");

console.log("highlights_markup_test: PASS");
