/**
 * Audit regressions for continuous-viewer prefetch/interval math (no browser).
 * Run: node scripts/scroll_logic_test.cjs
 */
const fs = require("fs");
const path = require("path");
const vm = require("vm");

const src = fs.readFileSync(path.join(__dirname, "..", "web", "scripts", "viewer_model.js"), "utf8");
const sandbox = { globalThis: {} };
sandbox.window = sandbox.globalThis;
vm.runInNewContext(src, sandbox);
const M = sandbox.globalThis.QAViewerModel;

function assert(cond, msg) {
  if (!cond) throw new Error(msg);
}

const results = { test: "mocked scroll geometry from viewer_model.js", cases: {} };

// F01: near loaded boundary (line ~187 with 1–200 loaded) must request 201
{
  const intervals = [
    { start: 1, end: 100, height: 2200 },
    { start: 101, end: 200, height: 2200 },
  ];
  const contentTop = 0;
  const contentBottom = 4400;
  const viewportTop = 4000;
  const viewportBottom = 4300;
  const targets = M.prefetchTargets({
    viewportTop,
    viewportBottom,
    contentTop,
    contentBottom,
    prefetchPx: 360,
    intervals,
    lineCount: 16988,
  });
  const lines = targets.map((t) => t.line);
  results.cases.atLoadedBoundary = { requested: lines, expectedNext: 201 };
  assert(lines.includes(201), "F01 expected next missing 201, got " + JSON.stringify(lines));
}

// F02: thumb near EOF maps to end region, not 201
{
  const intervals = [
    { start: 1, end: 100, height: 2200 },
    { start: 101, end: 200, height: 2200 },
  ];
  const lineCount = 16988;
  // Approximate scrollTop near end of virtual document
  const scrollTop = (lineCount - 40) * M.EST_LINE_PX;
  const target = M.lineAtScrollOffset(scrollTop, intervals, lineCount, 200);
  results.cases.thumbAtEOF = { mappedLine: target, expectedNear: lineCount };
  assert(target > 16000, "F02 thumb near EOF should map near end, got " + target);
  const win = M.requestWindow(target, lineCount);
  assert(win.start > 16000, "F02 request window should be near EOF, got " + JSON.stringify(win));
}

// F03: byte-limited 1–3 continues at 4
{
  const next = M.nextAfterPartial(1, 3, 16988);
  const intervals = [{ start: 1, end: 3 }];
  const missing = M.nextMissingFrom(intervals, 1, 16988);
  results.cases.byteLimitedSection = { nextAfterPartial: next, nextMissing: missing, expected: 4 };
  assert(next === 4 && missing === 4, "F03 expected continue at 4");
}

// Full-height distance must NOT be the sole prefetch signal (document invariant)
{
  const intervals = [
    { start: 1, end: 100 },
    { start: 101, end: 200 },
  ];
  // Viewport near content bottom but far from virtual scrollHeight bottom
  const targets = M.prefetchTargets({
    viewportTop: 4000,
    viewportBottom: 4350,
    contentTop: 0,
    contentBottom: 4400,
    prefetchPx: 360,
    intervals,
    lineCount: 16988,
  });
  assert(
    targets.some((t) => t.line === 201),
    "boundary prefetch must fire without being at virtual EOF"
  );
}

// F04: internal unloaded gap (audit reproduction) must demand missing lines
{
  const intervals = [
    { start: 1, end: 100, height: 2200 },
    { start: 1001, end: 1100, height: 2200 },
  ];
  const scrollTop = 9000;
  const clientHeight = 600;
  const center = M.lineAtScrollOffset(scrollTop, intervals, 20000, clientHeight / 2);
  const demand = M.viewportDemand({
    scrollTop,
    clientHeight,
    intervals,
    lineCount: 20000,
    overscanPx: 360,
  });
  results.cases.internalGap = {
    center,
    requested: demand.map((t) => t.line + "-" + t.rangeEnd),
  };
  assert(!M.coversLine(intervals, center), "F04 centre in gap");
  assert(demand.length > 0, "F04 viewportDemand must not be empty for internal gap");
  assert(
    demand.some((t) => t.line <= center && t.rangeEnd >= center),
    "F04 demand covers centre"
  );
}

console.log(JSON.stringify(results, null, 2));
console.log("scroll_logic_test: PASS");
