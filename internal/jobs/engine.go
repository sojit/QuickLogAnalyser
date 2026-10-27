package jobs

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"quickanalyser/internal/adapters/nx"
	"quickanalyser/internal/adapters/vsg"
	"quickanalyser/internal/appinfo"
	"quickanalyser/internal/archive"
	"quickanalyser/internal/classify"
	"quickanalyser/internal/config"
	"quickanalyser/internal/detection"
	"quickanalyser/internal/diag"
	"quickanalyser/internal/evidence"
	"quickanalyser/internal/platform"
	"quickanalyser/internal/profile"
	"quickanalyser/internal/store"
	"quickanalyser/internal/storage"
	"quickanalyser/internal/timeparse"
)

type Progress struct {
	JobID            string `json:"job_id"`
	State            string `json:"state"`
	Stage            string `json:"stage"`
	Message          string `json:"message"`
	Current          string `json:"current_source,omitempty"`
	FilesDone        int    `json:"files_done"`
	FilesTotal       int    `json:"files_total,omitempty"`
	BytesDone        int64  `json:"bytes_done"`
	SourcesTotal     int    `json:"sources_total"`
	ParseDone        int    `json:"parse_done"`
	Partial          bool   `json:"partial"`
	ElapsedMS        int64  `json:"elapsed_ms"`
	Percent          int    `json:"percent"`
	EventsCommitted  int    `json:"events_committed"`
	CountsFinal      bool   `json:"counts_final"`
	FirstResultMS    int64  `json:"first_result_ms,omitempty"`
	ResultGeneration int    `json:"result_generation,omitempty"`
}

type Engine struct {
	Root    *storage.Manager
	Limits  config.Limits
	Leases  *LeaseTracker
	mu      sync.Mutex
	active  string
	cancels map[string]context.CancelFunc
	subs    map[chan Progress]struct{}
}

// CreateOpts controls upload handling.
type CreateOpts struct {
	ForceReanalyse bool
}

func NewEngine(root *storage.Manager, limits config.Limits) *Engine {
	e := &Engine{
		Root:    root,
		Limits:  limits,
		Leases:  NewLeaseTracker(),
		cancels: map[string]context.CancelFunc{},
		subs:    map[chan Progress]struct{}{},
	}
	_ = e.RecoverInterruptedJobs()
	return e
}

// RecoverInterruptedJobs marks non-terminal jobs left by a previous process crash/restart.
func (e *Engine) RecoverInterruptedJobs() error {
	entries, err := os.ReadDir(e.Root.JobsDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, ent := range entries {
		if !ent.IsDir() || !storage.ValidJobID(ent.Name()) {
			continue
		}
		dir, err := e.Root.SafeJobDir(ent.Name())
		if err != nil {
			continue
		}
		st, err := store.Open(filepath.Join(dir, "results.sqlite"))
		if err != nil {
			continue
		}
		var state string
		err = st.DB.QueryRow(`SELECT state FROM jobs WHERE id=?`, ent.Name()).Scan(&state)
		if err == nil && state != "" && state != "completed" && state != "partial" && state != "cancelled" && state != "failed" && state != "interrupted" {
			_, _ = st.DB.Exec(`UPDATE jobs SET state=?, stage=?, partial=1, progress_message=?, updated_utc=? WHERE id=?`,
				"interrupted", "done", "Analysis interrupted by application restart; partial results may be available.", now, ent.Name())
		}
		_ = st.Close()
	}
	return nil
}

func (e *Engine) Subscribe() chan Progress {
	ch := make(chan Progress, 16)
	e.mu.Lock()
	e.subs[ch] = struct{}{}
	e.mu.Unlock()
	return ch
}

func (e *Engine) Unsubscribe(ch chan Progress) {
	e.mu.Lock()
	delete(e.subs, ch)
	e.mu.Unlock()
	close(ch)
}

func (e *Engine) emit(p Progress) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for ch := range e.subs {
		select {
		case ch <- p:
		default:
		}
	}
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func (e *Engine) CreateJob(originalName string, size int64, src io.Reader) (string, error) {
	res, err := e.CreateJobOpts(originalName, size, src, CreateOpts{})
	if err != nil {
		return "", err
	}
	return res.ID, nil
}

// CreateJobResult is returned when an upload is accepted or reused.
type CreateJobResult struct {
	ID             string           `json:"id"`
	Reused         bool             `json:"reused"`
	State          string           `json:"state"`
	Compatible     *CompatibleReuse `json:"compatible_match,omitempty"`
	NeedsChoice    bool             `json:"needs_choice,omitempty"`
	Message        string           `json:"message,omitempty"`
}

func (e *Engine) CreateJobOpts(originalName string, size int64, src io.Reader, opts CreateOpts) (CreateJobResult, error) {
	stagingHeld := false
	if e.Leases != nil {
		if err := e.Leases.AcquireStaging(); err != nil {
			return CreateJobResult{}, err
		}
		stagingHeld = true
		defer func() {
			if stagingHeld {
				e.Leases.ReleaseStaging()
			}
		}()
	}
	// Stage into root temporary while hashing — before allocating a permanent job.
	stageRoot := filepath.Join(e.Root.Root, "temporary", "uploads")
	if err := platform.EnsureDir(stageRoot); err != nil {
		return CreateJobResult{}, err
	}
	stageID := newID()
	stagePart := filepath.Join(stageRoot, stageID+".part")
	f, err := os.Create(stagePart)
	if err != nil {
		return CreateJobResult{}, err
	}
	h := sha256.New()
	lr := &io.LimitedReader{R: src, N: e.Limits.MaxUploadBytes + 1}
	written, err := io.Copy(io.MultiWriter(f, h), lr)
	_ = f.Close()
	if err != nil {
		_ = os.Remove(stagePart)
		return CreateJobResult{}, err
	}
	if written > e.Limits.MaxUploadBytes {
		_ = os.Remove(stagePart)
		return CreateJobResult{}, fmt.Errorf("upload exceeds configured size limit")
	}
	sum := hex.EncodeToString(h.Sum(nil))

	if !opts.ForceReanalyse {
		match, merr := e.FindCompatibleByHash(sum)
		if merr == nil && match != nil && match.Compatible {
			_ = os.Remove(stagePart)
			logReuse("history.reuse_hit", match.JobID, match.Reason)
			return CreateJobResult{
				ID: match.JobID, Reused: true, State: match.State, Compatible: match,
				Message: "Already analysed — opened saved results for identical archive bytes",
			}, nil
		}
		if merr == nil && match != nil && !match.Compatible {
			_ = os.Remove(stagePart)
			logReuse("history.reuse_rejected", match.JobID, match.Reason)
			return CreateJobResult{
				ID: match.JobID, Reused: false, NeedsChoice: true, Compatible: match, State: match.State,
				Message: "A saved analysis with the same archive bytes exists but is not fully compatible (" + match.Reason + "). Open saved results or reanalyse explicitly.",
			}, nil
		}
		logReuse("history.reuse_miss", "", "no compatible hash match")
	}

	e.mu.Lock()
	if e.active != "" {
		e.mu.Unlock()
		_ = os.Remove(stagePart)
		return CreateJobResult{}, fmt.Errorf("another analysis is already active")
	}
	id := newID()
	e.active = id
	e.mu.Unlock()

	e.Root.SetBusy(1)
	if stagingHeld && e.Leases != nil {
		if err := e.Leases.TransitionStagingToAnalyse(id); err != nil {
			_ = os.Remove(stagePart)
			e.finishActive(id)
			return CreateJobResult{}, err
		}
		stagingHeld = false
	} else if e.Leases != nil {
		if err := e.Leases.Acquire(id, OpAnalyse); err != nil {
			_ = os.Remove(stagePart)
			e.finishActive(id)
			return CreateJobResult{}, err
		}
	}
	jobDir, err := e.Root.SafeJobDir(id)
	if err != nil {
		// freshly minted IDs are always valid; fall back
		jobDir = e.Root.JobDir(id)
	}
	for _, sub := range []string{"input", "temporary", "evidence", "searches", "exports"} {
		if err := platform.EnsureDir(filepath.Join(jobDir, sub)); err != nil {
			_ = os.Remove(stagePart)
			e.finishActive(id)
			return CreateJobResult{}, err
		}
	}
	final := filepath.Join(jobDir, "input", "snapshot.bin")
	if err := os.Rename(stagePart, final); err != nil {
		// cross-device rename may fail; copy then remove
		if err := copyFile(stagePart, final); err != nil {
			_ = os.Remove(stagePart)
			e.finishActive(id)
			return CreateJobResult{}, err
		}
		_ = os.Remove(stagePart)
	}
	st, err := store.Open(filepath.Join(jobDir, "results.sqlite"))
	if err != nil {
		e.finishActive(id)
		return CreateJobResult{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = st.DB.Exec(`INSERT INTO jobs(id, original_name, sha256, size_bytes, state, stage, created_utc, updated_utc, app_version, rule_version)
		VALUES(?,?,?,?,?,?,?,?,?,?)`, id, originalName, sum, written, "queued", "intake", now, now, appinfo.Version(), detection.RuleBundleVersion)
	_ = st.Close()
	if err != nil {
		e.finishActive(id)
		return CreateJobResult{}, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	e.mu.Lock()
	e.cancels[id] = cancel
	e.mu.Unlock()
	go e.run(ctx, id, final, originalName)
	return CreateJobResult{ID: id, Reused: false, State: "queued"}, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	cerr := out.Close()
	if err != nil {
		return err
	}
	return cerr
}

func (e *Engine) Cancel(id string) {
	e.mu.Lock()
	if c, ok := e.cancels[id]; ok {
		c()
	}
	e.mu.Unlock()
}

// ActiveJobID returns the in-flight analysis ID, if any.
func (e *Engine) ActiveJobID() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.active
}

func (e *Engine) finishActive(id string) {
	e.mu.Lock()
	if e.active == id {
		e.active = ""
	}
	delete(e.cancels, id)
	e.mu.Unlock()
	if e.Leases != nil {
		e.Leases.Release(id, OpAnalyse)
	}
	e.Root.SetBusy(-1)
}

func (e *Engine) run(ctx context.Context, id, archivePath, originalName string) {
	defer e.finishActive(id)
	started := time.Now()
	jobDir := e.Root.JobDir(id)
	st, err := store.Open(filepath.Join(jobDir, "results.sqlite"))
	if err != nil {
		return
	}
	defer st.Close()

	var sourcesTotal, parseDone, eventsCommitted int
	var firstResultMS int64
	countsFinal := false
	lastEmit := time.Time{}
	lastLoggedStage := ""
	lastStallCheck := time.Now()
	setState := func(state, stage, msg string, partial bool, files int, bytes int64, current string, force bool) {
		elapsed := time.Since(started).Milliseconds()
		pct := stagePercent(stage, files, sourcesTotal, parseDone)
		now := time.Now().UTC().Format(time.RFC3339)
		_, _ = st.DB.Exec(`UPDATE jobs SET state=?, stage=?, updated_utc=?, partial=?, progress_message=?, progress_current=?,
			files_done=?, bytes_done=?, sources_total=?, parse_done=?, elapsed_ms=?, events_committed=?, first_result_ms=?, counts_final=? WHERE id=?`,
			state, stage, now, boolInt(partial), msg, current, files, bytes, sourcesTotal, parseDone, elapsed, eventsCommitted, firstResultMS, boolInt(countsFinal), id)
		if force || lastEmit.IsZero() || time.Since(lastEmit) >= 250*time.Millisecond {
			lastEmit = time.Now()
			e.emit(Progress{
				JobID: id, State: state, Stage: stage, Message: msg, Current: current,
				FilesDone: files, BytesDone: bytes, SourcesTotal: sourcesTotal, ParseDone: parseDone,
				Partial: partial, ElapsedMS: elapsed, Percent: pct,
				EventsCommitted: eventsCommitted, CountsFinal: countsFinal, FirstResultMS: firstResultMS,
				ResultGeneration: 1,
			})
		}
		// Low-volume correlated stage diagnostics (no customer paths / raw messages).
		if stage != lastLoggedStage || state == "failed" || state == "cancelled" || stage == "done" {
			lastLoggedStage = stage
			attrs := map[string]any{
				"job_id":            id,
				"stage":             stage,
				"result_generation": 1,
				"files_done":        files,
				"sources_total":     sourcesTotal,
				"parse_done":        parseDone,
				"events_committed":  eventsCommitted,
				"elapsed_ms":        elapsed,
				"partial":           partial,
			}
			code := "job.stage"
			switch {
			case state == "cancelled":
				code = "job.cancelled"
				attrs["cancelled"] = true
				attrs["reason_code"] = "user_or_context_cancel"
			case state == "failed":
				code = "job.error"
				attrs["error_code"] = "analysis_failed"
			case stage == "done":
				code = "job.complete"
			}
			diag.Instance().Info(code, attrs)
			lastStallCheck = time.Now()
		} else if time.Since(lastStallCheck) >= 60*time.Second {
			lastStallCheck = time.Now()
			diag.Instance().Warn("job.stall_sample", map[string]any{
				"job_id": id, "stage": stage, "elapsed_ms": elapsed,
				"parse_done": parseDone, "sources_total": sourcesTotal, "stalled": true,
			})
		}
	}

	setState("ingesting", "intake", "Inventorying archive", false, 0, 0, "", true)
	f, err := os.Open(archivePath)
	if err != nil {
		setState("failed", "intake", err.Error(), true, 0, 0, "", true)
		return
	}
	defer f.Close()

	ew := &evidence.Writer{Dir: filepath.Join(jobDir, "evidence"), Limits: e.Limits}
	walker := &archive.Walker{Limits: e.Limits}
	partial := false
	walker.OnBudgetExceed = func(reason string) {
		partial = true
		_, _ = st.DB.Exec(`INSERT INTO coverage_issues(id, job_id, code, severity, reason) VALUES(?,?,?,?,?)`,
			newID(), id, "budget", "warning", reason)
	}

	var sources []profile.SourceInfo
	files := 0
	err = walker.Walk(ctx, f, sanitizeName(originalName), 1, func(ctx context.Context, m archive.Member, r io.Reader) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if e.Limits.TotalAppDataBudget > 0 && walker.ExpandedBytes+ew.Used > e.Limits.TotalAppDataBudget {
			partial = true
			_, _ = st.DB.Exec(`INSERT INTO coverage_issues(id, job_id, code, severity, reason) VALUES(?,?,?,?,?)`,
				newID(), id, "app_data_budget", "warning", "total application data budget exceeded")
			return fmt.Errorf("total application data budget exceeded")
		}
		if m.Kind != archive.KindRegular || r == nil {
			if m.Kind == archive.KindLink {
				_, _ = st.DB.Exec(`INSERT INTO coverage_issues(id, job_id, code, severity, reason) VALUES(?,?,?,?,?)`,
					newID(), id, "skipped_link", "info", "skipped link: "+m.Path)
			}
			return nil
		}
		sid := newID()
		cov := "inventoried"
		cachePath := ""
		lineCount := 0
		size := m.Size
		decoded, closer, isGzip, gerr := evidence.DecodeMaybeGzip(r, m.Path, nil)
		if gerr != nil {
			partial = true
			_, _ = st.DB.Exec(`INSERT INTO coverage_issues(id, job_id, source_id, code, severity, reason) VALUES(?,?,?,?,?,?)`,
				newID(), id, sid, "gzip_decode", "warning", gerr.Error())
			decoded = r
		}
		if closer != nil {
			defer closer.Close()
		}
		sample, rest, serr := evidence.SampleAndRest(decoded, 4096)
		if serr != nil {
			partial = true
			_, _ = st.DB.Exec(`INSERT INTO coverage_issues(id, job_id, source_id, code, severity, reason) VALUES(?,?,?,?,?,?)`,
				newID(), id, sid, "sample_read", "warning", serr.Error())
			rest = decoded
		}
		cls := classify.ClassifyMember(m.Path, sample, isGzip)
		keepText := cls.KeepText
		var cs *evidence.CachedSource
		if keepText {
			var werr error
			cs, werr = ew.WriteMember(sid, m.Path, rest)
			if werr != nil {
				partial = true
				_, _ = st.DB.Exec(`INSERT INTO coverage_issues(id, job_id, source_id, code, severity, reason) VALUES(?,?,?,?,?,?)`,
					newID(), id, sid, "evidence_write", "warning", werr.Error())
			}
			if cs != nil {
				cachePath = evidence.RelativeCachePath(jobDir, cs.CachePath)
				absCache := cs.CachePath
				lineCount = cs.LineCount
				size = cs.Bytes
				cov = cs.Coverage
				if isGzip {
					cs.DecodedGzip = true
				}
				if cs.Truncated {
					partial = true
				}
				sources = append(sources, profile.SourceInfo{
					ID: sid, RawPath: m.Path, CachePath: absCache, SourceKind: string(cls.Kind),
				})
				for _, cp := range cs.Checkpoints {
					_, _ = st.DB.Exec(`INSERT OR REPLACE INTO line_checkpoints(source_id, line_no, byte_offset) VALUES(?,?,?)`,
						sid, cp.Line, cp.Offset)
				}
			}
		} else {
			_, _ = io.Copy(io.Discard, io.LimitReader(rest, 64))
			cov = "metadata_only"
			if cls.Kind == classify.KindBinary {
				_, _ = st.DB.Exec(`INSERT INTO coverage_issues(id, job_id, source_id, code, severity, reason) VALUES(?,?,?,?,?,?)`,
					newID(), id, sid, "binary_skipped", "info", "binary/non-text member not fully decoded: "+m.Path)
			} else if cls.Kind == classify.KindLog {
				_, _ = st.DB.Exec(`INSERT INTO coverage_issues(id, job_id, source_id, code, severity, reason) VALUES(?,?,?,?,?,?)`,
					newID(), id, sid, "named_log_unreadable", "info",
					"named log listed but not decoded as text ("+cls.Reason+"): "+m.Path)
			}
		}
		contentSHA := ""
		contentBytes := size
		if cs != nil {
			contentSHA = cs.ContentSHA256
			contentBytes = cs.Bytes
		}
		_, _ = st.DB.Exec(`INSERT INTO sources(id, job_id, raw_path, kind, cache_path, line_count, coverage, size_bytes, source_kind, classify_reason, classify_confidence, classify_version, content_sha256, content_bytes)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			sid, id, m.Path, string(m.Kind), cachePath, lineCount, cov, size,
			string(cls.Kind), cls.Reason, cls.Confidence, classify.Version, contentSHA, contentBytes)
		files++
		if files%10 == 0 {
			setState("ingesting", "intake", "Caching evidence", partial, files, walker.ExpandedBytes, m.Path, false)
		}
		return nil
	})
	if ctx.Err() != nil {
		setState("cancelled", "intake", "Cancelled during intake", true, files, walker.ExpandedBytes, "", true)
		return
	}
	if err != nil {
		partial = true
		_, _ = st.DB.Exec(`UPDATE jobs SET error_summary=?, error_detail=? WHERE id=?`, err.Error(), err.Error(), id)
		setState("partial", "intake", err.Error(), true, files, walker.ExpandedBytes, "", true)
		if files == 0 {
			_, _ = st.DB.Exec(`UPDATE jobs SET state=?, stage=?, partial=1, updated_utc=? WHERE id=?`,
				"failed", "done", time.Now().UTC().Format(time.RFC3339), id)
			setState("failed", "done", "Unreadable input: "+err.Error(), true, 0, walker.ExpandedBytes, "", true)
			return
		}
	}

	setState("profiling", "profile", "Extracting profile observations", partial, files, walker.ExpandedBytes, "", true)
	obs := profile.Resolve(append(append(profile.ObserveGeneric(sources), nx.Enrich(sources)...), vsg.Enrich(sources)...))
	productLabel := "Unknown product — generic analysis"
	for _, o := range obs {
		sel := 0
		if o.Status != "superseded" && o.Status != "historical" && !profile.OperationalField(o.Field) {
			sel = 1
		}
		_, _ = st.DB.Exec(`INSERT INTO attributes(id, job_id, field_key, raw_value, normalized, confidence, status, source_id, line_start, line_end, selected, scope)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, newID(), id, o.Field, o.Raw, o.Normalized, o.Confidence, o.Status, o.SourceID, o.LineStart, o.LineEnd, sel, o.Scope)
		if sel == 1 && (o.Field == "product.identity" || o.Field == "product.family") {
			productLabel = o.Raw
		}
	}
	collectionRaw := ""
	for _, o := range obs {
		if o.Field == "collection.time" && o.Status != "superseded" && o.Status != "historical" {
			collectionRaw = o.Raw
			break
		}
	}
	_, _ = st.DB.Exec(`UPDATE jobs SET product_label=?, schema_flags=?, collection_time_raw=? WHERE id=?`,
		productLabel, compatibilityFlagsJSON(e.Limits), nullIfEmpty(collectionRaw), id)

	// Detection targets classified log sources only; configuration never feeds severity findings.
	var parseSources []profile.SourceInfo
	for _, src := range sources {
		if src.SourceKind == string(classify.KindLog) || (src.SourceKind == "" && shouldDetect(src.RawPath)) {
			parseSources = append(parseSources, src)
		}
	}
	sourcesTotal = len(parseSources)
	parseDone = 0
	setState("parsing", "parsing", fmt.Sprintf("Detecting events in %d log sources", sourcesTotal), partial, files, walker.ExpandedBytes, "", true)
	workers := 2
	if n := runtime.NumCPU(); n < workers {
		workers = n
	}
	if workers < 1 {
		workers = 1
	}
	type parseMsg struct {
		src     profile.SourceInfo
		batch   []detection.Finding
		partial string
		err     error
		done    bool
	}
	jobsCh := make(chan profile.SourceInfo, workers*2)
	outCh := make(chan parseMsg, workers*2)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for src := range jobsCh {
				if ctx.Err() != nil {
					outCh <- parseMsg{src: src, err: ctx.Err(), done: true}
					continue
				}
				rf, err := os.Open(src.CachePath)
				if err != nil {
					outCh <- parseMsg{src: src, err: err, done: true}
					continue
				}
				var pending []detection.Finding
				flush := func(force bool) {
					if len(pending) == 0 {
						return
					}
					if !force && len(pending) < 500 {
						return
					}
					cp := append([]detection.Finding(nil), pending...)
					pending = pending[:0]
					outCh <- parseMsg{src: src, batch: cp}
				}
				scanRes, perr := detection.ScanEmit(rf, func(f detection.Finding) error {
					pending = append(pending, f)
					flush(false)
					return ctx.Err()
				})
				_ = rf.Close()
				flush(true)
				pm := parseMsg{src: src, done: true, partial: scanRes.Reason}
				if perr != nil && scanRes.FindingsEmitted == 0 {
					pm.err = perr
				}
				if scanRes.Partial {
					pm.partial = scanRes.Reason
				}
				outCh <- pm
			}
		}()
	}
	go func() {
		for _, src := range parseSources {
			select {
			case <-ctx.Done():
			case jobsCh <- src:
			}
		}
		close(jobsCh)
		wg.Wait()
		close(outCh)
	}()
	insertBatch := func(src profile.SourceInfo, batch []detection.Finding) error {
		if len(batch) == 0 {
			return nil
		}
		return st.WithTx(ctx, func(tx *sql.Tx) error {
			for _, fnd := range batch {
				catsList := fnd.Categories
				if len(catsList) == 0 {
					catsList = []string{"severity"}
				}
				cats, _ := json.Marshal(catsList)
				rules, _ := json.Marshal(fnd.RuleIDs)
				fp := detection.Fingerprint(src.RawPath, fnd.Severity, catsList, fnd.Message)
				evUTC, prec, conf, zoneKnown := normalizeEventTime(fnd.RawTime)
				if _, err := tx.Exec(`INSERT INTO events(id, job_id, source_id, severity, categories, message, line_start, line_end, raw_time, rule_ids, fingerprint, event_time_utc, time_precision, time_confidence, time_zone_known)
					VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, newID(), id, src.ID, fnd.Severity, string(cats), fnd.Message, fnd.LineStart, fnd.LineEnd, fnd.RawTime, string(rules), fp, evUTC, prec, conf, boolInt(zoneKnown)); err != nil {
					return err
				}
			}
			return nil
		})
	}
	for msg := range outCh {
		if msg.err != nil && len(msg.batch) == 0 && msg.done {
			partial = true
			_, _ = st.DB.Exec(`INSERT INTO coverage_issues(id, job_id, source_id, code, severity, reason) VALUES(?,?,?,?,?,?)`,
				newID(), id, msg.src.ID, "parse_open", "warning", msg.err.Error())
		}
		if len(msg.batch) > 0 {
			if err := insertBatch(msg.src, msg.batch); err != nil {
				partial = true
			} else {
				eventsCommitted += len(msg.batch)
				if firstResultMS == 0 {
					firstResultMS = time.Since(started).Milliseconds()
				}
				setState("parsing", "parsing", fmt.Sprintf("Detecting events (%d/%d) · %d findings committed (provisional)", parseDone, sourcesTotal, eventsCommitted), partial, files, walker.ExpandedBytes, msg.src.RawPath, true)
			}
		}
		if msg.partial != "" {
			partial = true
			_, _ = st.DB.Exec(`INSERT INTO coverage_issues(id, job_id, source_id, code, severity, reason) VALUES(?,?,?,?,?,?)`,
				newID(), id, msg.src.ID, "detection_partial", "warning", msg.partial)
			diag.Instance().Info("job.coverage", map[string]any{
				"job_id": id, "source_id": msg.src.ID, "coverage_code": "detection_partial", "stage": "parsing",
			})
		}
		if msg.done {
			parseDone++
			setState("parsing", "parsing", fmt.Sprintf("Detecting events (%d/%d) · %d findings committed (provisional)", parseDone, sourcesTotal, eventsCommitted), partial, files, walker.ExpandedBytes, msg.src.RawPath, parseDone%10 == 0 || parseDone == sourcesTotal)
		}
		if ctx.Err() != nil {
			setState("cancelled", "parsing", "Cancelled during parsing", true, files, walker.ExpandedBytes, "", true)
			return
		}
	}

	var obsStart, obsEnd string
	_ = st.DB.QueryRow(`SELECT COALESCE(MIN(event_time_utc),''), COALESCE(MAX(event_time_utc),'') FROM events WHERE job_id=? AND event_time_utc IS NOT NULL AND event_time_utc!=''`, id).Scan(&obsStart, &obsEnd)

	cov := map[string]any{
		"files": files, "expanded_bytes": walker.ExpandedBytes, "evidence_bytes": ew.Used,
		"sources_cached": len(sources), "sources_parsed": sourcesTotal, "partial": partial, "product_label": productLabel,
		"observed_log_start": obsStart, "observed_log_end": obsEnd, "collection_time_raw": collectionRaw,
	}
	b, _ := json.Marshal(cov)
	finalState := "completed"
	if partial {
		finalState = "partial"
	}
	if ctx.Err() != nil {
		finalState = "cancelled"
	}
	finishMsg := "Analysis finished"
	if finalState == "failed" {
		finishMsg = "Analysis failed"
	}
	countsFinal = finalState == "completed" || finalState == "partial"
	// Preserve durable error_detail from intake; progress_message may still summarize state.
	_, _ = st.DB.Exec(`UPDATE jobs SET state=?, stage=?, coverage_json=?, partial=?, observed_log_start=?, observed_log_end=?, updated_utc=?, events_committed=?, first_result_ms=?, counts_final=? WHERE id=?`,
		finalState, "done", string(b), boolInt(partial), nullIfEmpty(obsStart), nullIfEmpty(obsEnd), time.Now().UTC().Format(time.RFC3339), eventsCommitted, firstResultMS, boolInt(countsFinal), id)
	setState(finalState, "done", fmt.Sprintf("%s · %d findings (%s)", finishMsg, eventsCommitted, map[bool]string{true: "final", false: "provisional"}[countsFinal]), partial, files, walker.ExpandedBytes, "", true)
}

func normalizeEventTime(raw string) (utc string, precision, confidence string, zoneKnown bool) {
	st := timeparse.Parse(raw, 0)
	if st.UTC != nil {
		utc = st.UTC.UTC().Format(time.RFC3339Nano)
	}
	return utc, st.Precision, st.Confidence, st.ZoneKnown
}

func shouldDetect(path string) bool {
	cls := classify.ClassifyMember(path, nil, false)
	return cls.Kind == classify.KindLog
}

func stagePercent(stage string, files, sourcesTotal, parseDone int) int {
	switch stage {
	case "intake":
		if files <= 0 {
			return 5
		}
		// Intake has no known total; keep mid-band while growing.
		p := 10 + files/20
		if p > 45 {
			p = 45
		}
		return p
	case "profile":
		return 50
	case "parsing":
		if sourcesTotal <= 0 {
			return 55
		}
		return 55 + (parseDone * 40 / sourcesTotal)
	case "done":
		return 100
	default:
		return 5
	}
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func sanitizeName(n string) string {
	n = filepath.Base(n)
	if n == "" || n == "." {
		return "snapshot.tar.gz"
	}
	return n
}

func looksMetadata(p string) bool {
	l := strings.ToLower(p)
	return strings.Contains(l, "os-release") || strings.HasSuffix(l, "hostname") ||
		strings.Contains(l, "cpuinfo") || strings.Contains(l, "meminfo") ||
		strings.Contains(l, "version") || strings.Contains(l, "ifconfig") ||
		strings.Contains(l, "ip_addr") || strings.Contains(l, "uname") ||
		strings.HasSuffix(l, ".xml") || strings.HasSuffix(l, ".json") ||
		strings.HasSuffix(l, ".conf") || strings.HasSuffix(l, ".properties") ||
		strings.Contains(l, "product") || strings.Contains(l, "status")
}

func looksTextish(p string) bool {
	l := strings.ToLower(p)
	for _, ext := range []string{".txt", ".log", ".out", ".err", ".conf", ".cfg", ".xml", ".json", ".properties", ".yml", ".yaml", ".csv"} {
		if strings.HasSuffix(l, ext) {
			return true
		}
	}
	base := pathBase(l)
	return base == "messages" || base == "syslog" || strings.HasPrefix(base, "messages.")
}

func isBinaryName(p string) bool {
	l := strings.ToLower(p)
	for _, ext := range []string{".bin", ".so", ".dll", ".exe", ".class", ".jar", ".war", ".png", ".jpg", ".zip", ".core"} {
		if strings.HasSuffix(l, ext) {
			return true
		}
	}
	return strings.Contains(l, "/core.") || strings.Contains(l, "core.datasrv")
}

func pathBase(p string) string {
	p = strings.ReplaceAll(p, `\`, `/`)
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

func (e *Engine) OpenJobStore(id string) (*store.Store, error) {
	dir, err := e.Root.SafeJobDir(id)
	if err != nil {
		return nil, err
	}
	return store.Open(filepath.Join(dir, "results.sqlite"))
}

func (e *Engine) ListJobs() ([]map[string]any, error) {
	entries, err := os.ReadDir(e.Root.JobsDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []map[string]any
	for _, ent := range entries {
		if !ent.IsDir() || !storage.ValidJobID(ent.Name()) {
			continue
		}
		dir, err := e.Root.SafeJobDir(ent.Name())
		if err != nil {
			continue
		}
		st, err := store.Open(filepath.Join(dir, "results.sqlite"))
		if err != nil {
			continue
		}
		row := st.DB.QueryRow(`SELECT id, original_name, state, stage, partial, created_utc, updated_utc, COALESCE(product_label,''), COALESCE(progress_message,''), COALESCE(sha256,''), COALESCE(size_bytes,0) FROM jobs WHERE id=?`, ent.Name())
		var id, name, state, stage, created, updated, product, msg, sha string
		var partial int
		var size int64
		if err := row.Scan(&id, &name, &state, &stage, &partial, &created, &updated, &product, &msg, &sha, &size); err != nil {
			_ = st.Close()
			continue
		}
		_ = st.Close()
		disk, _ := JobDiskUsage(dir)
		out = append(out, map[string]any{
			"id": id, "original_name": name, "state": state, "stage": stage,
			"partial": partial == 1, "created_utc": created, "updated_utc": updated, "product_label": product,
			"message": msg, "active": id == e.ActiveJobID(),
			"sha256": sha, "upload_bytes": size, "disk_bytes": disk,
		})
	}
	return out, nil
}
