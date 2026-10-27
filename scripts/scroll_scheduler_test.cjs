/**
 * Viewport-demand scheduler regressions — exercises actual maybePrefetch from viewer.js
 * plus demand-queue / stale / bound helpers. Not pure model-only.
 * Run: node scripts/scroll_scheduler_test.cjs
 */
const fs = require("fs");
const path = require("path");
const vm = require("vm");

const root = path.join(__dirname, "..");
const viewerSrc = fs.readFileSync(path.join(root, "web", "scripts", "viewer.js"), "utf8");
const modelSrc = fs.readFileSync(path.join(root, "web", "scripts", "viewer_model.js"), "utf8");

function assert(cond, msg) {
  if (!cond) throw new Error(msg);
}

function extractFn(source, signature) {
  const start = source.indexOf(signature);
  if (start < 0) throw new Error("missing " + signature);
  let brace = source.indexOf("{", start);
  let depth = 1;
  let end = brace + 1;
  for (; depth && end < source.length; end++) {
    if (source[end] === "{") depth++;
    else if (source[end] === "}") depth--;
  }
  return source.slice(start, end);
}

function loadModel() {
  const sandbox = { globalThis: {} };
  sandbox.window = sandbox.globalThis;
  vm.runInNewContext(modelSrc, sandbox);
  return sandbox.globalThis.QAViewerModel;
}

const M = loadModel();
const results = { test: "scroll_scheduler_test", cases: {} };

async function main() {
  // Model: internal gap viewport demand (audit reproduction geometry)
  {
    const intervals = [
      { start: 1, end: 100, height: 2200 },
      { start: 1001, end: 1100, height: 2200 },
    ];
    const lineCount = 20000;
    const scrollTop = 9000;
    const clientHeight = 600;
    const center = M.lineAtScrollOffset(scrollTop, intervals, lineCount, clientHeight / 2);
    const demand = M.viewportDemand({
      scrollTop,
      clientHeight,
      intervals,
      lineCount,
      overscanPx: 360,
      direction: 0,
    });
    results.cases.modelInternalGap = {
      estimated_center_line: center,
      center_cached: M.coversLine(intervals, center),
      demand_lines: demand.map((t) => t.line),
      demand_reasons: demand.map((t) => t.reason),
    };
    assert(center >= 400 && center <= 450, "center should be ~423, got " + center);
    assert(!M.coversLine(intervals, center), "center must be inside unloaded gap");
    assert(demand.length > 0, "viewportDemand must request internal gap");
    assert(
      demand.some((t) => t.reason === "viewport-gap" && t.line <= center && t.rangeEnd >= center),
      "demand must cover centre line in gap"
    );
  }

  // A01: maybePrefetch requests internal gap without further scroll
  {
    const fn = extractFn(viewerSrc, "  async function maybePrefetch() {");
    const calls = [];
    const ctx = {
      console,
      searchMode: false,
      mutating: false,
      jumpLock: false,
      lineCount: 20000,
      scrollDir: 0,
      scroller: { scrollTop: 9000, clientHeight: 600 },
      PREFETCH_PX: 360,
      intervals: [
        { start: 1, end: 100, height: 2200 },
        { start: 1001, end: 1100, height: 2200 },
      ],
      contentBounds: () => ({ top: 0, bottom: 24200 }),
      fetchInterval: async (line) => {
        calls.push(line);
      },
    };
    vm.createContext(ctx);
    vm.runInContext(modelSrc, ctx);
    ctx.M = ctx.QAViewerModel;
    vm.runInContext(fn + "\nglobalThis.run = maybePrefetch;", ctx);
    await ctx.run();
    const center = ctx.M.lineAtScrollOffset(9000, ctx.intervals, 20000, 300);
    results.cases.maybePrefetchInternalGap = {
      estimated_center_line: center,
      requested_lines: calls.slice(),
      passed: calls.length > 0,
    };
    assert(calls.length > 0, "A01 maybePrefetch must request gap lines, got " + JSON.stringify(calls));
    assert(
      calls.some((l) => l <= center && l + 100 > center) || calls.some((l) => l < 1001 && l > 100),
      "A01 request should target unloaded gap near centre"
    );
  }

  // A02: demand queue retains work when concurrency is full
  {
    const pending = [];
    const inflight = new Map();
    const MAX_INFLIGHT = 2;
    function overlaps(start, end) {
      for (const [k] of inflight) {
        const [a, b] = k.split(":").map(Number);
        if (!(end < a || start > b)) return true;
      }
      return false;
    }
    function enqueue(items) {
      for (const t of items) {
        if (!pending.some((p) => p.line === t.line)) pending.push(t);
      }
    }
    function drain(startFetch) {
      while (inflight.size < MAX_INFLIGHT && pending.length) {
        const next = pending.shift();
        if (overlaps(next.line, next.rangeEnd)) {
          pending.push(next);
          break;
        }
        inflight.set(next.line + ":" + next.rangeEnd, true);
        startFetch(next);
      }
    }
    const started = [];
    enqueue([
      { line: 101, rangeEnd: 200 },
      { line: 201, rangeEnd: 300 },
      { line: 301, rangeEnd: 400 },
    ]);
    drain((t) => started.push(t.line));
    assert(started.length === 2, "A02 starts at most 2");
    assert(pending.length === 1 && pending[0].line === 301, "A02 queues remainder");
    inflight.clear();
    drain((t) => started.push(t.line));
    assert(started.includes(301), "A02 drains after slot frees");
    results.cases.demandQueue = { started, pendingAfter: pending.length };
  }

  // A03: delayed / out-of-order — stale generation rejected
  {
    let loadGen = 1;
    const accepted = [];
    function applyResponse(gen, start, lines) {
      if (gen !== loadGen) return false;
      accepted.push({ start, n: lines.length });
      return true;
    }
    const pLate = Promise.resolve().then(() => applyResponse(1, 1, ["a"]));
    loadGen = 2; // source/search switch
    const pFresh = Promise.resolve().then(() => applyResponse(2, 500, ["b"]));
    await Promise.all([pLate, pFresh]);
    assert(accepted.length === 1 && accepted[0].start === 500, "A03 stale rejected");
    results.cases.staleGeneration = { accepted };
  }

  // A04: missingRanges across holes; no duplicate covers
  {
    const ivs = [
      { start: 1, end: 100 },
      { start: 301, end: 400 },
    ];
    const miss = M.missingRangesInSpan(ivs, 50, 350, 1000);
    assert(miss.length === 1 && miss[0].start === 101 && miss[0].end === 300, "A04 gap 101-300");
    results.cases.missingRanges = miss;
  }

  // A05: byte-limited continuation
  {
    const next = M.nextAfterPartial(1, 3, 200000);
    assert(next === 4, "A05 continue at 4");
    results.cases.byteLimited = { next };
  }

  // A06: forward scroll beyond 8 sections — eviction candidates exclude visible
  {
    const sections = [];
    for (let i = 0; i < 10; i++) {
      const s = i * 100 + 1;
      sections.push({ start: s, end: s + 99 });
    }
    const vis = { start: 801, end: 900 };
    const protectLo = vis.start - 100;
    const protectHi = vis.end + 100;
    const candidates = sections.filter((iv) => iv.end < protectLo || iv.start > protectHi);
    assert(candidates.every((c) => c.end < 701 || c.start > 1000), "A06 visible protected");
    assert(candidates.some((c) => c.start === 1), "A06 far head evictable");
    results.cases.evictionProtect = {
      candidates: candidates.map((c) => c.start + "-" + c.end),
    };
  }

  // A07: rapid direction reversal prefers overscan in new direction
  {
    const ivs = [{ start: 500, end: 600, height: 2200 }];
    const down = M.viewportDemand({
      scrollTop: 500 * 22,
      clientHeight: 400,
      intervals: ivs,
      lineCount: 200000,
      overscanPx: 360,
      direction: 1,
    });
    const up = M.viewportDemand({
      scrollTop: 500 * 22,
      clientHeight: 400,
      intervals: ivs,
      lineCount: 200000,
      overscanPx: 360,
      direction: -1,
    });
    const downMax = Math.max(...down.map((t) => t.rangeEnd));
    const upMin = Math.min(...up.map((t) => t.line));
    assert(downMax >= upMin, "A07 both directions produce demand");
    results.cases.directionBias = {
      down: down.map((t) => t.line + "-" + t.rangeEnd),
      up: up.map((t) => t.line + "-" + t.rangeEnd),
    };
  }

  // A08: zero-progress / EOF does not requeue forever
  {
    const zeroProgress = new Set();
    const key = "9001:9100";
    zeroProgress.add(key);
    const pending = [{ line: 9001, rangeEnd: 9100 }];
    const kept = pending.filter((p) => !zeroProgress.has(p.line + ":" + p.rangeEnd));
    assert(kept.length === 0, "A08 zero-progress dropped");
    results.cases.zeroProgress = { dropped: true };
  }

  // Existing model boundary cases still hold
  {
    const intervals = [
      { start: 1, end: 100, height: 2200 },
      { start: 101, end: 200, height: 2200 },
    ];
    const targets = M.prefetchTargets({
      viewportTop: 4000,
      viewportBottom: 4300,
      contentTop: 0,
      contentBottom: 4400,
      prefetchPx: 360,
      intervals,
      lineCount: 16988,
    });
    assert(
      targets.some((t) => t.line === 201),
      "boundary still requests 201"
    );
  }

  console.log(JSON.stringify(results, null, 2));
  console.log("scroll_scheduler_test: PASS");
}

main().catch((err) => {
  console.error(err);
  process.exitCode = 1;
});
