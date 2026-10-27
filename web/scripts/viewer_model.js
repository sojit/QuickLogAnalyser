/** Pure continuous-viewer helpers (no DOM). Used by viewer.js and scroll_logic_test.cjs. */
(function (root) {
  const EST_LINE_PX = 22;
  const SECTION = 100;

  function clamp(n, lo, hi) {
    return Math.max(lo, Math.min(hi, n));
  }

  /** Sorted list of {start,end,height?} intervals. */
  function sortedIntervals(intervals) {
    return intervals.slice().sort((a, b) => a.start - b.start);
  }

  function coversLine(intervals, line) {
    for (const iv of intervals) {
      if (line >= iv.start && line <= iv.end) return true;
    }
    return false;
  }

  /** First missing physical line at or after `from` (1-based). null if none before lineCount. */
  function nextMissingFrom(intervals, from, lineCount) {
    if (lineCount < 1) return null;
    let line = Math.max(1, from);
    const sorted = sortedIntervals(intervals);
    while (line <= lineCount) {
      let covered = false;
      for (const iv of sorted) {
        if (line >= iv.start && line <= iv.end) {
          line = iv.end + 1;
          covered = true;
          break;
        }
      }
      if (!covered) return line;
    }
    return null;
  }

  /** Last missing line at or before `from`. */
  function prevMissingFrom(intervals, from, lineCount) {
    if (lineCount < 1) return null;
    let line = Math.min(lineCount, from);
    const sorted = sortedIntervals(intervals);
    while (line >= 1) {
      let covered = false;
      for (const iv of sorted) {
        if (line >= iv.start && line <= iv.end) {
          line = iv.start - 1;
          covered = true;
          break;
        }
      }
      if (!covered) return line;
      if (line < 1) return null;
    }
    return null;
  }

  function loadedExtent(intervals) {
    const sorted = sortedIntervals(intervals);
    if (!sorted.length) return null;
    return { first: sorted[0].start, last: sorted[sorted.length - 1].end, count: sorted.length };
  }

  /** Format loaded ranges without implying contiguity across holes. */
  function formatLoadedRanges(intervals) {
    const sorted = sortedIntervals(intervals);
    if (!sorted.length) return "none";
    return sorted.map((iv) => iv.start + "–" + iv.end).join(", ");
  }

  function hasHoles(intervals) {
    const sorted = sortedIntervals(intervals);
    for (let i = 1; i < sorted.length; i++) {
      if (sorted[i].start > sorted[i - 1].end + 1) return true;
    }
    return false;
  }

  /** Missing physical-line ranges inside [from,to] (inclusive), 1-based. */
  function missingRangesInSpan(intervals, from, to, lineCount) {
    const ranges = [];
    if (!lineCount || from > to) return ranges;
    let line = clamp(from, 1, lineCount);
    const end = clamp(to, 1, lineCount);
    while (line <= end) {
      if (coversLine(intervals, line)) {
        line++;
        continue;
      }
      const start = line;
      while (line <= end && !coversLine(intervals, line)) line++;
      ranges.push({ start: start, end: line - 1 });
    }
    return ranges;
  }

  /**
   * Viewport-demand scheduler: missing ranges covering the visible span plus overscan,
   * including internal gaps between cached sections (not only outer loaded edges).
   * @param {{scrollTop:number,clientHeight:number,intervals:any[],lineCount:number,overscanPx?:number,direction?:number}} g
   * @returns {{reason:string,line:number,rangeEnd:number}[]}
   */
  function viewportDemand(g) {
    const targets = [];
    if (!g || !g.lineCount || g.clientHeight <= 0) return targets;
    const ivs = g.intervals || [];
    const overscanPx = g.overscanPx != null ? g.overscanPx : 360;
    const overscanLines = Math.max(1, Math.ceil(overscanPx / EST_LINE_PX));
    const dir = g.direction || 0;

    const visA = lineAtScrollOffset(g.scrollTop, ivs, g.lineCount, 0);
    const visB = lineAtScrollOffset(g.scrollTop + g.clientHeight, ivs, g.lineCount, 0);
    const visLo = Math.min(visA, visB);
    const visHi = Math.max(visA, visB);
    const mid = (visLo + visHi) / 2;

    let from = Math.max(1, visLo - overscanLines);
    let to = Math.min(g.lineCount, visHi + overscanLines);
    if (dir > 0) to = Math.min(g.lineCount, to + overscanLines);
    if (dir < 0) from = Math.max(1, from - overscanLines);

    const missing = missingRangesInSpan(ivs, from, to, g.lineCount);
    missing.sort(function (a, b) {
      const aVis = a.end >= visLo && a.start <= visHi ? 0 : 1;
      const bVis = b.end >= visLo && b.start <= visHi ? 0 : 1;
      if (aVis !== bVis) return aVis - bVis;
      return Math.abs(a.start - mid) - Math.abs(b.start - mid);
    });
    for (let i = 0; i < missing.length; i++) {
      const r = missing[i];
      const visible = r.end >= visLo && r.start <= visHi;
      targets.push({
        reason: visible ? "viewport-gap" : "overscan-gap",
        line: r.start,
        rangeEnd: r.end,
      });
    }
    return targets;
  }

  /**
   * Prefetch decision: prefer viewport-demand (internal gaps); fall back to outer
   * loaded-edge targets when geometry content bounds are supplied.
   * @param {{viewportTop:number,viewportBottom:number,contentTop:number,contentBottom:number,prefetchPx:number,intervals:any[],lineCount:number,scrollTop?:number,clientHeight?:number,direction?:number}} g
   */
  function prefetchTargets(g) {
    const targets = [];
    if (!g || !g.lineCount) return targets;

    const scrollTop = g.scrollTop != null ? g.scrollTop : g.viewportTop;
    const clientHeight =
      g.clientHeight != null ? g.clientHeight : Math.max(1, g.viewportBottom - g.viewportTop);
    const demand = viewportDemand({
      scrollTop: scrollTop,
      clientHeight: clientHeight,
      intervals: g.intervals,
      lineCount: g.lineCount,
      overscanPx: g.prefetchPx,
      direction: g.direction || 0,
    });
    for (let i = 0; i < demand.length; i++) targets.push(demand[i]);

    // Outer-edge hints when content bounds are known and demand is empty near edges.
    const extent = loadedExtent(g.intervals);
    if (extent && g.contentBottom != null && g.contentTop != null) {
      const nearBottom =
        g.viewportBottom >= g.contentBottom - g.prefetchPx &&
        g.viewportBottom <= g.contentBottom + g.prefetchPx * 2;
      const nearTop =
        g.viewportTop <= g.contentTop + g.prefetchPx &&
        g.viewportTop + g.prefetchPx >= g.contentTop - g.prefetchPx;
      if (nearBottom) {
        const next = nextMissingFrom(g.intervals, extent.last + 1, g.lineCount);
        if (next != null && !targets.some(function (t) { return t.line === next; })) {
          targets.push({ reason: "boundary-next", line: next, rangeEnd: next });
        }
      }
      if (nearTop) {
        const prev = prevMissingFrom(g.intervals, extent.first - 1, g.lineCount);
        if (prev != null) {
          const start = Math.max(1, prev - SECTION + 1);
          if (!targets.some(function (t) { return t.line === start; })) {
            targets.push({ reason: "boundary-prev", line: start, rangeEnd: prev });
          }
        }
      }
    }
    return targets;
  }

  /**
   * Map a scroll offset into an estimated physical line using measured heights for loaded
   * intervals and EST_LINE_PX for unloaded regions.
   */
  function lineAtScrollOffset(scrollTop, intervals, lineCount, viewportCenterOffset) {
    const y = Math.max(0, scrollTop + (viewportCenterOffset || 0));
    const sorted = sortedIntervals(intervals);
    let pos = 0;
    let line = 1;
    const lastLoaded = sorted.length ? sorted[sorted.length - 1].end : 0;
    const firstLoaded = sorted.length ? sorted[0].start : lineCount + 1;

    // Top unloaded: lines 1 .. firstLoaded-1
    if (firstLoaded > 1) {
      const topLines = firstLoaded - 1;
      const topH = topLines * EST_LINE_PX;
      if (y < pos + topH) {
        const idx = Math.floor((y - pos) / EST_LINE_PX);
        return clamp(1 + idx, 1, lineCount);
      }
      pos += topH;
      line = firstLoaded;
    }

    for (let i = 0; i < sorted.length; i++) {
      const iv = sorted[i];
      // Gap before this interval
      if (iv.start > line) {
        const gapLines = iv.start - line;
        const gapH = gapLines * EST_LINE_PX;
        if (y < pos + gapH) {
          const idx = Math.floor((y - pos) / EST_LINE_PX);
          return clamp(line + idx, 1, lineCount);
        }
        pos += gapH;
      }
      const h = iv.height > 0 ? iv.height : (iv.end - iv.start + 1) * EST_LINE_PX;
      if (y < pos + h) {
        const frac = (y - pos) / h;
        const span = iv.end - iv.start;
        return clamp(iv.start + Math.floor(frac * (span + 1)), iv.start, iv.end);
      }
      pos += h;
      line = iv.end + 1;
    }

    // Bottom unloaded
    if (line <= lineCount) {
      const bottomLines = lineCount - line + 1;
      const bottomH = bottomLines * EST_LINE_PX;
      if (y < pos + bottomH) {
        const idx = Math.floor((y - pos) / EST_LINE_PX);
        return clamp(line + idx, 1, lineCount);
      }
      return lineCount;
    }
    return clamp(lastLoaded || 1, 1, lineCount);
  }

  /** Request window starting at `start`, capped by SECTION and lineCount. */
  function requestWindow(start, lineCount) {
    const s = clamp(start, 1, Math.max(1, lineCount));
    const e = Math.min(lineCount, s + SECTION - 1);
    return { start: s, end: e };
  }

  /**
   * After a byte-limited response, next fetch must continue at actualEnd+1 (not bucket+SECTION).
   */
  function nextAfterPartial(requestedStart, actualEnd, lineCount) {
    if (actualEnd < requestedStart) return null;
    const next = actualEnd + 1;
    if (next > lineCount) return null;
    return next;
  }

  root.QAViewerModel = {
    EST_LINE_PX,
    SECTION,
    clamp,
    sortedIntervals,
    coversLine,
    nextMissingFrom,
    prevMissingFrom,
    loadedExtent,
    formatLoadedRanges,
    hasHoles,
    missingRangesInSpan,
    viewportDemand,
    prefetchTargets,
    lineAtScrollOffset,
    requestWindow,
    nextAfterPartial,
  };
})(typeof window !== "undefined" ? window : globalThis);
