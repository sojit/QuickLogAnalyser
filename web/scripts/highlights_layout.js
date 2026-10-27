/**
 * Log Highlights presentation helpers (draft.22).
 * Pure markup builders — no shared fileLink mutation.
 */
(function (root, factory) {
  if (typeof module === "object" && module.exports) {
    module.exports = factory();
  } else {
    root.QAHighlightsLayout = factory();
  }
})(typeof globalThis !== "undefined" ? globalThis : this, function () {
  "use strict";

  function defaultEsc(s) {
    return String(s ?? "")
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;");
  }

  function pathParts(full) {
    const p = String(full || "").replace(/\\/g, "/");
    const i = p.lastIndexOf("/");
    if (i < 0) return { name: p, rel: "" };
    return { name: p.slice(i + 1), rel: p.slice(0, i) };
  }

  /** Compact Lines X–Y deep link — never embeds filename/path blocks. */
  function renderLineRangeLink(opts) {
    const esc = opts.esc || defaultEsc;
    const start = opts.lineStart;
    const end = opts.lineEnd != null ? opts.lineEnd : opts.lineStart;
    const label = `Lines ${start}–${end}`;
    return `<a class="hl-line-range" href="${esc(opts.href)}" target="_blank" rel="noopener" title="${esc(label)}">${esc(label)}</a>`;
  }

  function renderFileHeader(opts) {
    const esc = opts.esc || defaultEsc;
    const parts = pathParts(opts.path);
    const nameHref = opts.nameHref || opts.href || "#";
    return `<div class="hl-file-header">
      <div class="hl-file-identity">
        <a class="hl-file-name" href="${esc(nameHref)}" target="_blank" rel="noopener" title="${esc(opts.path)}">${esc(parts.name)}</a>
        ${parts.rel ? `<div class="hl-file-path" title="${esc(opts.path)}">${esc(parts.rel)}</div>` : ""}
      </div>
      <button type="button" class="btn open-log hl-file-open" data-job="${esc(opts.jobId)}" data-source="${esc(opts.sourceId)}" data-path="${esc(opts.path)}" data-mode="log">Open</button>
    </div>`;
  }

  function renderIssue(opts) {
    const esc = opts.esc || defaultEsc;
    const cats = opts.categories ? ` · ${esc(opts.categories)}` : "";
    const when = opts.when || "time uncertain";
    const range = renderLineRangeLink({
      href: opts.rangeHref,
      lineStart: opts.lineStart,
      lineEnd: opts.lineEnd,
      esc,
    });
    return `<article class="hl-issue">
      <div class="hl-issue-meta">
        <span class="sev-${esc(opts.severity)}">${esc(opts.severity)}</span>${cats}
        <span class="hl-issue-count"> · ${esc(String(opts.count))} occurrence(s) on page · </span>
        ${range}
      </div>
      <div class="hl-issue-time muted">${esc(when)}</div>
      <pre class="hl-issue-snippet">${esc(opts.message)}</pre>
    </article>`;
  }

  function renderFileBlock(opts) {
    const esc = opts.esc || defaultEsc;
    const groups = opts.groups || [];
    const header = renderFileHeader({
      jobId: opts.jobId,
      sourceId: opts.sourceId,
      path: opts.path,
      nameHref: opts.fileHref,
      esc,
    });
    let body = "";
    for (const g of groups) {
      body += renderIssue({
        severity: g.severity,
        categories: g.categories,
        count: g.count,
        lineStart: g.line_start,
        lineEnd: g.line_end,
        when: g.raw_time || g.event_time_utc || "time uncertain",
        message: g.message,
        rangeHref: typeof opts.rangeHrefFor === "function"
          ? opts.rangeHrefFor(g)
          : opts.rangeHref,
        esc,
      });
    }
    return `<section class="file-block panel hl-file-block">
      ${header}
      <div class="hl-file-summary muted">${groups.length} issue group(s) on this page (page-local grouping)</div>
      ${body}
    </section>`;
  }

  /** Structure checks for the two-issue same-file regression. */
  function analyzeMarkup(html) {
    const filenameBlocks = (html.match(/class="hl-file-name"/g) || []).length;
    const pathBlocks = (html.match(/class="hl-file-path"/g) || []).length;
    const lineRanges = (html.match(/class="hl-line-range"/g) || []).length;
    const fileLinksInMeta = /hl-issue-meta[\s\S]*?class="file-link"/.test(html);
    const fileNameInMeta = /hl-issue-meta[\s\S]*?class="file-name"/.test(html);
    const issues = (html.match(/class="hl-issue"/g) || []).length;
    return {
      filenameBlocks,
      pathBlocks,
      lineRanges,
      issues,
      fileLinksInMeta,
      fileNameInMeta,
      ok:
        filenameBlocks === 1 &&
        pathBlocks === 1 &&
        lineRanges === 2 &&
        issues === 2 &&
        !fileLinksInMeta &&
        !fileNameInMeta,
    };
  }

  return {
    pathParts,
    renderLineRangeLink,
    renderFileHeader,
    renderIssue,
    renderFileBlock,
    analyzeMarkup,
    defaultEsc,
  };
});
