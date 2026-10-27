(() => {
  const state = {
    session: "",
    viewedJobId: null,
    activeJobId: null,
    progressWatchId: null,
    viewGen: 0,
    viewTab: "overview",
    viewedMeta: { name: "", product: "", state: "" },
    needsSetup: true,
    watching: false,
    terminal: false,
    progressES: null,
    get jobId() { return this.viewedJobId; },
    set jobId(v) { this.viewedJobId = v; },
  };
  const hlState = { sort: "newest", range: "24h", includeUndated: false, offset: 0 };
  const fileListState = { kind: "log", q: "", offset: 0, timer: null, reqToken: 0, abort: null };
  const perJobUI = {};

  const $ = (sel) => document.querySelector(sel);
  const $$ = (sel) => [...document.querySelectorAll(sel)];

  function api(path, opts = {}) {
    const headers = Object.assign({ "X-QA-Session": state.session }, opts.headers || {});
    return fetch(path, { ...opts, headers }).then(async (r) => {
      const ct = r.headers.get("content-type") || "";
      const isJSON = ct.includes("application/json");
      const body = isJSON ? await r.json() : await r.text();
      if (!r.ok) {
        const err = new Error((body && body.error) || body || r.statusText);
        err.payload = typeof body === "object" ? body : null;
        err.status = r.status;
        throw err;
      }
      return body;
    });
  }


  function bumpViewGen() { state.viewGen += 1; return state.viewGen; }
  function stillViewing(jobId, gen, tab) {
    if (state.viewedJobId !== jobId || state.viewGen !== gen) return false;
    if (tab && state.viewTab && state.viewTab !== tab) return false;
    return true;
  }
  function reportDiag(code, extra) {
    try {
      const body = Object.assign({ code, job_id: state.viewedJobId || "", tab: state.viewTab || "" }, extra || {});
      fetch("/api/diagnostics/client", {
        method: "POST",
        headers: { "Content-Type": "application/json", "X-QA-Session": state.session },
        body: JSON.stringify(body),
      }).catch(() => {});
    } catch (_) {}
  }
  function updateViewingIdentity() {
    const el = $("#viewing-identity");
    if (!el) return;
    const m = state.viewedMeta || {};
    el.textContent = state.viewedJobId
      ? `Viewing: ${m.name || state.viewedJobId} · ${m.product || "—"} · ${m.state || "saved"}`
      : "";
    if (!state.viewedJobId || $("#view-results")?.classList.contains("hidden")) {
      el.classList.add("hidden");
      return;
    }
    el.classList.remove("hidden");
  }
  function selectViewedJob(id, meta) {
    if (state.viewedJobId && state.viewedJobId !== id) {
      perJobUI[state.viewedJobId] = { fileQ: fileListState.q, fileOffset: fileListState.offset, hlOffset: hlState.offset, hlSort: hlState.sort, hlRange: hlState.range };
      clearTimeout(fileListState.timer);
      if (fileListState.abort) try { fileListState.abort.abort(); } catch (_) {}
    }
    state.viewedJobId = id;
    state.viewedMeta = meta || state.viewedMeta || {};
    const saved = perJobUI[id];
    if (saved) {
      fileListState.q = saved.fileQ || ""; fileListState.offset = saved.fileOffset || 0;
      hlState.offset = saved.hlOffset || 0; hlState.sort = saved.hlSort || "newest"; hlState.range = saved.hlRange || "24h";
    } else { fileListState.q = ""; fileListState.offset = 0; hlState.offset = 0; }
    bumpViewGen(); updateViewingIdentity();
  }
  function closeProgressWatch() {
    if (state.progressES) { try { state.progressES.close(); } catch (_) {} state.progressES = null; }
    state.watching = false; state.progressWatchId = null;
  }

  function show(id) {
    $$(".view").forEach((el) => el.classList.add("hidden"));
    const el = document.getElementById(id);
    if (el) el.classList.remove("hidden");
    updateViewingIdentity();
    refreshActiveBanner();
  }

  function esc(s) {
    return String(s ?? "")
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;");
  }

  function isRunningState(st) {
    return st && !["completed", "partial", "cancelled", "failed", "interrupted"].includes(st);
  }

  async function refreshActiveBanner() {
    try {
      const a = await api("/api/active-job");
      const banner = $("#active-banner");
      const resumeUpload = $("#btn-upload-resume");
      const title = $("#active-banner-title");
      if (a.active && a.id) {
        state.activeJobId = a.id;
        const label = a.original_name || a.id;
        const running = isRunningState(a.state);
        state.terminal = !running;
        if (title) title.textContent = running ? "Background analysis" : "Background analysis finished";
        $("#active-banner-text").textContent = running
          ? `${label} · ${a.state || "running"} / ${a.stage || "…"} · ${a.percent || 0}%`
          : `${label} finished (${a.state}). Viewing another snapshot is unchanged.`;
        const onProgress = document.getElementById("view-progress")?.classList.contains("hidden") === false;
        if (running) {
          // Hide only when the user is already watching THIS job's progress screen.
          // Always show when a different snapshot is being viewed.
          const watchingThis = onProgress && state.viewedJobId === a.id;
          if (watchingThis) banner.classList.add("hidden");
          else banner.classList.remove("hidden");
          resumeUpload.classList.remove("hidden");
          $("#btn-upload").disabled = true;
        } else {
          banner.classList.add("hidden");
          resumeUpload.classList.add("hidden");
          $("#btn-upload").disabled = false;
        }
      } else {
        state.activeJobId = null;
        banner.classList.add("hidden");
        resumeUpload.classList.add("hidden");
        $("#btn-upload").disabled = false;
        if (!state.watching) state.terminal = true;
      }
    } catch (_) {}
  }

  function resumeActive() {
    const id = state.activeJobId;
    if (!id) return;
    selectViewedJob(id, { name: "active analysis", product: "", state: "processing" });
    show("view-progress");
    watchProgress(id);
  }

  async function init() {
    const q = new URLSearchParams(location.search);
    const boot = await fetch("/api/session").then((r) => r.json());
    state.session = q.get("s") || boot.session;
    state.needsSetup = boot.needs_setup;
    $("#brand").textContent = boot.product || "QuickAnalyser";
    $("#footer-meta").textContent = `${boot.product} · ${boot.signature} · ${boot.version}`;
    await refreshStorage();
    bind();
    const about = await api("/api/about");
    $("#about-body").textContent = JSON.stringify(about, null, 2);

    const active = await api("/api/active-job").catch(() => ({ active: false }));
    if (state.needsSetup) {
      show("view-storage");
    } else if (active.active && active.id && isRunningState(active.state)) {
      state.activeJobId = active.id;
      selectViewedJob(active.id, { name: active.original_name || active.id, product: "", state: active.state });
      show("view-progress");
      watchProgress(active.id);
    } else {
      show("view-upload");
      await refreshActiveBanner();
    }
    setInterval(refreshActiveBanner, 3000);
  }

  async function refreshStorage() {
    const info = await api("/api/storage");
    state.needsSetup = !!info.needs_setup;
    const free = info.free_bytes ? formatBytes(info.free_bytes) : "unknown";
    const retained = info.retained_bytes != null ? formatBytes(info.retained_bytes) : "—";
    const n = info.retained_analyses != null ? info.retained_analyses : "—";
    let label = `Storage: ${info.mode} · ${info.root} · free ${free} · retained ${retained} (${n} analyses)`;
    if (info.temporary_or_test_root) {
      label += " · TEMP/TEST ROOT — not durable retention";
    }
    $("#storage-indicator").textContent = label;
    $("#storage-root-display").textContent = info.root || "";
    $("#storage-free").textContent = free + (info.temporary_or_test_root ? " · temporary/test root" : "");
  }

  async function refreshDiagnostics() {
    const el = $("#diag-status");
    if (!el) return;
    try {
      const st = await api("/api/diagnostics");
      el.textContent = JSON.stringify(st, null, 2);
      if ($("#diag-msg") && !($("#diag-msg").textContent || "").includes("Bundle")) {
        $("#diag-msg").textContent = st.health === "ok" ? "Diagnostics healthy." : ("Health: " + (st.health || "unknown") + (st.health_error ? " · " + st.health_error : ""));
      }
    } catch (e) {
      el.textContent = e.message || String(e);
    }
  }

  function formatBytes(n) {
    if (n < 1024) return `${n} B`;
    const u = ["KiB", "MiB", "GiB", "TiB"];
    let v = n;
    let i = -1;
    do {
      v /= 1024;
      i++;
    } while (v >= 1024 && i < u.length - 1);
    return `${v.toFixed(1)} ${u[i]}`;
  }

  function bind() {
    $("#nav-upload").onclick = async () => {
      const a = await api("/api/active-job").catch(() => ({ active: false }));
      if (a.active && a.id && isRunningState(a.state)) {
        selectViewedJob(a.id, { name: a.original_name || a.id, product: "", state: a.state });
        show("view-progress");
        watchProgress(a.id);
        return;
      }
      show("view-upload");
      refreshActiveBanner();
    };
    $("#nav-previous").onclick = () => {
      show("view-previous");
      loadPrevious();
    };
    $("#nav-settings").onclick = () => {
      show("view-storage");
      refreshStorage();
      refreshActiveBanner();
      // Settings also exposes Diagnostics below storage controls when present.
      const diag = $("#view-diagnostics");
      if (diag) {
        // Keep storage as primary settings; open diagnostics via dedicated control if added.
      }
      refreshDiagnostics();
    };
    $("#nav-about").onclick = () => {
      show("view-about");
      refreshActiveBanner();
    };
    const navDiag = $("#nav-diagnostics");
    if (navDiag) {
      navDiag.onclick = () => {
        show("view-diagnostics");
        refreshDiagnostics();
        refreshActiveBanner();
      };
    }
    const btnDiagRefresh = $("#btn-diag-refresh");
    if (btnDiagRefresh) btnDiagRefresh.onclick = () => refreshDiagnostics();
    const btnDiagDebug = $("#btn-diag-debug");
    if (btnDiagDebug) btnDiagDebug.onclick = async () => {
      await api("/api/diagnostics", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ action: "enable_debug" }) });
      await refreshDiagnostics();
    };
    const btnDiagInfo = $("#btn-diag-info");
    if (btnDiagInfo) btnDiagInfo.onclick = async () => {
      await api("/api/diagnostics", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ action: "disable_debug" }) });
      await refreshDiagnostics();
    };
    const btnDiagBundle = $("#btn-diag-bundle");
    if (btnDiagBundle) btnDiagBundle.onclick = async () => {
      try {
        const res = await api("/api/diagnostics/bundle", { method: "POST" });
        $("#diag-msg").textContent = (res.message || "Bundle created") + (res.path ? " · " + res.path : "");
      } catch (e) {
        $("#diag-msg").textContent = e.message || String(e);
      }
    };
    $("#btn-quit").onclick = () => api("/api/quit", { method: "POST" });
    $("#btn-resume-active").onclick = resumeActive;
    $("#btn-upload-resume").onclick = resumeActive;

    $$("input[name=storage-mode]").forEach((el) => {
      el.onchange = () => {
        $("#custom-path-row").classList.toggle("hidden", el.value !== "custom" || !el.checked);
      };
    });

    $("#btn-use-storage").onclick = async () => {
      const mode = $$("input[name=storage-mode]").find((x) => x.checked)?.value || "default";
      const path = $("#custom-path").value.trim();
      try {
        await api("/api/storage", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ mode, path }),
        });
        state.needsSetup = false;
        await refreshStorage();
        show("view-upload");
      } catch (e) {
        alert(e.message);
      }
    };

    $("#file-input").onchange = () => {
      const f = $("#file-input").files?.[0];
      $("#selected-name").textContent = f ? f.name : "No file selected";
    };

    $("#btn-upload").onclick = () => upload(false);
    $("#btn-cancel").onclick = async () => {
      const id = state.progressWatchId || state.activeJobId;
      if (!id) return;
      await api(`/api/jobs/${id}/cancel`, { method: "POST" });
    };

    $$(".result-tab").forEach((btn) => {
      btn.onclick = () => {
        $$(".result-tab").forEach((b) => {
          b.classList.remove("active");
          b.setAttribute("aria-selected", "false");
        });
        btn.classList.add("active");
        btn.setAttribute("aria-selected", "true");
        state.viewTab = btn.dataset.tab || "overview";
        bumpViewGen();
        reportDiag("ui.tab_changed", {});
        loadResultTab(state.viewTab);
      };
    });
  }

  function clearWorkspaceChrome() {
    const controls = $("#workspace-controls");
    const status = $("#workspace-status");
    if (controls) controls.innerHTML = "";
    if (status) status.innerHTML = "";
  }

  function setWorkspaceStatus(html) {
    const status = $("#workspace-status");
    if (status) status.innerHTML = html || "";
  }

  function prepareChromeForTab(tabName) {
    const controls = $("#workspace-controls");
    if (tabName === "overview" || tabName === "coverage") {
      clearWorkspaceChrome();
      return;
    }
    if (tabName === "highlights") {
      if (controls && !controls.querySelector("#hl-sort")) {
        clearWorkspaceChrome();
      }
      ensureHighlightsControls();
      return;
    }
    if (tabName === "viewer" || tabName === "configuration") {
      if (controls && !controls.querySelector("#file-q")) {
        clearWorkspaceChrome();
      }
    }
  }

  async function upload(forceReanalyse) {
    const f = $("#file-input").files?.[0];
    if (!f) {
      alert("Choose a snapshot archive first.");
      return;
    }
    const fd = new FormData();
    fd.append("file", f);
    $("#upload-status").textContent = forceReanalyse ? "Reanalysing…" : "Uploading & hashing…";
    const qs = forceReanalyse ? "?reanalyse=1" : "";
    try {
      const res = await api("/api/jobs" + qs, { method: "POST", body: fd });
      if (res.needs_choice && res.compatible_match) {
        const openSaved = confirm(
          `${res.message || "Identical archive bytes already analysed."}\n\nOK = Open saved results\nCancel = stay on upload (use Reanalyse if you need a new run)`
        );
        if (openSaved) {
          selectViewedJob(res.id, { name: f.name, product: "", state: res.state || "completed" });
          show("view-results");
          loadResultTab("overview");
          $("#upload-status").textContent = "Opened saved results.";
          return;
        }
        $("#upload-status").textContent = res.message || "Saved analysis exists. Choose Previous Analyses or upload with reanalyse.";
        return;
      }
      if (res.reused) {
        selectViewedJob(res.id, { name: f.name, product: "", state: res.state || "completed" });
        $("#upload-status").textContent = res.message || "Opened saved results for identical archive.";
        show("view-results");
        loadResultTab("overview");
        return;
      }
      state.activeJobId = res.id;
      selectViewedJob(res.id, { name: f.name, product: "", state: "queued" });
      state.terminal = false;
      show("view-progress");
      watchProgress(res.id);
    } catch (e) {
      const activeId = e.payload && e.payload.active_job_id;
      if (activeId) {
        state.activeJobId = activeId;
        selectViewedJob(activeId, { name: "active analysis", product: "", state: "processing" });
        $("#upload-status").textContent = "Another analysis is already active. Returning to it…";
        $("#btn-upload-resume").classList.remove("hidden");
        show("view-progress");
        watchProgress(activeId);
        return;
      }
      $("#upload-status").textContent = e.message;
    }
  }

  function formatElapsed(ms) {
    const s = Math.max(0, Math.floor((ms || 0) / 1000));
    const m = Math.floor(s / 60);
    const r = s % 60;
    return m > 0 ? `${m}m ${r}s` : `${r}s`;
  }

  function renderProgress(p) {
    const pct = typeof p.percent === "number" ? p.percent : 0;
    $("#prog-state").textContent = `${p.state || "…"} / ${p.stage || "…"}`;
    $("#prog-msg").textContent = p.message || p.current_source || "";
    let detail = `${p.files_done || 0} members cached · ${formatBytes(p.bytes_done || 0)} expanded`;
    if (p.stage === "parsing" || p.sources_total) {
      detail += ` · parsed ${p.parse_done || 0}/${p.sources_total || 0} log sources`;
    }
    if (p.events_committed != null) {
      detail += ` · ${p.events_committed} findings committed (${p.counts_final ? "final" : "provisional"})`;
    }
    if (p.first_result_ms) {
      detail += ` · first result ${formatElapsed(p.first_result_ms)}`;
    }
    if (p.current_source) {
      detail += ` · ${p.current_source}`;
    }
    $("#prog-files").textContent = detail;
    $("#prog-pct").textContent = `${pct}%`;
    $("#prog-elapsed").textContent = `Elapsed ${formatElapsed(p.elapsed_ms)}`;
    $("#prog-bar").style.width = `${Math.min(100, Math.max(0, pct))}%`;
  }

  function watchProgress(id) {
    if (state.progressWatchId === id && state.watching) return;
    closeProgressWatch();
    state.watching = true;
    state.progressWatchId = id;
    state.activeJobId = id;
    const es = new EventSource(`/api/jobs/${id}/progress?s=${encodeURIComponent(state.session)}`);
    state.progressES = es;
    es.onmessage = (ev) => {
      if (state.progressWatchId !== id) return;
      const p = JSON.parse(ev.data);
      if (state.viewedJobId === id && !$("#view-progress")?.classList.contains("hidden")) renderProgress(p);
      if (p.stage === "done" || ["completed", "partial", "cancelled", "failed"].includes(p.state)) {
        $("#prog-bar").style.width = "100%";
        closeProgressWatch();
        state.terminal = true;
        refreshActiveBanner();
        if (state.viewedJobId === id && !$("#view-progress")?.classList.contains("hidden")) {
          show("view-results");
          loadResultTab("overview");
        }
      }
    };
    es.onerror = () => {
      if (state.progressWatchId !== id) return;
      closeProgressWatch();
      pollJob(id);
    };
  }

  async function pollJob(id) {
    state.watching = true;
    state.progressWatchId = id;
    const tick = async () => {
      if (state.progressWatchId !== id) return;
      try {
        const j = await api(`/api/jobs/${id}`);
        if (state.viewedJobId === id && !$("#view-progress")?.classList.contains("hidden")) renderProgress(j);
        if (["completed", "partial", "cancelled", "failed"].includes(j.state)) {
          state.watching = false; state.progressWatchId = null; state.terminal = true;
          refreshActiveBanner();
          if (state.viewedJobId === id && !$("#view-progress")?.classList.contains("hidden")) {
            show("view-results"); loadResultTab("overview");
          }
          return;
        }
        setTimeout(tick, 1000);
      } catch (_) { state.watching = false; state.progressWatchId = null; }
    };
    tick();
  }

  function formatCategories(raw) {
    if (raw == null || raw === "" || raw === "null") return "";
    if (Array.isArray(raw)) return raw.filter(Boolean).join(", ");
    try {
      const parsed = JSON.parse(raw);
      if (Array.isArray(parsed)) return parsed.filter(Boolean).join(", ");
    } catch (_) {
      /* plain string */
    }
    const s = String(raw).trim();
    return s === "null" ? "" : s;
  }

  function pathParts(full) {
    const p = String(full || "").replace(/\\/g, "/");
    const i = p.lastIndexOf("/");
    if (i < 0) return { name: p, rel: "" };
    return { name: p.slice(i + 1), rel: p.slice(0, i) };
  }

  function viewerURL(jobId, sourceId, path, mode, lineStart, lineEnd) {
    const u = new URL("/viewer", location.origin);
    u.searchParams.set("job", jobId || state.viewedJobId);
    u.searchParams.set("source", sourceId);
    u.searchParams.set("s", state.session);
    u.searchParams.set("title", path || "Source");
    u.searchParams.set("mode", mode || "log");
    if (lineStart) u.searchParams.set("line", String(lineStart));
    if (lineEnd) u.searchParams.set("line_end", String(lineEnd));
    return u.pathname + u.search;
  }

  function fileLink(jobId, sourceId, path, mode, lineStart, lineEnd) {
    const parts = pathParts(path);
    const href = viewerURL(jobId, sourceId, path, mode, lineStart, lineEnd);
    return `<a class="file-link" href="${esc(href)}" target="_blank" rel="noopener" title="${esc(path)}">
      <span class="file-name">${esc(parts.name)}</span>
      ${parts.rel ? `<span class="file-rel">${esc(parts.rel)}</span>` : ""}
    </a>`;
  }

  function isFileOpenable(f) {
    if (typeof f.openable === "boolean") return f.openable;
    return f.coverage !== "metadata_only";
  }

  function fileLabel(jobId, f, mode) {
    const openable = isFileOpenable(f);
    const statusBits = [
      f.event_count != null ? `${f.event_count} events` : null,
      f.line_count != null ? `${f.line_count} lines` : null,
      f.coverage,
      f.classify_reason && f.coverage === "metadata_only" ? f.classify_reason : null,
    ]
      .filter(Boolean)
      .join(" · ");
    const label = openable
      ? fileLink(jobId, f.id, f.path, mode || f.source_kind || "log")
      : (() => {
          const parts = pathParts(f.path);
          return `<span class="path-label" title="${esc(f.path)}">
            <span class="file-name">${esc(parts.name)}</span>
            ${parts.rel ? `<span class="file-rel">${esc(parts.rel)}</span>` : ""}
          </span>`;
        })();
    return `<span class="path-label">${label}
      <span class="muted">${esc(statusBits)}</span></span>`;
  }

  function attrMap(items) {
    const m = {};
    for (const a of items || []) m[a.field] = a;
    return m;
  }

  function evidenceLine(jobId, a) {
    if (!a) return "";
    const name = a.source_path ? pathParts(a.source_path).name : "";
    const link =
      a.source_id && a.source_path
        ? fileLink(jobId, a.source_id, a.source_path, "log", a.line_start, a.line_end)
        : name
          ? `<span>${esc(name)}</span>`
          : "";
    return `<div class="muted">evidence · ${link} · lines ${a.line_start}–${a.line_end} · ${esc(a.confidence)} · ${esc(a.status)}</div>`;
  }

  function componentLabel(field) {
    const key = String(field || "").replace(/^component\./, "");
    const map = {
      "db.schema": "Configuration database schema version",
      "schema": "Component schema version",
    };
    for (const [k, v] of Object.entries(map)) {
      if (key === k || key.endsWith("." + k) || key.includes(k)) return v + ` (${key})`;
    }
    return `Component schema version (${key})`;
  }

  let resultLoadSeq = 0;
  async function loadResultTab(tab) {
    const jobId = state.viewedJobId;
    if (!jobId) return;
    const tabName = tab || state.viewTab || "overview";
    state.viewTab = tabName;
    const gen = state.viewGen;
    const seq = ++resultLoadSeq;
    const host = $("#result-body");
    prepareChromeForTab(tabName);
    host.innerHTML = `<p class='muted'>Loading ${esc((state.viewedMeta && state.viewedMeta.name) || jobId)}…</p>`;
    const stale = () => seq !== resultLoadSeq || !stillViewing(jobId, gen, tabName);
    if (tabName === "overview") {
      const data = await api(`/api/jobs/${jobId}/overview`);
      if (stale()) {
        reportDiag("ui.stale_discarded", { tab: "overview" });
        return;
      }
      const job = data.job || {};
      state.viewedMeta = { name: job.original_name || (state.viewedMeta && state.viewedMeta.name) || jobId, product: job.product_label || "", state: job.state || "saved" };
      updateViewingIdentity();
      const sel = attrMap(data.selected);
      const family = sel["product.family"] || sel["product.identity"];
      const release = sel["product.release"];
      const build = sel["product.build"] || sel["product.install_build"];
      const pkgRev = sel["product.package_revision"];
      const pkg = sel["product.installed_package"];
      const hostn = sel["hostname"];
      const ip = sel["network.ipv4"];
      const collected = sel["collection.time"];
      const os = sel["os.pretty_name"];
      const kernel = sel["os.kernel"];
      const platform = sel["platform"];
      const cpu = sel["cpu.model"];
      const mem = sel["memory.total_kb"];
      const uptime = sel["uptime"];
      const findings = data.findings || {};
      const recent = data.recent_activity || {};
      const covSum = data.coverage_summary || {};
      const compat = data.compatibility || {};
      let compatBanner = "";
      if (compat.needs_reanalysis || compat.refresh_failed || compat.stale_classifier) {
        compatBanner = `<div class="panel" style="border-color:var(--accent)"><strong>Saved analysis needs attention</strong>
          <p class="muted">${esc(compat.message || "Classifier or profile refresh required. Re-upload if evidence is missing; failed refresh preserves prior findings.")}</p>
          <p class="muted">${compat.refresh_failed ? "Refresh failed — prior findings retained. " : ""}${compat.classification_refreshed ? "Classification refreshed. " : ""}${compat.findings_rebuilt ? "Findings rebuilt. " : ""}Missing: ${esc((compat.missing || []).join(", ") || "none")}</p>
        </div>`;
      }
      const softBits = [
        release ? `release ${esc(release.raw)}` : null,
        pkgRev ? `package revision ${esc(pkgRev.raw)}` : null,
        build ? `build ${esc(build.raw)}` : null,
        pkg ? `package ${esc(pkg.raw)}` : null,
      ].filter(Boolean);
      const comps = (data.components || [])
        .map((c) => `<li><strong>${esc(componentLabel(c.field))}</strong>: ${esc(c.raw)} ${evidenceLine(jobId, c)}</li>`)
        .join("");
      const hist = (data.history || [])
        .slice(0, 40)
        .map((h) => `<li>${esc(h.field)} = ${esc(h.raw)} <span class="muted">(${esc(h.status)})</span></li>`)
        .join("");
      const recentItems = (recent.items || [])
        .map((g) => {
          const cats = formatCategories(g.categories);
          const when = g.raw_time || g.event_time_utc || "time uncertain";
          return `<li class="finding-snip">
            <div class="row" style="justify-content:space-between">
              <span class="sev-${esc(g.severity)}">${esc(g.severity)}</span>
              <span class="muted">${esc(when)}</span>
            </div>
            ${fileLink(jobId, g.source_id, g.path, "log", g.line_start, g.line_end)}
            <span class="muted">lines ${g.line_start}–${g.line_end}${cats ? " · " + esc(cats) : ""}</span>
            <pre>${esc(g.message)}</pre>
          </li>`;
        })
        .join("");
      const collDisplay = covSum.collection_time_raw || (collected && collected.raw) || "";
      host.innerHTML = `
        ${compatBanner}
        <div class="panel">
          <div class="row" style="justify-content:space-between">
            <div>
              <strong>${esc((family && family.raw) || job.product_label || "Unknown product — generic analysis")}</strong>
              <div class="muted">${esc(job.original_name)} · ${esc(job.state)}${job.partial ? " (partial coverage)" : ""}</div>
            </div>
            <button class="btn" id="btn-export">Export JSON</button>
          </div>
          <h2 style="margin-top:1rem;font-size:1rem">Identity &amp; software</h2>
          <div>Hostname: ${hostn ? esc(hostn.raw) : "<span class='muted'>unavailable</span>"}${evidenceLine(jobId, hostn)}</div>
          <div>Management IP: ${ip ? esc(ip.raw) : "<span class='muted'>unavailable</span>"}${evidenceLine(jobId, ip)}</div>
          <div>${softBits.length ? softBits.join(" · ") : "<span class='muted'>release/build unavailable — see history or reanalyse</span>"}</div>
          ${release ? evidenceLine(jobId, release) : ""}
          ${build ? evidenceLine(jobId, build) : ""}
          <div class="muted" style="margin-top:0.4rem">OS: ${os ? esc(os.raw) : "unavailable"} · Kernel: ${kernel ? esc(kernel.raw) : "unavailable"} · Platform: ${platform ? esc(platform.raw) : "unavailable"}</div>
          <div class="muted">CPU: ${cpu ? esc(cpu.raw) : "unavailable"} · Memory: ${mem ? esc(mem.raw) + " kB" : "unavailable"} · Uptime: ${uptime ? esc(uptime.raw) : "unavailable"}</div>

          <h2 style="margin-top:1rem;font-size:1rem">Snapshot &amp; log coverage</h2>
          <div>Snapshot collection time: ${collDisplay ? esc(collDisplay) : "<span class='muted'>unavailable</span>"}${collected ? evidenceLine(jobId, collected) : ""}</div>
          <div>Observed log range: ${covSum.observed_log_start && covSum.observed_log_end ? esc(covSum.observed_log_start) + " → " + esc(covSum.observed_log_end) : "<span class='muted'>unknown / no reliable timestamps</span>"}</div>
          <div>Latest dated entry: ${covSum.latest_dated_entry ? esc(covSum.latest_dated_entry) : "<span class='muted'>unavailable</span>"}</div>
          <div class="muted">Analysis time: ${esc(covSum.analysis_time || job.updated_utc || "—")} · Log sources: ${covSum.log_sources || 0} · Configuration: ${covSum.configuration_sources || 0} · Partial sources: ${covSum.partial_sources || 0}</div>
          ${covSum.error_detail ? `<div class="muted">Intake/error detail: ${esc(covSum.error_detail)}</div>` : ""}

          <h2 style="margin-top:1rem;font-size:1rem">Recent findings</h2>
          <p class="muted">Detection matches — not confirmed incidents. Window: latest 24h relative to ${esc(recent.ref_label || "unknown")} (${esc(recent.ref_time || "—")}).</p>
          <div class="muted">${recent.event_total || 0} events · ${recent.group_total || 0} groups in window · ${esc(JSON.stringify(recent.by_severity || {}))} · ${findings.undated_total || 0} undated overall · ${recent.historical_total || 0} older dated</div>
          <ul class="list">${recentItems || "<li class='muted'>No recent findings in the selected window. Use Log Highlights → All available for historical evidence.</li>"}</ul>
          <div class="muted">${findings.event_total || 0} total findings · ${findings.group_total || 0} groups across all retained logs</div>

          <details style="margin-top:1rem"><summary>Technical details (component schema versions)</summary>
            <p class="muted">These are component/database schema versions from configuration evidence, not device firmware versions.</p>
            <ul class="list">${comps || "<li class='muted'>No component schema versions extracted.</li>"}</ul>
          </details>
          <details style="margin-top:0.6rem"><summary>Evidence history / superseded values</summary>
            <ul class="list">${hist || "<li class='muted'>None retained.</li>"}</ul>
          </details>
        </div>`;
      $("#btn-export").onclick = () => {
        window.open(`/api/jobs/${jobId}/exports?s=${encodeURIComponent(state.session)}`, "_blank");
      };
    } else if (tabName === "highlights") {
      await loadHighlights(0, seq);
    } else if (tabName === "viewer") {
      await loadFileList("log", 0, "", seq);
    } else if (tabName === "configuration") {
      await loadFileList("configuration", 0, "", seq);
    } else if (tabName === "coverage") {
      const data = await api(`/api/jobs/${jobId}/coverage`);
      if (stale()) {
        reportDiag("ui.stale_discarded", { tab: "coverage" });
        return;
      }
      const kinds = Object.entries(data.kind_counts || {})
        .map(([k, v]) => `<li>${esc(k)}: ${v}</li>`)
        .join("");
      const sources = (data.sources || [])
        .map((s) => {
          const parts = pathParts(s.path);
          return `<li><strong>${esc(s.source_kind)}</strong> · <span title="${esc(s.path)}">${esc(parts.name)}</span>
            <span class="muted">${esc(parts.rel)} · ${esc(s.coverage)} · ${s.line_count || 0} lines · ${esc(s.classify_reason || "")}</span></li>`;
        })
        .join("");
      host.innerHTML = `<div class="panel">
        <h2 style="font-size:1rem">Source classification</h2>
        <ul class="list">${kinds || "<li class='muted'>No sources.</li>"}</ul>
        <h2 style="font-size:1rem;margin-top:1rem">All members</h2>
        <ul class="list">${sources || "<li class='muted'>None.</li>"}</ul>
        <h2 style="font-size:1rem;margin-top:1rem">Coverage issues</h2>
        <ul class="list">${(data.items || [])
          .map((i) => `<li><strong>${esc(i.code)}</strong> · ${esc(i.severity)} — ${esc(i.reason)}</li>`)
          .join("") || "<li class='muted'>No coverage issues recorded.</li>"}</ul>
      </div>`;
    }
  }


  async function loadFileList(kind, offset, q, loadSeq) {
    const jobId = state.viewedJobId;
    if (!jobId) return;
    const gen = state.viewGen;
    const host = $("#result-body");
    const controls = $("#workspace-controls");
    fileListState.kind = kind;
    fileListState.offset = offset || 0;
    if (q !== undefined && q !== null) fileListState.q = q;
    const token = ++fileListState.reqToken;
    if (fileListState.abort) try { fileListState.abort.abort(); } catch (_) {}
    fileListState.abort = new AbortController();
    const params = new URLSearchParams({ kind, offset: String(fileListState.offset), limit: "50" });
    if (fileListState.q) params.set("q", fileListState.q);
    let files;
    try {
      files = await api(`/api/jobs/${jobId}/files?${params}`, { signal: fileListState.abort.signal });
    } catch (e) {
      if (e.name === "AbortError") return;
      throw e;
    }
    const expectTab = kind === "configuration" ? "configuration" : "viewer";
    if ((loadSeq != null && loadSeq !== resultLoadSeq) || !stillViewing(jobId, gen, expectTab) || token !== fileListState.reqToken) {
      reportDiag("ui.stale_discarded", { tab: expectTab });
      return;
    }
    const label = kind === "configuration" ? "Configuration" : "Log";
    const findLabel = kind === "configuration" ? "Find configuration file" : "Find log file";
    const items = files.items || [];
    let listHtml;
    if (!(files.total_files > 0)) listHtml = `<li class="muted">No ${label.toLowerCase()} sources classified.</li>`;
    else if (!(files.matching_files > 0)) listHtml = `<li class="muted">No filenames match “${esc(fileListState.q)}”. Clear the filter to see all ${files.total_files} ${label.toLowerCase()} files.</li>`;
    else {
      listHtml = items.map((f) => {
        const openBtn = isFileOpenable(f)
          ? `<button class="btn open-log" data-job="${esc(jobId)}" data-source="${esc(f.id)}" data-path="${esc(f.path)}" data-mode="${esc(kind)}">Open</button>`
          : `<span class="muted file-blocked" title="${esc(f.classify_reason || f.coverage || "not openable")}">Listed · not openable</span>`;
        return `<li>${fileLabel(jobId, f, kind)} ${openBtn}</li>`;
      }).join("");
    }
    const from = items.length ? fileListState.offset + 1 : 0;
    const to = fileListState.offset + items.length;
    const countText = `Matching ${files.matching_files || 0} of ${files.total_files || 0} · showing ${from}–${to}${files.truncated ? " (more available)" : ""}`;
    if (controls && !controls.querySelector("#file-q")) {
      controls.innerHTML = `<div class="workspace-control-row" id="file-toolbar">
        <label class="workspace-find-label" for="file-q">${esc(findLabel)}</label>
        <input id="file-q" type="search" placeholder="Filename or path" aria-label="${esc(findLabel)}" />
        <button type="button" class="btn" id="file-q-search">Search</button>
        <button type="button" class="btn" id="file-q-clear">Clear</button>
      </div>`;
      const input = $("#file-q");
      input.value = fileListState.q;
      const runSearch = (immediate) => {
        clearTimeout(fileListState.timer);
        const k = fileListState.kind || "log";
        const doIt = () => loadFileList(k, 0, input.value.trim());
        if (immediate) doIt(); else fileListState.timer = setTimeout(doIt, 200);
      };
      input.oninput = () => runSearch(false);
      input.onkeydown = (ev) => { if (ev.key === "Enter") { ev.preventDefault(); runSearch(true); } };
      $("#file-q-search").onclick = () => runSearch(true);
      $("#file-q-clear").onclick = () => {
        clearTimeout(fileListState.timer);
        if (fileListState.abort) fileListState.abort.abort();
        input.value = "";
        loadFileList(fileListState.kind || "log", 0, "");
        input.focus();
      };
    } else if (controls) {
      const lab = controls.querySelector(".workspace-find-label");
      if (lab) lab.textContent = findLabel;
      const input = $("#file-q");
      if (input && document.activeElement !== input) input.value = fileListState.q;
      if (input) input.setAttribute("aria-label", findLabel);
    }
    setWorkspaceStatus(`<span>${esc(countText)}</span>
      <details class="workspace-help"><summary>About file list</summary>
        <p>${esc(label)} sources only — locate by name/path (not content). Openable filenames use a new browser tab. Named logs stay listed even when content is blocked or empty.</p>
      </details>`);
    host.innerHTML = `<div class="panel content-panel" id="file-list-panel">
      <ul class="list" id="file-q-results">${listHtml}</ul>
      <div class="row" style="margin-top:0.8rem">
        <button class="btn" id="file-prev" ${fileListState.offset <= 0 ? "disabled" : ""}>Previous</button>
        <button class="btn" id="file-next" ${files.truncated ? "" : "disabled"}>Next</button>
      </div>
      <p id="popup-fallback" class="muted hidden"></p>
    </div>`;
    $("#file-prev").onclick = () => loadFileList(kind, Math.max(0, fileListState.offset - 50), fileListState.q);
    $("#file-next").onclick = () => loadFileList(kind, fileListState.offset + 50, fileListState.q);
    bindOpenLog();
  }

  function ensureHighlightsControls() {
    const controls = $("#workspace-controls");
    if (!controls) return;
    if (controls.querySelector("#hl-sort")) return;
    controls.innerHTML = `<div class="workspace-control-row" id="hl-toolbar">
      <label>Sort <select id="hl-sort" aria-label="Sort highlights">
        <option value="newest">Newest first</option>
        <option value="oldest">Oldest first</option>
        <option value="severity_newest">Severity then newest</option>
        <option value="file_line">File / line</option>
      </select></label>
      <label>Range <select id="hl-range" aria-label="Time range">
        <option value="24h">Latest 24 hours</option>
        <option value="7d">Latest 7 days</option>
        <option value="all">All available</option>
        <option value="historical">Historical (older than 24h)</option>
        <option value="custom">Custom</option>
      </select></label>
      <label><input type="checkbox" id="hl-undated" /> Include undated</label>
    </div>`;
    const sortEl = $("#hl-sort");
    const rangeEl = $("#hl-range");
    const und = $("#hl-undated");
    if (sortEl) {
      sortEl.value = hlState.sort;
      sortEl.onchange = () => { hlState.sort = sortEl.value; bumpViewGen(); loadHighlights(0); };
    }
    if (rangeEl) {
      rangeEl.value = hlState.range;
      rangeEl.onchange = () => { hlState.range = rangeEl.value; bumpViewGen(); loadHighlights(0); };
    }
    if (und) {
      und.checked = !!hlState.includeUndated;
      und.onchange = () => { hlState.includeUndated = und.checked; bumpViewGen(); loadHighlights(0); };
    }
  }

  async function loadHighlights(offset, loadSeq) {
    const jobId = state.viewedJobId;
    if (!jobId) return;
    const gen = state.viewGen;
    const host = $("#result-body");
    ensureHighlightsControls();
    hlState.offset = offset || 0;
    const sortEl = $("#hl-sort");
    const rangeEl = $("#hl-range");
    const und = $("#hl-undated");
    if (sortEl && document.activeElement !== sortEl) sortEl.value = hlState.sort;
    if (rangeEl && document.activeElement !== rangeEl) rangeEl.value = hlState.range;
    if (und && document.activeElement !== und) und.checked = !!hlState.includeUndated;
    const q = new URLSearchParams({
      offset: String(hlState.offset),
      limit: "50",
      sort: hlState.sort,
      range: hlState.range,
      include_undated: hlState.includeUndated ? "1" : "0",
    });
    const data = await api(`/api/jobs/${jobId}/events?${q}`);
    if ((loadSeq != null && loadSeq !== resultLoadSeq) || !stillViewing(jobId, gen, "highlights")) {
      reportDiag("ui.stale_discarded", { tab: "highlights" });
      return;
    }
    const byFile = {};
    const fileOrder = [];
    for (const g of data.items || []) {
      if (!byFile[g.path]) {
        byFile[g.path] = [];
        fileOrder.push(g.path);
      }
      byFile[g.path].push(g);
    }
    setWorkspaceStatus(
      `<span>Showing ${data.page_event_count || 0} of ${data.total_events || 0} events${data.truncated ? " (more available)" : ""}</span>` +
      `<span>Reference: ${esc(data.ref_label || "—")} (${esc(data.ref_time || "—")}) · ${data.undated_total || 0} undated</span>` +
      `<details class="workspace-help"><summary>Details</summary>` +
      `<p>Filter/sort applied before pagination. Findings are detection matches, not confirmed incidents.</p></details>`
    );
    let html = "";
    if (!(data.items || []).length) {
      html += `<div class="panel content-panel"><p class="muted">No findings in this window.</p>
        ${data.range !== "all" ? `<button class="btn" id="hl-show-all">Show all available</button>` : ""}</div>`;
    }
    const HL = window.QAHighlightsLayout;
    for (const path of fileOrder) {
      const groups = byFile[path];
      const sid = groups[0]?.source_id;
      if (HL && typeof HL.renderFileBlock === "function") {
        const mapped = groups.map((g) => ({
          severity: g.severity,
          categories: formatCategories(g.categories),
          count: g.count,
          line_start: g.line_start,
          line_end: g.line_end,
          raw_time: g.raw_time,
          event_time_utc: g.event_time_utc,
          message: g.message,
          source_id: g.source_id,
          path: g.path,
        }));
        html += HL.renderFileBlock({
          jobId,
          sourceId: sid,
          path,
          groups: mapped,
          fileHref: viewerURL(jobId, sid, path, "log"),
          rangeHrefFor: (g) => viewerURL(jobId, g.source_id || sid, g.path || path, "log", g.line_start, g.line_end),
          esc,
        });
      } else {
        html += `<section class="file-block panel hl-file-block">
          <div class="hl-file-header">
            <div class="hl-file-identity">${fileLink(jobId, sid, path, "log")}</div>
            <button type="button" class="btn open-log" data-job="${esc(jobId)}" data-source="${esc(sid)}" data-path="${esc(path)}" data-mode="log">Open</button>
          </div>
          <div class="hl-file-summary muted">${groups.length} issue group(s) on this page (page-local grouping)</div>`;
        for (const g of groups) {
          const cats = formatCategories(g.categories);
          const when = g.raw_time || g.event_time_utc || "time uncertain";
          const href = viewerURL(jobId, g.source_id, g.path, "log", g.line_start, g.line_end);
          html += `<article class="hl-issue">
            <div class="hl-issue-meta">
              <span class="sev-${esc(g.severity)}">${esc(g.severity)}</span>${cats ? ` · ${esc(cats)}` : ""}
              <span class="hl-issue-count"> · ${g.count} occurrence(s) on page · </span>
              <a class="hl-line-range" href="${esc(href)}" target="_blank" rel="noopener">Lines ${g.line_start}–${g.line_end}</a>
            </div>
            <div class="hl-issue-time muted">${esc(when)}</div>
            <pre class="hl-issue-snippet">${esc(g.message)}</pre>
          </article>`;
        }
        html += `</section>`;
      }
    }
    html += `<div class="row" style="margin-top:1rem">
      <button class="btn" id="btn-ev-prev" ${hlState.offset <= 0 ? "disabled" : ""}>Previous</button>
      <button class="btn" id="btn-ev-next" ${data.truncated ? "" : "disabled"}>Next</button>
    </div>
    <p id="popup-fallback" class="muted hidden"></p>`;
    host.innerHTML = html;
    const showAll = $("#hl-show-all");
    if (showAll) showAll.onclick = () => { hlState.range = "all"; if (rangeEl) rangeEl.value = "all"; loadHighlights(0); };
    $("#btn-ev-prev").onclick = () => loadHighlights(Math.max(0, hlState.offset - 50));
    $("#btn-ev-next").onclick = () => loadHighlights(hlState.offset + 50);
    bindOpenLog();
  }

  function bindOpenLog() {
    $$(".open-log").forEach((btn) => {
      btn.onclick = () => {
        const jobId = btn.dataset.job || state.viewedJobId;
        const sid = btn.dataset.source;
        const path = btn.dataset.path;
        const mode = btn.dataset.mode || "log";
        const line = btn.dataset.line || "";
        const url = viewerURL(jobId, sid, path, mode, line || undefined);
        const w = window.open(url, "_blank");
        if (!w) {
          const fb = $("#popup-fallback");
          if (fb) {
            fb.classList.remove("hidden");
            fb.innerHTML = `Popup blocked. <a href="${esc(url)}" target="_blank" rel="noopener">Open ${esc(mode)} in new tab</a>`;
          } else {
            location.href = url;
          }
        }
      };
    });
  }

  async function loadPrevious() {
    await refreshStorage();
    const data = await api("/api/jobs");
    const activeId = data.active_job_id || "";
    $("#previous-list").innerHTML = (data.items || [])
      .map((j) => {
        const running = (j.active || j.id === activeId) && isRunningState(j.state);
        const interrupted = j.state === "interrupted" || (!running && isRunningState(j.state));
        let status = j.state;
        if (running) status = `${j.state} (active — in progress)`;
        else if (interrupted) status = `interrupted (was ${j.stage || j.state}; partial results)`;
        const label = running ? "Resume" : "Open";
        const disk = j.disk_bytes != null ? formatBytes(j.disk_bytes) : "";
        return `<li class="row" style="justify-content:space-between">
          <span>${esc(j.original_name)} · ${esc(j.product_label || "—")} · ${esc(status)}${disk ? ` · disk ${disk}` : ""}
            <span class="muted" style="display:block">Reopens saved results without re-upload. SHA ${esc((j.sha256 || "").slice(0, 12))}…</span>
          </span>
          <span class="row">
            <button class="btn reopen" data-id="${esc(j.id)}" data-name="${esc(j.original_name || "")}" data-product="${esc(j.product_label || "")}" data-state="${esc(j.state || "")}" data-running="${running ? "1" : "0"}">${label}</button>
            ${running ? "" : `<button class="btn clean-job" data-id="${esc(j.id)}">Clean temp/search</button>`}
            ${running ? "" : `<button class="btn delete-job" data-id="${esc(j.id)}">Delete analysis</button>`}
          </span>
        </li>`;
      })
      .join("") || "<li class='muted'>No retained analyses in this storage root.</li>";
    $$(".reopen").forEach((b) => {
      b.onclick = () => {
        const id = b.dataset.id;
        selectViewedJob(id, { name: b.dataset.name || id, product: b.dataset.product || "", state: b.dataset.state || "" });
        if (b.dataset.running === "1") {
          show("view-progress");
          watchProgress(id);
        } else {
          $$(".result-tab").forEach((t) => t.classList.remove("active"));
          const ov = document.querySelector('.result-tab[data-tab="overview"]');
          if (ov) ov.classList.add("active");
          state.viewTab = "overview";
          show("view-results");
          loadResultTab("overview");
        }
      };
    });
    $$(".clean-job").forEach((b) => {
      b.onclick = async () => {
        if (!confirm("Clean temporary/search/export data for this analysis? Retained upload copy, evidence and findings stay so you can reopen results.")) return;
        try {
          const res = await api(`/api/jobs/${b.dataset.id}/clean`, { method: "POST" });
          alert(res.note || "Cleaned.");
          await loadPrevious();
        } catch (e) {
          alert(e.message || String(e));
        }
      };
    });
    $$(".delete-job").forEach((b) => {
      b.onclick = async () => {
        if (!confirm("Delete this analysis from the selected storage root?\n\nThis removes the internal retained upload copy (snapshot.bin), decoded evidence, results database, searches and exports for this job.\n\nYour original snapshot file outside this storage root is NOT deleted or modified.\n\nReopening these results afterward requires uploading again.")) return;
        try {
          await api(`/api/jobs/${b.dataset.id}`, { method: "DELETE" });
          await loadPrevious();
        } catch (e) {
          alert(e.message || String(e));
        }
      };
    });
  }

  init().catch((e) => {
    document.body.innerHTML = `<pre style="padding:2rem">Failed to start UI: ${esc(e.message)}</pre>`;
  });
})();
