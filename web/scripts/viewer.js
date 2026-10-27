(function () {
  const M = window.QAViewerModel;
  const params = new URLSearchParams(location.search);
  const job = params.get("job") || "";
  const source = params.get("source") || "";
  const focusLine = Number(params.get("line") || "1") || 1;
  const titleParam = params.get("title") || "";
  const urlSession = params.get("s") || "";
  if (urlSession) {
    try {
      localStorage.setItem("qa_session", urlSession);
    } catch (_) {}
  }
  const session =
    urlSession ||
    localStorage.getItem("qa_session") ||
    (document.cookie.match(/qa_session=([^;]+)/) || [])[1] ||
    "";

  const MAX_SECTIONS = 8;
  const MAX_CACHE_BYTES = 2 * 1024 * 1024;
  const MAX_INFLIGHT = 2;
  const PREFETCH_PX = 360;
  const PAGE_SIZE = 50;

  const pathTitle = document.getElementById("path-title");
  const pathFull = document.getElementById("path-full");
  const pathDetail = document.getElementById("path-detail");
  const countEl = document.getElementById("count");
  const coverageEl = document.getElementById("coverage-note");
  const rangeEl = document.getElementById("range-note");
  const scroller = document.getElementById("source-view");
  const topSpacer = document.getElementById("virt-top");
  const bottomSpacer = document.getElementById("virt-bottom");
  const sectionsHost = document.getElementById("virt-sections");
  const statusEl = document.getElementById("virt-status");
  const techEl = document.getElementById("virt-tech");
  const chromeEl = document.getElementById("viewer-chrome");
  const queryEl = document.getElementById("query");
  const caseEl = document.getElementById("case");
  const matchingOnlyEl = document.getElementById("matching-only");
  const wrapEl = document.getElementById("wrap-lines");
  const gotoEl = document.getElementById("goto-line");
  const copyPathBtn = document.getElementById("btn-copy-path");

  function basename(p) {
    const s = String(p || "").replace(/\\/g, "/");
    const i = s.lastIndexOf("/");
    return i < 0 ? s : s.slice(i + 1);
  }

  let lineCount = 0;
  let coverage = "";
  let sourcePath = titleParam || "";
  let lastQuery = "";
  let searchMode = false;
  let searchId = "";
  let searchGen = 0;
  let searchMeta = null;
  let searchPageHits = [];
  let searchOffset = 0;
  let loadGen = 0;
  let mutating = false;
  let jumpLock = false;
  /** @type {{start:number,end:number,lines:string[],el:HTMLElement,height:number,bytes:number,eof:boolean}[]} */
  let intervals = [];
  /** @type {Map<string, {token:number, controller:AbortController, promise:Promise<any>}>} */
  const inflight = new Map();
  let inflightToken = 0;
  let scheduleTok = 0;
  let scrollDir = 0;
  let lastScrollTop = 0;
  let prefetchBusy = false;
  let needReschedule = false;
  /** @type {{reason:string,line:number,rangeEnd:number}[]} */
  let pendingDemand = [];
  /** Tracks zero-progress requests to avoid busy loops. */
  const zeroProgress = new Set();

  function headers() {
    const h = { Accept: "application/json" };
    if (session) h["X-QA-Session"] = session;
    return h;
  }

  function escapeHtml(s) {
    return String(s)
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;");
  }

  function highlight(text, q, caseSensitive) {
    if (!q) return escapeHtml(text);
    const src = caseSensitive ? text : text.toLowerCase();
    const needle = caseSensitive ? q : q.toLowerCase();
    let out = "";
    let i = 0;
    while (i < text.length) {
      const j = src.indexOf(needle, i);
      if (j < 0) {
        out += escapeHtml(text.slice(i));
        break;
      }
      out += escapeHtml(text.slice(i, j)) + "<mark>" + escapeHtml(text.slice(j, j + q.length)) + "</mark>";
      i = j + q.length;
    }
    return out;
  }

  function applyWrap() {
    scroller.classList.toggle("wrap-on", !!wrapEl.checked);
    scroller.classList.toggle("wrap-off", !wrapEl.checked);
  }

  function setStatus(msg, retryFn) {
    // Keep a visible failure until Retry is used or a non-empty status replaces it.
    if (!msg) {
      if (document.getElementById("virt-retry")) return;
      statusEl.innerHTML = "";
      return;
    }
    if (retryFn) {
      statusEl.innerHTML =
        escapeHtml(msg) + ' <button type="button" class="btn" id="virt-retry">Retry</button>';
      const btn = document.getElementById("virt-retry");
      if (btn)
        btn.onclick = () => {
          statusEl.innerHTML = "";
          retryFn();
        };
    } else {
      statusEl.textContent = msg;
    }
  }

  function cacheBytes() {
    return intervals.reduce((n, iv) => n + (iv.bytes || 0), 0);
  }

  function updateIdentity() {
    const full = sourcePath || titleParam || "Source";
    pathTitle.textContent = basename(full) || "Source";
    pathTitle.title = full;
    if (pathFull) pathFull.textContent = full;
    document.title = (basename(full) || "Source") + " — QuickAnalyser";
    if (pathDetail) {
      pathDetail.textContent =
        "Job " + job + " · source " + source + (sourcePath ? "" : " · path pending");
    }
  }

  if (copyPathBtn) {
    copyPathBtn.onclick = async () => {
      const text = (pathFull && pathFull.textContent) || sourcePath || titleParam || "";
      try {
        await navigator.clipboard.writeText(text);
        copyPathBtn.textContent = "Copied";
        setTimeout(() => { copyPathBtn.textContent = "Copy path"; }, 1200);
      } catch (_) {
        copyPathBtn.textContent = "Copy failed";
        setTimeout(() => { copyPathBtn.textContent = "Copy path"; }, 1200);
      }
    };
  }

  function updateRangeNote() {
    if (!intervals.length) {
      const empty = lineCount ? "Loaded: none · Retained source lines: " + lineCount : "";
      rangeEl.textContent = empty;
      if (techEl) techEl.textContent = "";
      return;
    }
    const vis = visibleLineRange();
    // Compact status: loaded range + coverage extent only.
    let compact = "Loaded " + M.formatLoadedRanges(intervals);
    if (vis) compact += " · Viewport ≈ " + vis.start + "–" + vis.end;
    const extent = M.loadedExtent(intervals);
    if (extent && extent.last >= lineCount && lineCount > 0) compact += " · End of file reached";
    rangeEl.textContent = compact;
    // Technical counters live in Details (draft.23 audit follow-up).
    if (techEl) {
      let tech =
        "Retained source lines: " +
        lineCount +
        " · Sections: " +
        intervals.length +
        "/" +
        MAX_SECTIONS +
        " · Cache ≈ " +
        Math.round(cacheBytes() / 1024) +
        " KiB";
      if (M.hasHoles(intervals)) tech += " · gaps present";
      techEl.textContent = tech;
    }
  }

  function onChromeLayoutChange() {
    if (searchMode) return;
    const anchor = captureAnchor();
    remeasure();
    updateSpacers();
    restoreAnchor(anchor);
    updateRangeNote();
    schedulePrefetch();
  }

  function visibleLineRange() {
    const rootRect = scroller.getBoundingClientRect();
    const rows = sectionsHost.querySelectorAll(".logline");
    let start = 0;
    let end = 0;
    for (const row of rows) {
      const r = row.getBoundingClientRect();
      if (r.bottom < rootRect.top || r.top > rootRect.bottom) continue;
      const n = Number(row.dataset.line || "0");
      if (!start || n < start) start = n;
      if (n > end) end = n;
    }
    return start ? { start, end } : null;
  }

  function captureAnchor() {
    const rootRect = scroller.getBoundingClientRect();
    const rows = sectionsHost.querySelectorAll(".logline");
    for (const row of rows) {
      const r = row.getBoundingClientRect();
      if (r.bottom < rootRect.top || r.top > rootRect.bottom) continue;
      return {
        line: Number(row.dataset.line || "0"),
        offset: r.top - rootRect.top,
      };
    }
    return {
      line: M.lineAtScrollOffset(
        scroller.scrollTop,
        intervals.map((iv) => ({ start: iv.start, end: iv.end, height: iv.height })),
        lineCount,
        scroller.clientHeight / 2
      ),
      offset: 0,
    };
  }

  function restoreAnchor(anchor) {
    if (!anchor || !anchor.line) return;
    const row = sectionsHost.querySelector('.logline[data-line="' + anchor.line + '"]');
    if (row) {
      const rootRect = scroller.getBoundingClientRect();
      const r = row.getBoundingClientRect();
      const delta = r.top - rootRect.top - (anchor.offset || 0);
      scroller.scrollTop = Math.max(0, scroller.scrollTop + delta);
      return;
    }
    // Estimated restore via model
    const y = estimateScrollForLine(anchor.line) - (anchor.offset || 0);
    scroller.scrollTop = Math.max(0, y);
  }

  function estimateScrollForLine(line) {
    const sorted = M.sortedIntervals(
      intervals.map((iv) => ({ start: iv.start, end: iv.end, height: iv.height }))
    );
    let pos = 0;
    let cur = 1;
    const first = sorted.length ? sorted[0].start : lineCount + 1;
    if (line < first) return (line - 1) * M.EST_LINE_PX;
    if (first > 1) pos += (first - 1) * M.EST_LINE_PX;
    cur = first;
    for (const iv of sorted) {
      if (iv.start > cur) {
        if (line < iv.start) return pos + (line - cur) * M.EST_LINE_PX;
        pos += (iv.start - cur) * M.EST_LINE_PX;
      }
      const h = iv.height > 0 ? iv.height : (iv.end - iv.start + 1) * M.EST_LINE_PX;
      if (line >= iv.start && line <= iv.end) {
        const frac = (line - iv.start) / Math.max(1, iv.end - iv.start + 1);
        return pos + frac * h;
      }
      pos += h;
      cur = iv.end + 1;
    }
    return pos + Math.max(0, line - cur) * M.EST_LINE_PX;
  }

  function updateSpacers() {
    const sorted = M.sortedIntervals(intervals);
    if (!sorted.length) {
      topSpacer.style.height = lineCount ? lineCount * M.EST_LINE_PX + "px" : "0px";
      bottomSpacer.style.height = "0px";
      return;
    }
    const first = sorted[0].start;
    const last = sorted[sorted.length - 1].end;
    topSpacer.style.height = first > 1 ? (first - 1) * M.EST_LINE_PX + "px" : "0px";
    // Gaps between intervals: insert gap spacers as elements? For simplicity estimate only
    // outer spacers; internal gaps get estimated filler nodes.
    ensureGapFillers(sorted);
    bottomSpacer.style.height = last < lineCount ? (lineCount - last) * M.EST_LINE_PX + "px" : "0px";
  }

  function ensureGapFillers(sorted) {
    const existing = sectionsHost.querySelectorAll(".virt-gap");
    existing.forEach((n) => n.remove());
    for (let i = 1; i < sorted.length; i++) {
      const prev = sorted[i - 1];
      const next = sorted[i];
      if (next.start <= prev.end + 1) continue;
      const gapStart = prev.end + 1;
      const gapEnd = next.start - 1;
      const gapLines = gapEnd - gapStart + 1;
      const gap = document.createElement("div");
      gap.className = "virt-gap virt-spacer virt-gap-loading";
      gap.dataset.after = String(prev.end);
      gap.dataset.gapStart = String(gapStart);
      gap.dataset.gapEnd = String(gapEnd);
      gap.style.height = Math.max(gapLines * M.EST_LINE_PX, 28) + "px";
      gap.setAttribute("role", "status");
      gap.setAttribute("aria-label", "Loading lines " + gapStart + "–" + gapEnd);
      const label = document.createElement("div");
      label.className = "virt-gap-label muted";
      label.textContent = "Loading lines " + gapStart + "–" + gapEnd + "…";
      gap.appendChild(label);
      const nextEl = intervals.find((x) => x.start === next.start)?.el;
      if (nextEl) sectionsHost.insertBefore(gap, nextEl);
      else sectionsHost.appendChild(gap);
    }
  }

  function renderIntervalEl(iv) {
    const wrap = document.createElement("div");
    wrap.className = "virt-section";
    wrap.dataset.start = String(iv.start);
    wrap.dataset.end = String(iv.end);
    const q = searchMode ? "" : lastQuery;
    const cs = caseEl.checked;
    let html = "";
    for (let i = 0; i < iv.lines.length; i++) {
      const ln = iv.start + i;
      const cls = ln === focusLine ? "logline focus-line" : "logline";
      html +=
        '<div class="' +
        cls +
        '" data-line="' +
        ln +
        '"><span class="ln">' +
        ln +
        '</span><span class="tx">' +
        highlight(iv.lines[i], q, cs) +
        "</span></div>";
    }
    wrap.innerHTML = html;
    return wrap;
  }

  function insertInterval(iv) {
    // Drop overlapping intervals (partial buckets / refresh)
    const kept = [];
    for (const ex of intervals) {
      if (ex.end < iv.start || ex.start > iv.end) {
        kept.push(ex);
      } else if (ex.el && ex.el.parentNode) {
        ex.el.parentNode.removeChild(ex.el);
      }
    }
    intervals = kept;
    const el = renderIntervalEl(iv);
    iv.el = el;
    const after = intervals.filter((x) => x.start > iv.start).sort((a, b) => a.start - b.start)[0];
    if (after && after.el) sectionsHost.insertBefore(el, after.el);
    else sectionsHost.appendChild(el);
    iv.height = el.offsetHeight || (iv.end - iv.start + 1) * M.EST_LINE_PX;
    intervals.push(iv);
    intervals = M.sortedIntervals(intervals);
  }

  function remeasure() {
    for (const iv of intervals) {
      if (iv.el) iv.height = iv.el.offsetHeight || iv.height;
    }
  }

  function pruneToBounds(anchorLine) {
    const anchor = anchorLine || (M.loadedExtent(intervals) || {}).first || 1;
    const vis = visibleLineRange();
    const protectLo = Math.max(1, (vis ? vis.start : anchor) - M.SECTION);
    const protectHi = Math.min(lineCount || Number.MAX_SAFE_INTEGER, (vis ? vis.end : anchor) + M.SECTION);
    while (intervals.length > MAX_SECTIONS || cacheBytes() > MAX_CACHE_BYTES) {
      if (intervals.length <= 1) break;
      const candidates = intervals.filter(
        (iv) => iv.end < protectLo || iv.start > protectHi
      );
      if (!candidates.length) break;
      let drop = candidates[0];
      let best = -1;
      for (const iv of candidates) {
        const dist = Math.min(Math.abs(iv.start - anchor), Math.abs(iv.end - anchor));
        if (dist > best) {
          best = dist;
          drop = iv;
        }
      }
      if (drop.el && drop.el.parentNode) drop.el.parentNode.removeChild(drop.el);
      intervals = intervals.filter((iv) => iv !== drop);
    }
  }

  function inflightKey(start, end) {
    return start + ":" + end;
  }

  function overlapsInflight(start, end) {
    for (const [k] of inflight) {
      const parts = k.split(":").map(Number);
      if (!(end < parts[0] || start > parts[1])) return true;
    }
    return false;
  }

  function enqueueDemand(items) {
    for (const t of items || []) {
      if (!t || !t.line) continue;
      const end =
        t.rangeEnd ||
        Math.min(t.line + M.SECTION - 1, lineCount || t.line + M.SECTION - 1);
      if (lineCount && t.line > lineCount) continue;
      if (M.coversLine(intervals, t.line) && (!t.rangeEnd || M.coversLine(intervals, end))) {
        let covered = true;
        for (let ln = t.line; ln <= end; ln++) {
          if (!M.coversLine(intervals, ln)) {
            covered = false;
            break;
          }
        }
        if (covered) continue;
      }
      const zp = inflightKey(t.line, end);
      if (zeroProgress.has(zp)) continue;
      const dup = pendingDemand.some((p) => p.line === t.line && (p.rangeEnd || 0) === end);
      if (!dup) {
        pendingDemand.push({
          reason: t.reason || "demand",
          line: t.line,
          rangeEnd: end,
        });
      }
    }
  }

  function drainDemandQueue() {
    const deferred = [];
    while (inflight.size < MAX_INFLIGHT && pendingDemand.length) {
      const next = pendingDemand.shift();
      if (!next) break;
      const end = next.rangeEnd || next.line;
      let covered = true;
      for (let ln = next.line; ln <= end; ln++) {
        if (!M.coversLine(intervals, ln)) {
          covered = false;
          break;
        }
      }
      if (covered) continue;
      if (zeroProgress.has(inflightKey(next.line, end))) continue;
      if (overlapsInflight(next.line, end)) {
        deferred.push(next);
        continue;
      }
      // Fire without awaiting; settlement schedules another pass.
      fetchInterval(next.line, { reason: next.reason || "queued", rangeEnd: end });
    }
    // Retain unsatisfied demand (still overlapping or slot-blocked) without dropping it.
    if (deferred.length) pendingDemand = deferred.concat(pendingDemand);
  }

  async function fetchInterval(startLine, opts) {
    opts = opts || {};
    if (!lineCount && !opts.allowUnknown) {
      // still allow first fetch
    }
    const hintEnd =
      opts.rangeEnd != null
        ? opts.rangeEnd
        : startLine + M.SECTION - 1;
    const win = M.requestWindow(startLine, lineCount || hintEnd);
    // Skip if fully covered
    let covered = true;
    for (let ln = win.start; ln <= win.end; ln++) {
      if (!M.coversLine(intervals, ln)) {
        covered = false;
        break;
      }
    }
    if (covered && !opts.force) return null;

    // Shrink request to first missing..end
    const missing = M.nextMissingFrom(intervals, win.start, lineCount || win.end);
    if (missing == null && lineCount) return null;
    const reqStart = missing != null ? missing : win.start;
    const reqEnd = Math.min(lineCount || win.end, reqStart + M.SECTION - 1);
    const key = inflightKey(reqStart, reqEnd);

    if (zeroProgress.has(key) && !opts.force) return null;

    if (inflight.has(key) && !opts.force) {
      try {
        return await inflight.get(key).promise;
      } catch (_) {
        return null;
      }
    }
    if (overlapsInflight(reqStart, reqEnd) && !opts.force && !inflight.has(key)) {
      enqueueDemand([{ line: reqStart, rangeEnd: reqEnd, reason: opts.reason || "overlap" }]);
      return null;
    }
    if (inflight.size >= MAX_INFLIGHT && !opts.force) {
      enqueueDemand([{ line: reqStart, rangeEnd: reqEnd, reason: opts.reason || "queued" }]);
      return null;
    }

    const gen = loadGen;
    const myJob = job;
    const mySource = source;
    const mySearchGen = searchGen;
    const token = ++inflightToken;
    const ac = new AbortController();
    let settle;
    const promise = new Promise((resolve) => {
      settle = resolve;
    });
    const entry = { token, controller: ac, promise };
    inflight.set(key, entry);

    setStatus("Loading lines " + reqStart + "–" + reqEnd + "…");
    try {
      const r = await fetch(
        "/api/jobs/" +
          encodeURIComponent(job) +
          "/sources/" +
          encodeURIComponent(source) +
          "/context?start=" +
          reqStart +
          "&end=" +
          reqEnd,
        { headers: headers(), signal: ac.signal }
      );
      if (!r.ok) throw new Error("HTTP " + r.status + " " + (await r.text()));
      const data = await r.json();
      if (gen !== loadGen || myJob !== job || mySource !== source || mySearchGen !== searchGen) {
        settle(null);
        return null;
      }
      if (typeof data.line_count === "number" && data.line_count > 0) lineCount = data.line_count;
      if (data.path) {
        sourcePath = data.path;
        updateIdentity();
      }
      if (data.coverage) {
        coverage = data.coverage;
        const limited = coverage === "partial" || coverage === "truncated";
        coverageEl.textContent =
          "Source coverage: " +
          coverage +
          (limited ? " (incomplete retained evidence)" : "") +
          (data.checkpoint_used
            ? " · checkpoint L" + data.checkpoint_line
            : data.checkpoints_available
              ? " · " + data.checkpoints_available + " checkpoints"
              : "");
      }
      const lines = data.lines || [];
      const actualStart = Number(data.start) || reqStart;
      const actualEnd = lines.length
        ? Number(data.end) || actualStart + lines.length - 1
        : actualStart - 1;
      if (!lines.length) {
        zeroProgress.add(key);
        if (data.eof || reqStart > lineCount) {
          setStatus("End of retained source (line " + lineCount + ").");
        } else {
          setStatus("No lines returned for " + reqStart + "–" + reqEnd + ".", () => {
            zeroProgress.delete(key);
            fetchInterval(reqStart, { force: true }).then(() => schedulePrefetch("retry"));
          });
        }
        settle(null);
        return null;
      }
      let bytes = 0;
      for (const ln of lines) bytes += ln.length + 1;
      if (gen !== loadGen || myJob !== job || mySource !== source) {
        settle(null);
        return null;
      }
      const hadContent = intervals.length > 0;
      const anchor = hadContent && !opts.jump ? captureAnchor() : null;
      mutating = true;
      const iv = {
        start: actualStart,
        end: actualEnd,
        lines,
        el: null,
        height: 0,
        bytes,
        eof: !!data.eof || actualEnd >= lineCount,
      };
      insertInterval(iv);
      remeasure();
      updateSpacers();
      pruneToBounds((anchor && anchor.line) || actualStart);
      updateSpacers();
      if (anchor) restoreAnchor(anchor);
      mutating = false;
      if (gen !== loadGen) {
        settle(null);
        return null;
      }
      if (iv.eof) setStatus("End of retained source (line " + lineCount + ").");
      else setStatus("");
      updateRangeNote();

      // Continue partial bucket immediately when byte-limited
      const cont = M.nextAfterPartial(reqStart, actualEnd, lineCount);
      if (cont != null && cont <= reqEnd && !M.coversLine(intervals, cont)) {
        settle(iv);
        enqueueDemand([{ line: cont, rangeEnd: reqEnd, reason: "byte-partial" }]);
        schedulePrefetch("byte-partial");
        return iv;
      }
      settle(iv);
      return iv;
    } catch (e) {
      if (e.name === "AbortError") {
        settle(null);
        return null;
      }
      if (gen === loadGen) {
        setStatus(
          "Failed to load lines " + reqStart + "–" + reqEnd + ": " + (e.message || e),
          () => {
            setStatus("");
            zeroProgress.delete(key);
            enqueueDemand([{ line: reqStart, rangeEnd: reqEnd, reason: "retry" }]);
            schedulePrefetch("retry");
          }
        );
      }
      settle(null);
      return null;
    } finally {
      const cur = inflight.get(key);
      if (cur && cur.token === token) inflight.delete(key);
      // Wake demand queue / re-cover viewport without requiring another scroll.
      if (gen === loadGen) schedulePrefetch("after-fetch");
    }
  }

  function clearBrowseCache() {
    loadGen++;
    for (const ent of inflight.values()) {
      try {
        ent.controller.abort();
      } catch (_) {}
    }
    inflight.clear();
    pendingDemand = [];
    zeroProgress.clear();
    scrollDir = 0;
    lastScrollTop = 0;
    intervals = [];
    sectionsHost.innerHTML = "";
    topSpacer.style.height = "0px";
    bottomSpacer.style.height = "0px";
  }

  async function jumpToLine(line, align) {
    searchMode = false;
    jumpLock = true;
    document.getElementById("btn-prev").style.display = "none";
    document.getElementById("btn-next").style.display = "none";
    applyWrap();
    gotoEl.value = String(line);
    clearBrowseCache();
    setStatus("Loading…");
    try {
      // Prefer a SECTION-sized window ending at the target when near EOF.
      const loadStart =
        lineCount && line >= lineCount - M.SECTION + 1
          ? Math.max(1, lineCount - M.SECTION + 1)
          : Math.max(1, line - Math.floor(M.SECTION / 2));
      const sec = await fetchInterval(loadStart, { allowUnknown: true, force: true, jump: true });
      if (!sec) return;
      line = M.clamp(line, 1, lineCount || line);
      gotoEl.value = String(line);
      if (!M.coversLine(intervals, line)) {
        const again = await fetchInterval(line, { force: true, jump: true });
        if (!again && !M.coversLine(intervals, line)) return;
      }
      // Bounded overscan neighbors
      const next = M.nextMissingFrom(intervals, line + 1, lineCount);
      const prev = M.prevMissingFrom(intervals, line - 1, lineCount);
      if (prev != null) await fetchInterval(Math.max(1, prev - M.SECTION + 1), { jump: true });
      if (next != null) await fetchInterval(next, { jump: true });

      await new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)));
      updateSpacers();
      remeasure();
      if (align === "start") scroller.scrollTop = 0;
      else if (align === "end") {
        scroller.scrollTop = Math.max(0, scroller.scrollHeight - scroller.clientHeight);
      } else {
        const row = sectionsHost.querySelector('.logline[data-line="' + line + '"]');
        if (row) row.scrollIntoView({ block: "center" });
        else scroller.scrollTop = estimateScrollForLine(line);
      }
      updateRangeNote();
    } finally {
      jumpLock = false;
      if (intervals.length) schedulePrefetch();
    }
  }

  function contentBounds() {
    const sorted = M.sortedIntervals(intervals);
    if (!sorted.length) return { top: 0, bottom: 0 };
    let top = topSpacer.offsetHeight;
    let bottom = top;
    for (const iv of sorted) {
      bottom += iv.el ? iv.el.offsetHeight : iv.height;
    }
    // include gap fillers between
    sectionsHost.querySelectorAll(".virt-gap").forEach((g) => {
      bottom += g.offsetHeight;
    });
    return { top, bottom };
  }

  function schedulePrefetch(reason) {
    if (searchMode) return;
    needReschedule = true;
    if (prefetchBusy) return;
    prefetchBusy = true;
    const my = ++scheduleTok;
    const run = () => {
      prefetchBusy = false;
      if (my !== scheduleTok) return;
      if (!needReschedule) return;
      needReschedule = false;
      if (mutating || jumpLock) {
        schedulePrefetch("deferred-" + (reason || "lock"));
        return;
      }
      maybePrefetch(reason || "schedule");
    };
    if (typeof requestAnimationFrame === "function") requestAnimationFrame(run);
    else setTimeout(run, 0);
  }

  async function maybePrefetch() {
    if (searchMode || mutating || jumpLock || !lineCount) return;

    const viewportTop = scroller.scrollTop;
    const viewportBottom = scroller.scrollTop + scroller.clientHeight;
    const bounds = contentBounds();
    const ivSnap = intervals.map((iv) => ({
      start: iv.start,
      end: iv.end,
      height: iv.height,
    }));

    // Primary: viewport-demand covers internal gaps and visible missing ranges.
    const targets = M.viewportDemand({
      scrollTop: viewportTop,
      clientHeight: scroller.clientHeight,
      intervals: ivSnap,
      lineCount,
      overscanPx: PREFETCH_PX,
      direction: typeof scrollDir === "number" ? scrollDir : 0,
    });

    // Outer-edge / spacer mapped window when thumb is outside loaded content.
    const inTopSpacer = viewportTop < bounds.top - 4;
    const inBottomSpacer = viewportBottom > bounds.bottom + 4;
    if (
      inTopSpacer ||
      inBottomSpacer ||
      viewportTop > bounds.bottom ||
      viewportBottom < bounds.top
    ) {
      const target = M.lineAtScrollOffset(
        scroller.scrollTop,
        ivSnap,
        lineCount,
        scroller.clientHeight / 2
      );
      if (!M.coversLine(intervals, target) && !targets.some((t) => t.line === target)) {
        targets.push({ reason: "thumb-map", line: target, rangeEnd: target });
      }
    }

    // Boundary hints when demand empty but near loaded edges.
    if (!targets.length) {
      const edge = M.prefetchTargets({
        viewportTop,
        viewportBottom,
        contentTop: bounds.top,
        contentBottom: bounds.bottom,
        prefetchPx: PREFETCH_PX,
        intervals: ivSnap,
        lineCount,
        scrollTop: viewportTop,
        clientHeight: scroller.clientHeight,
        direction: typeof scrollDir === "number" ? scrollDir : 0,
      });
      for (const t of edge) targets.push(t);
    }

    if (typeof enqueueDemand === "function") enqueueDemand(targets);

    // Call fetchInterval for each target so probes exercising maybePrefetch observe requests.
    // When concurrency is full, fetchInterval retains demand instead of dropping it.
    for (const t of targets) {
      await fetchInterval(t.line, { reason: t.reason, rangeEnd: t.rangeEnd });
    }

    if (typeof drainDemandQueue === "function") drainDemandQueue();
  }

  scroller.addEventListener("scroll", () => {
    if (mutating || searchMode) return;
    const st = scroller.scrollTop;
    if (st > lastScrollTop + 1) scrollDir = 1;
    else if (st < lastScrollTop - 1) scrollDir = -1;
    lastScrollTop = st;
    updateRangeNote();
    schedulePrefetch("scroll");
  });

  window.addEventListener("resize", onChromeLayoutChange);
  if (chromeEl && typeof ResizeObserver !== "undefined") {
    const ro = new ResizeObserver(() => onChromeLayoutChange());
    ro.observe(chromeEl);
  }
  document.querySelectorAll("#viewer-chrome details").forEach((d) => {
    d.addEventListener("toggle", onChromeLayoutChange);
  });

  async function runSearch() {
    const q = queryEl.value;
    if (!q) {
      countEl.textContent = "Enter a query to search the entire retained source.";
      return;
    }
    const gen = ++searchGen;
    lastQuery = q;
    searchMode = true;
    clearBrowseCache();
    setStatus("Searching…");
    document.getElementById("btn-prev").style.display = "";
    document.getElementById("btn-next").style.display = "";

    const r = await fetch("/api/jobs/" + encodeURIComponent(job) + "/searches", {
      method: "POST",
      headers: Object.assign({ "Content-Type": "application/json" }, headers()),
      body: JSON.stringify({
        source_id: source,
        query: q,
        case_sensitive: caseEl.checked,
      }),
    });
    if (gen !== searchGen) return;
    if (!r.ok) {
      setStatus("Search failed: HTTP " + r.status + " " + (await r.text()), () => runSearch());
      return;
    }
    const started = await r.json();
    if (!started.id) {
      setStatus("Search contract error: missing search id", () => runSearch());
      return;
    }
    searchId = started.id;
    searchOffset = 0;
    // Prefer start payload counts when present (avoids blank UI if page GET races).
    if (typeof started.matching_lines_found === "number") {
      searchMeta = {
        matching_lines_found: started.matching_lines_found,
        keyword_occurrences_found: started.keyword_occurrences_found || 0,
        count_is_final: !!started.count_is_final,
        scope_complete: !!started.scope_complete,
        error: started.error || "",
      };
    }
    await loadSearchPage(gen);
  }

  async function loadSearchPage(gen) {
    if (!searchId) return;
    const r = await fetch(
      "/api/jobs/" +
        encodeURIComponent(job) +
        "/searches/" +
        encodeURIComponent(searchId) +
        "?offset=" +
        searchOffset +
        "&limit=" +
        PAGE_SIZE,
      { headers: headers() }
    );
    if (gen !== searchGen) return;
    if (!r.ok) {
      const detail = await r.text();
      setStatus(
        "Search page failed: HTTP " + r.status + (detail ? " " + detail : ""),
        () => loadSearchPage(gen)
      );
      return;
    }
    const data = await r.json();
    if (!data.search || !Array.isArray(data.items)) {
      setStatus("Search contract error: expected search + items", () => loadSearchPage(gen));
      return;
    }
    searchMeta = data.search;
    searchPageHits = data.items;
    const status = searchMeta.count_is_final ? "final" : "incomplete";
    countEl.textContent =
      "Matching lines: " +
      searchMeta.matching_lines_found +
      " · Occurrences: " +
      searchMeta.keyword_occurrences_found +
      " · Status: " +
      status +
      (searchMeta.scope_complete ? "" : " (source incomplete)") +
      (searchMeta.error ? " · " + searchMeta.error : "");
    renderSearchPage();
    setStatus(searchMeta.count_is_final ? "Search complete." : "Search results (counts may still be partial).");
  }

  function renderSearchPage() {
    sectionsHost.innerHTML = "";
    topSpacer.style.height = "0px";
    bottomSpacer.style.height = "0px";
    if (!searchPageHits.length) {
      sectionsHost.innerHTML = '<p class="muted">No matching lines on this page.</p>';
      rangeEl.textContent = "Search page empty";
      return;
    }
    if (matchingOnlyEl.checked) {
      const wrap = document.createElement("div");
      wrap.className = "virt-section search-results";
      let html = "";
      for (const h of searchPageHits) {
        html +=
          '<div class="logline" data-line="' +
          h.line +
          '"><span class="ln">' +
          h.line +
          '</span><span class="tx">' +
          highlight(h.text, lastQuery, caseEl.checked) +
          "</span></div>";
      }
      wrap.innerHTML = html;
      sectionsHost.appendChild(wrap);
      rangeEl.textContent =
        "Search hits " +
        (searchOffset + 1) +
        "–" +
        (searchOffset + searchPageHits.length) +
        " (page size " +
        PAGE_SIZE +
        "; server-paged, not all text kept in memory)";
      scroller.scrollTop = 0;
      return;
    }
    // Context mode: jump continuous viewer to first hit with highlight query retained
    searchMode = false;
    const first = searchPageHits[0].line;
    jumpToLine(first).then(() => {
      lastQuery = queryEl.value;
      // re-highlight by re-rendering intervals
      const anchor = captureAnchor();
      for (const iv of intervals) {
        const neu = renderIntervalEl(iv);
        iv.el.replaceWith(neu);
        iv.el = neu;
        iv.height = neu.offsetHeight;
      }
      updateSpacers();
      restoreAnchor(anchor);
      rangeEl.textContent =
        "Search context around line " +
        first +
        " · " +
        searchPageHits.length +
        " hits on this page · use Next for more matches";
      document.getElementById("btn-prev").style.display = "";
      document.getElementById("btn-next").style.display = "";
      searchMode = false;
    });
  }

  function clearSearchToBrowse() {
    searchGen++;
    searchId = "";
    searchMeta = null;
    searchPageHits = [];
    searchOffset = 0;
    searchMode = false;
    lastQuery = "";
    countEl.textContent = "";
    document.getElementById("btn-prev").style.display = "none";
    document.getElementById("btn-next").style.display = "none";
    jumpToLine(focusLine);
  }

  document.getElementById("btn-search").onclick = () => runSearch();
  queryEl.addEventListener("keydown", (e) => {
    if (e.key === "Enter") {
      e.preventDefault();
      runSearch();
    }
  });
  document.getElementById("btn-clear").onclick = () => {
    queryEl.value = "";
    clearSearchToBrowse();
  };
  matchingOnlyEl.addEventListener("change", () => {
    if (searchId) loadSearchPage(searchGen);
  });
  document.getElementById("btn-prev").onclick = async () => {
    searchOffset = Math.max(0, searchOffset - PAGE_SIZE);
    await loadSearchPage(searchGen);
  };
  document.getElementById("btn-next").onclick = async () => {
    if (!searchMeta) return;
    if (searchOffset + PAGE_SIZE < searchMeta.matching_lines_found) {
      searchOffset += PAGE_SIZE;
      await loadSearchPage(searchGen);
    }
  };
  document.getElementById("btn-start").onclick = () => jumpToLine(1, "start");
  document.getElementById("btn-end").onclick = () => jumpToLine(lineCount || focusLine, "end");
  document.getElementById("btn-goto").onclick = () => jumpToLine(Number(gotoEl.value || "1") || 1);
  gotoEl.addEventListener("keydown", (e) => {
    if (e.key === "Enter") {
      e.preventDefault();
      document.getElementById("btn-goto").click();
    }
  });
  document.getElementById("btn-nav-prev").onclick = () => {
    const extent = M.loadedExtent(intervals);
    const anchor = extent ? extent.first : focusLine;
    jumpToLine(Math.max(1, anchor - M.SECTION));
  };
  document.getElementById("btn-nav-next").onclick = () => {
    const extent = M.loadedExtent(intervals);
    const next = extent ? extent.last + 1 : focusLine + M.SECTION;
    jumpToLine(Math.min(lineCount || next, next));
  };
  wrapEl.addEventListener("change", () => {
    applyWrap();
    if (searchMode && matchingOnlyEl.checked) {
      renderSearchPage();
      return;
    }
    const anchor = captureAnchor();
    for (const iv of intervals) {
      const neu = renderIntervalEl(iv);
      iv.el.replaceWith(neu);
      iv.el = neu;
      iv.height = neu.offsetHeight;
    }
    updateSpacers();
    restoreAnchor(anchor);
    updateRangeNote();
    schedulePrefetch("wrap");
  });

  updateIdentity();
  applyWrap();
  gotoEl.value = String(focusLine);
  jumpToLine(focusLine);
})();
