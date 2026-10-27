package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"quickanalyser/internal/appinfo"
	"quickanalyser/internal/config"
	"quickanalyser/internal/diag"
	"quickanalyser/internal/evidence"
	"quickanalyser/internal/export"
	"quickanalyser/internal/jobs"
	"quickanalyser/internal/masking"
	"quickanalyser/internal/platform"
	"quickanalyser/internal/search"
	"quickanalyser/internal/storage"
	"quickanalyser/internal/store"
	"quickanalyser/web"
)

type Server struct {
	Root       *storage.Manager
	Engine     *jobs.Engine
	Limits     config.Limits
	Session    string
	Addr       string
	HTTP       *http.Server
	Log        *diag.Logger
	mu         sync.Mutex
	needsSetup bool
	quitting   bool
	clientDiag struct {
		mu    sync.Mutex
		count int
		window time.Time
	}
}

func New(root *storage.Manager, needsSetup bool, limits config.Limits) *Server {
	log := diag.Instance()
	return &Server{
		Root:       root,
		Engine:     jobs.NewEngine(root, limits),
		Limits:     limits,
		Session:    newToken(),
		needsSetup: needsSetup,
		Log:        log,
	}
}

func newToken() string {
	var b [24]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func (s *Server) ListenAndServe(openBrowser bool) error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	s.Addr = ln.Addr().String()
	mux := http.NewServeMux()
	mux.Handle("/", s.withSecurity(s.handleUI))
	mux.Handle("/viewer", s.withSecurity(s.handleViewer))
	mux.Handle("/styles/", s.withSecurity(s.handleStatic))
	mux.Handle("/scripts/", s.withSecurity(s.handleStatic))
	mux.Handle("/assets/", s.withSecurity(s.handleStatic))
	mux.Handle("/fixtures/", s.withSecurity(s.handleStatic))
	mux.Handle("/api/session", s.withSecurity(s.handleSession))
	mux.Handle("/api/about", s.withSecurity(s.handleAbout))
	mux.Handle("/api/storage", s.withSecurity(s.handleStorage))
	mux.Handle("/api/jobs", s.withSecurity(s.handleJobs))
	mux.Handle("/api/active-job", s.withSecurity(s.handleActiveJob))
	mux.Handle("/api/jobs/", s.withSecurity(s.handleJobSub))
	mux.Handle("/api/quit", s.withSecurity(s.handleQuit))
	mux.Handle("/api/diagnostics", s.withSecurity(s.handleDiagnostics))
	mux.Handle("/api/diagnostics/bundle", s.withSecurity(s.handleDiagnosticsBundle))
	mux.Handle("/api/diagnostics/client", s.withSecurity(s.handleDiagnosticsClient))

	listenMsg := "http://" + s.Addr + "/"
	fmt.Printf("%s %s listening on %s\n", appinfo.ProductName, appinfo.Version(), listenMsg)
	if s.Log != nil {
		s.Log.Info("startup.listening", map[string]any{"addr": s.Addr})
	}
	bootURL := "http://" + s.Addr + "/?s=" + s.Session
	if openBrowser {
		_ = platform.OpenBrowser(bootURL)
	}
	s.HTTP = &http.Server{Handler: mux, ReadHeaderTimeout: 30 * time.Second}
	return s.HTTP.Serve(ln)
}

// Shutdown cancels active analysis and stops the HTTP server gracefully.
func (s *Server) Shutdown() error {
	s.mu.Lock()
	s.quitting = true
	s.mu.Unlock()
	if s.Engine != nil {
		if id := s.Engine.ActiveJobID(); id != "" {
			s.Engine.Cancel(id)
		}
	}
	if s.HTTP == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.HTTP.Shutdown(ctx)
}

func (s *Server) withSecurity(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if i := strings.IndexByte(host, ':'); i >= 0 {
			host = host[:i]
		}
		if host != "127.0.0.1" && host != "localhost" {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			if !allowedLoopbackOrigin(origin) {
				http.Error(w, "forbidden origin", http.StatusForbidden)
				return
			}
		}
		if strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/api/session" {
			tok := r.Header.Get("X-QA-Session")
			if tok == "" {
				tok = r.URL.Query().Get("s")
			}
			if tok != s.Session {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		next(w, r)
	})
}

func allowedLoopbackOrigin(origin string) bool {
	o := strings.ToLower(strings.TrimSpace(origin))
	return strings.HasPrefix(o, "http://127.0.0.1") || strings.HasPrefix(o, "http://localhost") ||
		strings.HasPrefix(o, "https://127.0.0.1") || strings.HasPrefix(o, "https://localhost")
}

func (s *Server) handleUI(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	data, err := web.FS.ReadFile("index.html")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, "/")
	p = strings.TrimPrefix(p, "assets/")
	if strings.HasPrefix(r.URL.Path, "/assets/") {
		switch {
		case strings.HasSuffix(p, ".css"):
			p = "styles/" + filepath.Base(p)
		case strings.HasSuffix(p, ".js"):
			p = "scripts/" + filepath.Base(p)
		}
	}
	data, err := web.FS.ReadFile(p)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	switch {
	case strings.HasSuffix(p, ".css"):
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	case strings.HasSuffix(p, ".js"):
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	case strings.HasSuffix(p, ".html"):
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	default:
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	_, _ = w.Write(data)
}

func (s *Server) handleViewer(w http.ResponseWriter, r *http.Request) {
	data, err := web.FS.ReadFile("viewer.html")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{
		"session":     s.Session,
		"needs_setup": s.needsSetup,
		"product":     appinfo.ProductName,
		"signature":   appinfo.DeveloperDisplay,
		"version":     appinfo.Version(),
	})
}

func (s *Server) handleAbout(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, appinfo.BuildIdentity())
}

func (s *Server) handleStorage(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		info := storageInfo(s.Root)
		info["needs_setup"] = s.needsSetup
		writeJSON(w, info)
	case http.MethodPost:
		var body struct {
			Mode string `json:"mode"`
			Path string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if s.Engine != nil && s.Engine.Leases != nil {
			if err := s.Engine.Leases.AcquireRoot(); err != nil {
				http.Error(w, err.Error(), 409)
				return
			}
			defer s.Engine.Leases.ReleaseRoot()
		}
		var (
			m   *storage.Manager
			err error
		)
		switch body.Mode {
		case storage.ModeDefault:
			p, e := platform.DefaultDataRoot()
			if e != nil {
				http.Error(w, e.Error(), 400)
				return
			}
			m, err = s.Root.ChangeRoot(p, storage.ModeDefault)
		case storage.ModePortable:
			p, e := platform.PortableDataRoot()
			if e != nil {
				http.Error(w, e.Error(), 400)
				return
			}
			m, err = s.Root.ChangeRoot(p, storage.ModePortable)
		case storage.ModeCustom:
			if strings.TrimSpace(body.Path) == "" {
				http.Error(w, "path required", 400)
				return
			}
			m, err = s.Root.ChangeRoot(body.Path, storage.ModeCustom)
		default:
			http.Error(w, "invalid mode", 400)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		s.Root = m
		s.Engine = jobs.NewEngine(m, s.Limits)
		s.needsSetup = false
		if s.Log != nil {
			_ = s.Log.AttachRoot(m.Root)
			s.Log.Info("storage.root_changed", map[string]any{"mode": body.Mode})
		}
		writeJSON(w, storageInfo(m))
	default:
		http.Error(w, "method not allowed", 405)
	}
}

func (s *Server) handleActiveJob(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", 405)
		return
	}
	id := s.Engine.ActiveJobID()
	if id == "" {
		writeJSON(w, map[string]any{"active": false})
		return
	}
	p, err := s.readJobProgress(id)
	if err != nil {
		writeJSON(w, map[string]any{"active": true, "id": id})
		return
	}
	p["active"] = true
	writeJSON(w, p)
}

func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		list, err := s.Engine.ListJobs()
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, map[string]any{"items": list, "active_job_id": s.Engine.ActiveJobID()})
	case http.MethodPost:
		if s.needsSetup {
			http.Error(w, "complete storage setup first", 409)
			return
		}
		// Stream multipart parts directly; do not ParseMultipartForm (avoids OS-temp spool for large archives).
		mr, err := r.MultipartReader()
		if err != nil {
			http.Error(w, "expected multipart upload", 400)
			return
		}
		var (
			filename string
			part     io.Reader
			closer   io.Closer
		)
		for {
			p, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			if p.FormName() != "file" {
				_, _ = io.Copy(io.Discard, p)
				_ = p.Close()
				continue
			}
			filename = p.FileName()
			if filename == "" {
				filename = "snapshot.tar.gz"
			}
			part = p
			closer = p
			break
		}
		if part == nil {
			http.Error(w, "file required", 400)
			return
		}
		defer closer.Close()
		force := r.URL.Query().Get("reanalyse") == "1" || r.URL.Query().Get("force") == "1"
		res, err := s.Engine.CreateJobOpts(filename, 0, part, jobs.CreateOpts{ForceReanalyse: force})
		if err != nil {
			active := s.Engine.ActiveJobID()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error":         err.Error(),
				"active_job_id": active,
				"code":          "job_active",
			})
			return
		}
		writeJSON(w, res)
	default:
		http.Error(w, "method not allowed", 405)
	}
}

func (s *Server) handleJobSub(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/jobs/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	id := parts[0]
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			s.handleJobGet(w, r, id)
		case http.MethodDelete:
			s.handleJobDelete(w, r, id)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}
	switch parts[1] {
	case "progress":
		s.handleProgress(w, r, id)
	case "cancel":
		s.Engine.Cancel(id)
		writeJSON(w, map[string]string{"status": "cancelling"})
	case "profile":
		s.handleProfile(w, r, id)
	case "overview":
		s.handleOverview(w, r, id)
	case "compatibility":
		s.handleCompatibility(w, r, id)
	case "files":
		s.handleFiles(w, r, id)
	case "events":
		s.handleEvents(w, r, id)
	case "coverage":
		s.handleCoverage(w, r, id)
	case "sources":
		if len(parts) >= 4 {
			switch parts[3] {
			case "context":
				s.handleContext(w, r, id, parts[2])
			default:
				http.Error(w, "unsupported source subroute", http.StatusNotFound)
			}
			return
		}
		s.handleSources(w, r, id)
	case "searches":
		if len(parts) == 2 && r.Method == http.MethodPost {
			s.handleSearchStart(w, r, id)
		} else if len(parts) >= 3 {
			s.handleSearchGet(w, r, id, parts[2])
		}
	case "exports":
		s.handleExport(w, r, id)
	case "clean":
		s.handleJobClean(w, r, id)
	case "refresh":
		s.handleJobRefresh(w, r, id)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleJobGet(w http.ResponseWriter, r *http.Request, id string) {
	p, err := s.readJobProgress(id)
	if err != nil {
		http.Error(w, "job not found", 404)
		return
	}
	writeJSON(w, p)
}

func (s *Server) readJobProgress(id string) (map[string]any, error) {
	st, err := s.Engine.OpenJobStore(id)
	if err != nil {
		return nil, err
	}
	defer st.Close()
	row := st.DB.QueryRow(`SELECT id, original_name, state, stage, partial, created_utc, updated_utc,
		COALESCE(product_label,''), COALESCE(coverage_json,''), COALESCE(error_summary,''),
		COALESCE(progress_message,''), COALESCE(progress_current,''),
		COALESCE(files_done,0), COALESCE(bytes_done,0), COALESCE(sources_total,0), COALESCE(parse_done,0), COALESCE(elapsed_ms,0),
		COALESCE(events_committed,0), COALESCE(first_result_ms,0), COALESCE(counts_final,0), COALESCE(result_generation,1)
		FROM jobs WHERE id=?`, id)
	var jobID, name, state, stage, created, updated, product, cov, errSum, msg, current string
	var partial, filesDone, sourcesTotal, parseDone, eventsCommitted, countsFinal, generation int
	var bytesDone, elapsed, firstResultMS int64
	if err := row.Scan(&jobID, &name, &state, &stage, &partial, &created, &updated, &product, &cov, &errSum,
		&msg, &current, &filesDone, &bytesDone, &sourcesTotal, &parseDone, &elapsed,
		&eventsCommitted, &firstResultMS, &countsFinal, &generation); err != nil {
		return nil, err
	}
	pct := 5
	switch stage {
	case "intake":
		pct = 10 + filesDone/20
		if pct > 45 {
			pct = 45
		}
		if filesDone == 0 {
			pct = 5
		}
	case "profile":
		pct = 50
	case "parsing":
		if sourcesTotal > 0 {
			pct = 55 + (parseDone * 40 / sourcesTotal)
		} else {
			pct = 55
		}
	case "done":
		pct = 100
	}
	return map[string]any{
		"id": jobID, "job_id": jobID, "original_name": name, "state": state, "stage": stage, "partial": partial == 1,
		"created_utc": created, "updated_utc": updated, "product_label": product,
		"coverage_json": cov, "error_summary": errSum,
		"message": msg, "current_source": current,
		"files_done": filesDone, "bytes_done": bytesDone,
		"sources_total": sourcesTotal, "parse_done": parseDone,
		"elapsed_ms": elapsed, "percent": pct,
		"events_committed": eventsCommitted, "first_result_ms": firstResultMS,
		"counts_final": countsFinal == 1, "result_generation": generation,
	}, nil
}

func (s *Server) handleProgress(w http.ResponseWriter, r *http.Request, id string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "stream unsupported", 500)
		return
	}
	ch := s.Engine.Subscribe()
	defer s.Engine.Unsubscribe(ch)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	sendJob := func() {
		s.handleJobGetSSE(w, flusher, id)
	}
	sendJob()
	for {
		select {
		case <-r.Context().Done():
			return
		case p, ok := <-ch:
			if !ok {
				return
			}
			if p.JobID != id {
				continue
			}
			b, _ := json.Marshal(p)
			fmt.Fprintf(w, "data: %s\n\n", b)
			flusher.Flush()
		case <-ticker.C:
			sendJob()
		}
	}
}

func (s *Server) handleJobGetSSE(w http.ResponseWriter, flusher http.Flusher, id string) {
	p, err := s.readJobProgress(id)
	if err != nil {
		return
	}
	b, _ := json.Marshal(p)
	fmt.Fprintf(w, "data: %s\n\n", b)
	flusher.Flush()
}

func (s *Server) handleProfile(w http.ResponseWriter, r *http.Request, id string) {
	st, err := s.Engine.OpenJobStore(id)
	if err != nil {
		http.Error(w, "job not found", 404)
		return
	}
	defer st.Close()
	_, _ = jobs.CheckJobCompatibility(st, id)
	rows, err := st.DB.Query(`SELECT field_key, raw_value, normalized, confidence, status, source_id, line_start, line_end, selected, scope FROM attributes WHERE job_id=? ORDER BY field_key`, id)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	var items []map[string]any
	for rows.Next() {
		var field, raw, norm, conf, status, src, scope string
		var ls, le, sel int
		_ = rows.Scan(&field, &raw, &norm, &conf, &status, &src, &ls, &le, &sel, &scope)
		items = append(items, map[string]any{
			"field": field, "raw": raw, "normalized": norm, "confidence": conf, "status": status,
			"source_id": src, "line_start": ls, "line_end": le, "selected": sel == 1, "scope": scope,
		})
	}
	writeJSON(w, map[string]any{"items": items})
}

func (s *Server) handleCompatibility(w http.ResponseWriter, r *http.Request, id string) {
	st, err := s.Engine.OpenJobStore(id)
	if err != nil {
		http.Error(w, "job not found", 404)
		return
	}
	defer st.Close()
	compat, err := jobs.CheckJobCompatibility(st, id)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, compat)
}

func (s *Server) ensureCompatOnOpen(st *store.Store, id string) jobs.JobCompatibility {
	compat, _ := jobs.CheckJobCompatibility(st, id)
	if !compat.StaleClassifier {
		return compat
	}
	jobDir, err := s.Engine.Root.SafeJobDir(id)
	if err != nil {
		compat.RefreshFailed = true
		compat.Message = "Classifier is stale but job directory is unavailable; reanalyse the original snapshot."
		return compat
	}
	// Open-path: refresh kinds/versions only. Full findings rebuild stays on POST /refresh
	// so large retained analyses (e.g. SGVE) do not block Overview/Log Viewer.
	out, err := jobs.EnsureJobClassificationOnly(st, id, jobDir)
	if err != nil {
		if out.Message == "" {
			out.Message = err.Error()
		}
		out.RefreshFailed = true
	}
	return out
}

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request, id string) {
	st, err := s.Engine.OpenJobStore(id)
	if err != nil {
		http.Error(w, "job not found", 404)
		return
	}
	defer st.Close()
	compat := s.ensureCompatOnOpen(st, id)
	rows, err := st.DB.Query(`SELECT a.field_key, a.raw_value, a.normalized, a.confidence, a.status, a.source_id, a.line_start, a.line_end, a.selected, a.scope, COALESCE(s.raw_path,'')
		FROM attributes a LEFT JOIN sources s ON s.id=a.source_id WHERE a.job_id=? ORDER BY a.field_key`, id)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	var selected, history, components []map[string]any
	for rows.Next() {
		var field, raw, norm, conf, status, src, scope, srcPath string
		var ls, le, sel int
		_ = rows.Scan(&field, &raw, &norm, &conf, &status, &src, &ls, &le, &sel, &scope, &srcPath)
		item := map[string]any{
			"field": field, "raw": raw, "normalized": norm, "confidence": conf, "status": status,
			"source_id": src, "source_path": srcPath, "line_start": ls, "line_end": le, "selected": sel == 1, "scope": scope,
		}
		if strings.HasPrefix(field, "component.") {
			components = append(components, item)
			continue
		}
		if status == "historical" || status == "superseded" || status == "conflict" {
			history = append(history, item)
			continue
		}
		if sel == 1 {
			selected = append(selected, item)
		} else {
			history = append(history, item)
		}
	}
	sevCounts := map[string]int{}
	erows, err := st.DB.Query(`SELECT e.severity, COUNT(*) FROM events e JOIN sources s ON s.id=e.source_id WHERE e.job_id=? AND s.source_kind='log' GROUP BY e.severity`, id)
	if err == nil {
		defer erows.Close()
		for erows.Next() {
			var sev string
			var n int
			_ = erows.Scan(&sev, &n)
			sevCounts[sev] = n
		}
	}
	var eventTotal, groupApprox, undated int
	_ = st.DB.QueryRow(`SELECT COUNT(*) FROM events e JOIN sources s ON s.id=e.source_id WHERE e.job_id=? AND s.source_kind='log'`, id).Scan(&eventTotal)
	_ = st.DB.QueryRow(`SELECT COUNT(DISTINCT e.fingerprint) FROM events e JOIN sources s ON s.id=e.source_id WHERE e.job_id=? AND s.source_kind='log'`, id).Scan(&groupApprox)
	_ = st.DB.QueryRow(`SELECT COUNT(*) FROM events e JOIN sources s ON s.id=e.source_id WHERE e.job_id=? AND s.source_kind='log' AND (e.event_time_utc IS NULL OR e.event_time_utc='')`, id).Scan(&undated)

	var collRaw, obsStart, obsEnd, errSummary, errDetail string
	_ = st.DB.QueryRow(`SELECT COALESCE(collection_time_raw,''), COALESCE(observed_log_start,''), COALESCE(observed_log_end,''), COALESCE(error_summary,''), COALESCE(error_detail,'') FROM jobs WHERE id=?`, id).Scan(&collRaw, &obsStart, &obsEnd, &errSummary, &errDetail)
	if obsStart == "" || obsEnd == "" {
		_ = st.DB.QueryRow(`SELECT COALESCE(MIN(event_time_utc),''), COALESCE(MAX(event_time_utc),'') FROM events e JOIN sources s ON s.id=e.source_id WHERE e.job_id=? AND s.source_kind='log' AND e.event_time_utc IS NOT NULL AND e.event_time_utc!=''`, id).Scan(&obsStart, &obsEnd)
	}
	if collRaw == "" {
		for _, a := range selected {
			if a["field"] == "collection.time" {
				if v, ok := a["raw"].(string); ok {
					collRaw = v
				}
			}
		}
	}

	refLabel := "unavailable"
	refTime := time.Time{}
	if collRaw != "" {
		if t, err := time.Parse(time.RFC3339Nano, collRaw); err == nil {
			refTime = t
			refLabel = "collection_time"
		} else if t, err := time.Parse(time.RFC3339, collRaw); err == nil {
			refTime = t
			refLabel = "collection_time"
		}
	}
	if refTime.IsZero() && obsEnd != "" {
		if t, err := time.Parse(time.RFC3339Nano, obsEnd); err == nil {
			refTime = t
			refLabel = "latest_observed_event"
		}
	}
	from24 := ""
	to24 := ""
	if !refTime.IsZero() {
		from24 = refTime.Add(-24 * time.Hour).UTC().Format(time.RFC3339Nano)
		to24 = refTime.UTC().Format(time.RFC3339Nano)
	}

	recentSev := map[string]int{}
	var recentTotal, recentGroups, historicalTotal int
	if from24 != "" {
		_ = st.DB.QueryRow(`SELECT COUNT(*) FROM events e JOIN sources s ON s.id=e.source_id
			WHERE e.job_id=? AND s.source_kind='log' AND e.event_time_utc>=? AND e.event_time_utc<=?`, id, from24, to24).Scan(&recentTotal)
		_ = st.DB.QueryRow(`SELECT COUNT(DISTINCT e.fingerprint) FROM events e JOIN sources s ON s.id=e.source_id
			WHERE e.job_id=? AND s.source_kind='log' AND e.event_time_utc>=? AND e.event_time_utc<=?`, id, from24, to24).Scan(&recentGroups)
		_ = st.DB.QueryRow(`SELECT COUNT(*) FROM events e JOIN sources s ON s.id=e.source_id
			WHERE e.job_id=? AND s.source_kind='log' AND e.event_time_utc IS NOT NULL AND e.event_time_utc!='' AND e.event_time_utc<?`, id, from24).Scan(&historicalTotal)
		rsev, _ := st.DB.Query(`SELECT e.severity, COUNT(*) FROM events e JOIN sources s ON s.id=e.source_id
			WHERE e.job_id=? AND s.source_kind='log' AND e.event_time_utc>=? AND e.event_time_utc<=? GROUP BY e.severity`, id, from24, to24)
		if rsev != nil {
			defer rsev.Close()
			for rsev.Next() {
				var sev string
				var n int
				_ = rsev.Scan(&sev, &n)
				recentSev[sev] = n
			}
		}
	}

	var recent []map[string]any
	recentQ := `SELECT e.id, e.source_id, s.raw_path, e.severity, e.categories, e.message, e.line_start, e.line_end, e.raw_time, COALESCE(e.event_time_utc,'')
		FROM events e JOIN sources s ON s.id=e.source_id WHERE e.job_id=? AND s.source_kind='log'`
	recentArgs := []any{id}
	if from24 != "" {
		recentQ += ` AND e.event_time_utc>=? AND e.event_time_utc<=?`
		recentArgs = append(recentArgs, from24, to24)
	}
	recentQ += ` ORDER BY CASE WHEN e.event_time_utc IS NULL OR e.event_time_utc='' THEN 1 ELSE 0 END, e.event_time_utc DESC, s.raw_path, e.line_start LIMIT 8`
	rr, _ := st.DB.Query(recentQ, recentArgs...)
	if rr != nil {
		defer rr.Close()
		for rr.Next() {
			var eid, sid, path, sev, cats, msg, raw, evUTC string
			var ls, le int
			_ = rr.Scan(&eid, &sid, &path, &sev, &cats, &msg, &ls, &le, &raw, &evUTC)
			recent = append(recent, map[string]any{
				"event_id": eid, "source_id": sid, "path": path, "severity": sev, "categories": cats,
				"message": masking.Line(msg), "line_start": ls, "line_end": le, "raw_time": raw, "event_time_utc": evUTC,
			})
		}
	}

	var logSources, cfgSources, partialSources, undatedSources int
	_ = st.DB.QueryRow(`SELECT COUNT(*) FROM sources WHERE job_id=? AND source_kind='log' AND cache_path IS NOT NULL AND cache_path!=''`, id).Scan(&logSources)
	_ = st.DB.QueryRow(`SELECT COUNT(*) FROM sources WHERE job_id=? AND source_kind='configuration' AND cache_path IS NOT NULL AND cache_path!=''`, id).Scan(&cfgSources)
	_ = st.DB.QueryRow(`SELECT COUNT(*) FROM sources WHERE job_id=? AND coverage='partial'`, id).Scan(&partialSources)

	job, _ := s.readJobProgress(id)
	writeJSON(w, map[string]any{
		"job":        job,
		"selected":   selected,
		"components": components,
		"history":    history,
		"findings": map[string]any{
			"by_severity": sevCounts, "event_total": eventTotal, "group_total": groupApprox,
			"undated_total": undated, "label": "findings", "not_confirmed_incidents": true,
		},
		"recent_activity": map[string]any{
			"window": "24h", "ref_time": refTime.UTC().Format(time.RFC3339Nano), "ref_label": refLabel,
			"by_severity": recentSev, "event_total": recentTotal, "group_total": recentGroups,
			"historical_total": historicalTotal, "items": recent,
			"empty": len(recent) == 0,
		},
		"coverage_summary": map[string]any{
			"collection_time_raw": collRaw,
			"observed_log_start":  obsStart,
			"observed_log_end":    obsEnd,
			"latest_dated_entry":  obsEnd,
			"log_sources":         logSources,
			"configuration_sources": cfgSources,
			"partial_sources":     partialSources,
			"undated_sources":     undatedSources,
			"error_summary":       errSummary,
			"error_detail":        errDetail,
			"analysis_time":       job["updated_utc"],
		},
		"compatibility": compat,
	})
}

func (s *Server) withJobLease(w http.ResponseWriter, id, op string) bool {
	if s.Engine.Leases == nil {
		return true
	}
	if err := s.Engine.Leases.Acquire(id, op); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return false
	}
	return true
}

func (s *Server) handleFiles(w http.ResponseWriter, r *http.Request, id string) {
	if !storage.ValidJobID(id) {
		http.Error(w, "invalid job id", 400)
		return
	}
	if !s.withJobLease(w, id, jobs.OpRead) {
		return
	}
	defer s.Engine.Leases.Release(id, jobs.OpRead)
	st, err := s.Engine.OpenJobStore(id)
	if err != nil {
		http.Error(w, "job not found", 404)
		return
	}
	defer st.Close()
	compat := s.ensureCompatOnOpen(st, id)
	kindFilter := r.URL.Query().Get("kind")
	qFilter := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(qFilter) > 256 {
		qFilter = qFilter[:256]
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = s.Limits.PageDefault
	}
	if limit > s.Limits.PageMax {
		limit = s.Limits.PageMax
	}
	if offset < 0 {
		offset = 0
	}
	baseWhere := `s.job_id=?`
	args := []any{id}
	switch kindFilter {
	case "log":
		// Named/classified logs remain listable even when metadata_only / no cache
		// (binary, corrupt, limited). Openability is separate from inventory.
		baseWhere += ` AND s.source_kind='log'`
	case "configuration":
		baseWhere += ` AND s.source_kind='configuration' AND s.cache_path IS NOT NULL AND s.cache_path != ''`
	case "all":
	default:
		if kindFilter != "" {
			baseWhere += ` AND s.source_kind=?`
			args = append(args, kindFilter)
		} else {
			baseWhere += ` AND s.cache_path IS NOT NULL AND s.cache_path != ''`
		}
	}
	var totalFiles int
	_ = st.DB.QueryRow(`SELECT COUNT(*) FROM sources s WHERE `+baseWhere, args...).Scan(&totalFiles)

	matchWhere := baseWhere
	matchArgs := append([]any{}, args...)
	if qFilter != "" {
		// Literal substring; escape LIKE wildcards so %/_ are literal.
		like := "%" + escapeLike(strings.ToLower(qFilter)) + "%"
		matchWhere += ` AND lower(s.raw_path) LIKE ? ESCAPE '\'`
		matchArgs = append(matchArgs, like)
	}
	var matching int
	_ = st.DB.QueryRow(`SELECT COUNT(*) FROM sources s WHERE `+matchWhere, matchArgs...).Scan(&matching)

	q := `SELECT s.id, s.raw_path, s.coverage, s.line_count, COALESCE(s.source_kind,''), COALESCE(s.classify_reason,''), COALESCE(s.classify_confidence,''),
		COALESCE(s.cache_path,''),
		(SELECT COUNT(*) FROM events e WHERE e.source_id=s.id) AS event_count
		FROM sources s WHERE ` + matchWhere + ` ORDER BY s.raw_path, s.id LIMIT ? OFFSET ?`
	qArgs := append(append([]any{}, matchArgs...), limit, offset)
	rows, err := st.DB.Query(q, qArgs...)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	var items []map[string]any
	for rows.Next() {
		var sid, path, cov, sk, reason, conf, cache string
		var lines, events int
		_ = rows.Scan(&sid, &path, &cov, &lines, &sk, &reason, &conf, &cache, &events)
		hasCache := cache != ""
		openable := hasCache && cov != "metadata_only"
		items = append(items, map[string]any{
			"id": sid, "path": path, "coverage": cov, "line_count": lines, "event_count": events,
			"source_kind": sk, "classify_reason": reason, "classify_confidence": conf,
			"has_cache": hasCache, "openable": openable,
		})
	}
	writeJSON(w, map[string]any{
		"items": items, "kind": kindFilter, "q": qFilter,
		"offset": offset, "limit": limit,
		"total_files": totalFiles, "matching_files": matching,
		"truncated": offset+len(items) < matching,
		"compatibility": compat,
	})
}

func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

func (s *Server) handleJobDelete(w http.ResponseWriter, r *http.Request, id string) {
	jobDir, err := s.Engine.Root.SafeJobDir(id)
	if err != nil {
		http.Error(w, "invalid job id", 400)
		return
	}
	if !s.Engine.Root.JobExists(id) {
		http.Error(w, "job not found", 404)
		return
	}
	if s.Engine.ActiveJobID() == id {
		http.Error(w, "cannot delete active analysis", http.StatusConflict)
		return
	}
	if err := s.Engine.Leases.Acquire(id, jobs.OpDelete); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	defer s.Engine.Leases.Release(id, jobs.OpDelete)
	// Close any open handles by ensuring no store is held; RemoveAll the owned dir only.
	if err := os.RemoveAll(jobDir); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, map[string]any{
		"deleted": id,
		"removed": []string{"input/snapshot.bin", "evidence", "results.sqlite", "searches", "exports", "temporary"},
		"note":    "Removed the internal retained upload copy and analysis data under the selected storage root. The user's original snapshot file outside this root was not modified.",
	})
}

func (s *Server) handleJobClean(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	jobDir, err := s.Engine.Root.SafeJobDir(id)
	if err != nil || !s.Engine.Root.JobExists(id) {
		http.Error(w, "job not found", 404)
		return
	}
	if err := s.Engine.Leases.Acquire(id, jobs.OpClean); err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	defer s.Engine.Leases.Release(id, jobs.OpClean)
	before := jobs.DisposableSearchBytes(jobDir)
	if err := jobs.ClearDisposable(jobDir); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, map[string]any{
		"cleaned": id, "reclaimed_bytes_estimate": before,
		"kept": []string{"input/snapshot.bin", "evidence", "results.sqlite profile/findings"},
		"note": "Removed disposable search/export/temporary data only. Retained upload copy and evidence remain reopenable.",
	})
}

func (s *Server) handleJobRefresh(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	jobDir, err := s.Engine.Root.SafeJobDir(id)
	if err != nil {
		http.Error(w, "invalid job id", 400)
		return
	}
	if err := s.Engine.Leases.Acquire(id, jobs.OpRefresh); err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	defer s.Engine.Leases.Release(id, jobs.OpRefresh)
	st, err := s.Engine.OpenJobStore(id)
	if err != nil {
		http.Error(w, "job not found", 404)
		return
	}
	defer st.Close()
	compat, err := jobs.EnsureJobCompatibility(st, id, jobDir)
	if err != nil {
		writeJSON(w, map[string]any{"compatibility": compat, "error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"compatibility": compat})
}

func storageInfo(m *storage.Manager) map[string]any {
	info := m.Info()
	root := m.Root
	info["temporary_or_test_root"] = isTempRoot(root)
	var retained int64
	var analyses int
	entries, _ := os.ReadDir(m.JobsDir())
	for _, ent := range entries {
		if !ent.IsDir() || !storage.ValidJobID(ent.Name()) {
			continue
		}
		dir, err := m.SafeJobDir(ent.Name())
		if err != nil {
			continue
		}
		n, _ := jobs.JobDiskUsage(dir)
		retained += n
		analyses++
	}
	info["retained_analyses"] = analyses
	info["retained_bytes"] = retained
	info["ram_note"] = "Process RAM is measured separately from retained disk usage under this root."
	return info
}

func isTempRoot(root string) bool {
	l := strings.ToLower(filepath.Clean(root))
	return strings.Contains(l, string(filepath.Separator)+"temp"+string(filepath.Separator)) ||
		strings.Contains(l, string(filepath.Separator)+"tmp"+string(filepath.Separator)) ||
		strings.Contains(l, `\appdata\local\temp\`) ||
		strings.Contains(l, "/tmp/") ||
		strings.Contains(l, "qa-browser-")
}

func (s *Server) handleCoverage(w http.ResponseWriter, r *http.Request, id string) {
	st, err := s.Engine.OpenJobStore(id)
	if err != nil {
		http.Error(w, "job not found", 404)
		return
	}
	defer st.Close()
	_, _ = jobs.CheckJobCompatibility(st, id)
	rows, err := st.DB.Query(`SELECT id, COALESCE(source_id,''), code, COALESCE(severity,''), reason FROM coverage_issues WHERE job_id=?`, id)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	var items []map[string]any
	for rows.Next() {
		var iid, sid, code, sev, reason string
		_ = rows.Scan(&iid, &sid, &code, &sev, &reason)
		items = append(items, map[string]any{"id": iid, "source_id": sid, "code": code, "severity": sev, "reason": reason})
	}
	srows, err := st.DB.Query(`SELECT id, raw_path, kind, COALESCE(source_kind,''), coverage, line_count, size_bytes, COALESCE(classify_reason,''), COALESCE(classify_confidence,'')
		FROM sources WHERE job_id=? ORDER BY source_kind, raw_path`, id)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer srows.Close()
	var sources []map[string]any
	kindCounts := map[string]int{}
	for srows.Next() {
		var sid, path, kind, sk, cov, reason, conf string
		var lines int
		var size int64
		_ = srows.Scan(&sid, &path, &kind, &sk, &cov, &lines, &size, &reason, &conf)
		if sk == "" {
			sk = "unknown"
		}
		kindCounts[sk]++
		sources = append(sources, map[string]any{
			"id": sid, "path": path, "archive_kind": kind, "source_kind": sk, "coverage": cov,
			"line_count": lines, "size_bytes": size, "classify_reason": reason, "classify_confidence": conf,
		})
	}
	writeJSON(w, map[string]any{"items": items, "sources": sources, "kind_counts": kindCounts})
}

func (s *Server) handleSources(w http.ResponseWriter, r *http.Request, id string) {
	s.handleFiles(w, r, id)
}

func (s *Server) handleContext(w http.ResponseWriter, r *http.Request, jobID, sourceID string) {
	if !s.withJobLease(w, jobID, jobs.OpRead) {
		return
	}
	defer s.Engine.Leases.Release(jobID, jobs.OpRead)
	st, err := s.Engine.OpenJobStore(jobID)
	if err != nil {
		http.Error(w, "job not found", 404)
		return
	}
	defer st.Close()
	var path, cache, cov, sk string
	var lineCount int
	err = st.DB.QueryRow(`SELECT raw_path, cache_path, coverage, COALESCE(source_kind,''), COALESCE(line_count,0) FROM sources WHERE id=? AND job_id=?`, sourceID, jobID).Scan(&path, &cache, &cov, &sk, &lineCount)
	if err != nil || cache == "" {
		http.Error(w, "source unavailable", 404)
		return
	}
	abs := jobs.ResolveCachePath(s.Engine.Root.JobDir(jobID), cache)
	start, _ := strconv.Atoi(r.URL.Query().Get("start"))
	end, _ := strconv.Atoi(r.URL.Query().Get("end"))
	if start < 1 {
		start = 1
	}
	if end < start {
		end = start + 50
	}
	if end-start > s.Limits.ContextLinesMax {
		end = start + s.Limits.ContextLinesMax
	}
	var checkpoints []evidence.Checkpoint
	crows, _ := st.DB.Query(`SELECT line_no, byte_offset FROM line_checkpoints WHERE source_id=? ORDER BY line_no`, sourceID)
	if crows != nil {
		defer crows.Close()
		for crows.Next() {
			var ln int
			var off int64
			if crows.Scan(&ln, &off) == nil {
				checkpoints = append(checkpoints, evidence.Checkpoint{Line: ln, Offset: off})
			}
		}
	}
	lines, err := evidence.ReadLinesFrom(abs, start, end, s.Limits.ContextBytesMax, checkpoints)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	masked := make([]string, len(lines))
	for i, ln := range lines {
		masked[i] = masking.Line(ln)
	}
	actualStart := start
	actualEnd := start - 1
	if len(masked) > 0 {
		actualEnd = start + len(masked) - 1
	}
	eof := lineCount > 0 && actualEnd >= lineCount
	if len(masked) == 0 && start > lineCount && lineCount > 0 {
		eof = true
	}
	usedCP, usedOK := evidence.NearestCheckpoint(checkpoints, start)
	writeJSON(w, map[string]any{
		"path": path, "start": actualStart, "end": actualEnd, "lines": masked,
		"requested_start": start, "requested_end": end,
		"coverage": cov, "source_kind": sk, "line_count": lineCount,
		"truncated": actualEnd < lineCount || cov == "partial",
		"eof": eof,
		"checkpoint_used": usedOK, "checkpoint_line": usedCP.Line, "checkpoint_offset": usedCP.Offset,
		"checkpoints_available": len(checkpoints),
	})
}

func (s *Server) handleSearchStart(w http.ResponseWriter, r *http.Request, jobID string) {
	var body struct {
		SourceID      string `json:"source_id"`
		Query         string `json:"query"`
		CaseSensitive bool   `json:"case_sensitive"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if !s.withJobLease(w, jobID, jobs.OpSearch) {
		return
	}
	defer s.Engine.Leases.Release(jobID, jobs.OpSearch)
	st, err := s.Engine.OpenJobStore(jobID)
	if err != nil {
		http.Error(w, "job not found", 404)
		return
	}
	defer st.Close()
	var cache, cov string
	if err := st.DB.QueryRow(`SELECT cache_path, coverage FROM sources WHERE id=? AND job_id=?`, body.SourceID, jobID).Scan(&cache, &cov); err != nil || cache == "" {
		http.Error(w, "source unavailable", 404)
		return
	}
	abs := jobs.ResolveCachePath(s.Engine.Root.JobDir(jobID), cache)
	if abs == "" || !fileExistsAbs(abs) {
		http.Error(w, "source evidence unavailable", 404)
		return
	}
	sid, err := search.Start(r.Context(), st, jobID, body.SourceID, abs, body.Query, body.CaseSensitive, cov)
	if err != nil {
		if errors.Is(err, search.ErrEmptyQuery) {
			http.Error(w, err.Error(), 400)
			return
		}
		// Preserve underlying store/scan errors instead of masking as generic client faults.
		http.Error(w, err.Error(), 500)
		return
	}
	res, gerr := search.GetForJob(st, jobID, sid)
	if gerr != nil {
		http.Error(w, "search started but result unavailable: "+gerr.Error(), 500)
		return
	}
	writeJSON(w, map[string]any{
		"id":                         sid,
		"state":                      res.State,
		"matching_lines_found":       res.MatchingLines,
		"keyword_occurrences_found":  res.Occurrences,
		"count_is_final":             res.CountIsFinal,
		"scope_complete":             res.ScopeComplete,
		"error":                      res.Error,
	})
}

func (s *Server) handleSearchGet(w http.ResponseWriter, r *http.Request, jobID, searchID string) {
	st, err := s.Engine.OpenJobStore(jobID)
	if err != nil {
		http.Error(w, "job not found", 404)
		return
	}
	defer st.Close()
	res, err := search.GetForJob(st, jobID, searchID)
	if err != nil {
		if errors.Is(err, search.ErrNotFound) {
			http.Error(w, err.Error(), 404)
			return
		}
		http.Error(w, err.Error(), 500)
		return
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = s.Limits.PageDefault
	}
	if limit > s.Limits.PageMax {
		limit = s.Limits.PageMax
	}
	hits, err := search.Page(st, searchID, offset, limit)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, map[string]any{"search": res, "items": hits, "offset": offset, "limit": limit})
}

func fileExistsAbs(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request, id string) {
	if !s.withJobLease(w, id, jobs.OpExport) {
		return
	}
	defer s.Engine.Leases.Release(id, jobs.OpExport)
	st, err := s.Engine.OpenJobStore(id)
	if err != nil {
		http.Error(w, "job not found", 404)
		return
	}
	defer st.Close()
	outDir := filepath.Join(s.Root.JobDir(id), "exports")
	_ = platform.EnsureDir(outDir)
	path, err := export.WriteJSON(st, id, outDir)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", "attachment; filename=\"quickanalyser-export.json\"")
	_, _ = io.Copy(w, f)
}

func (s *Server) handleQuit(w http.ResponseWriter, r *http.Request) {
	if s.Log != nil {
		s.Log.Info("shutdown.requested", map[string]any{})
	}
	writeJSON(w, map[string]string{"status": "quitting"})
	go func() {
		time.Sleep(200 * time.Millisecond)
		_ = s.Shutdown()
	}()
}

func (s *Server) handleDiagnostics(w http.ResponseWriter, r *http.Request) {
	if s.Log == nil {
		http.Error(w, "diagnostics unavailable", 503)
		return
	}
	switch r.Method {
	case http.MethodGet:
		st := s.Log.Status()
		st["open_folder"] = s.Log.Dir()
		writeJSON(w, st)
	case http.MethodPost:
		var body struct {
			Action  string `json:"action"`
			Level   string `json:"level"`
			Minutes int    `json:"minutes"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		switch strings.ToLower(body.Action) {
		case "enable_debug":
			ttl := time.Duration(body.Minutes) * time.Minute
			s.Log.EnableDebug(ttl)
			s.Log.Info("diagnostics.debug_enabled", map[string]any{})
		case "disable_debug":
			s.Log.DisableDebug()
			s.Log.Info("diagnostics.debug_disabled", map[string]any{})
		case "set_level":
			if strings.EqualFold(body.Level, "debug") {
				ttl := time.Duration(body.Minutes) * time.Minute
				s.Log.EnableDebug(ttl)
			} else {
				s.Log.DisableDebug()
			}
		default:
			http.Error(w, "unknown action", 400)
			return
		}
		writeJSON(w, s.Log.Status())
	default:
		http.Error(w, "method not allowed", 405)
	}
}

func (s *Server) handleDiagnosticsBundle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	if s.Log == nil {
		http.Error(w, "diagnostics unavailable", 503)
		return
	}
	path, err := s.Log.CreateBundle()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.Log.Info("diagnostics.bundle_created", map[string]any{})
	writeJSON(w, map[string]any{
		"path":    path,
		"message": "Local redacted support bundle created. Nothing was uploaded.",
	})
}

func (s *Server) handleDiagnosticsClient(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	if s.Log == nil {
		http.Error(w, "diagnostics unavailable", 503)
		return
	}
	s.clientDiag.mu.Lock()
	if time.Since(s.clientDiag.window) > time.Minute {
		s.clientDiag.window = time.Now()
		s.clientDiag.count = 0
	}
	s.clientDiag.count++
	n := s.clientDiag.count
	s.clientDiag.mu.Unlock()
	if n > 60 {
		http.Error(w, "client diagnostic rate limited", 429)
		return
	}
	var body struct {
		Code    string `json:"code"`
		JobID   string `json:"job_id"`
		Source  string `json:"source_id"`
		Tab     string `json:"tab"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 2048)).Decode(&body); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	code := strings.TrimSpace(body.Code)
	if code == "" || len(code) > 64 {
		http.Error(w, "invalid code", 400)
		return
	}
	// Allowlisted client codes only.
	allowed := map[string]bool{
		"ui.stale_discarded": true,
		"ui.view_switched":   true,
		"ui.fetch_failed":    true,
		"ui.tab_changed":     true,
	}
	if !allowed[code] {
		http.Error(w, "code not allowlisted", 400)
		return
	}
	s.Log.Info(code, map[string]any{
		"job_id":    body.JobID,
		"source_id": body.Source,
		"tab":       body.Tab,
		"category":  "client",
	})
	writeJSON(w, map[string]string{"status": "ok"})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(true)
	_ = enc.Encode(v)
}
