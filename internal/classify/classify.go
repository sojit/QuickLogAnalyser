package classify

import (
	"bytes"
	"path"
	"strings"
	"unicode/utf8"
)

// Version must bump whenever classification semantics change so saved analyses refresh.
const Version = "1.3.1"

type Kind string

const (
	KindLog        Kind = "log"
	KindConfig     Kind = "configuration"
	KindDiagnostic Kind = "diagnostic"
	KindInventory  Kind = "inventory"
	KindBinary     Kind = "binary"
	KindUnknown    Kind = "unknown"
)

type Result struct {
	Kind       Kind
	Reason     string
	Confidence string
	KeepText   bool
	NeedsGzip  bool
}

// ClassifyMember decides semantic kind from path plus optional bounded content sample.
// Severity words alone never promote a source to KindLog.
func ClassifyMember(rawPath string, sample []byte, declaredGzip bool) Result {
	lower := strings.ToLower(strings.ReplaceAll(rawPath, `\`, `/`))
	base := path.Base(lower)
	needsGzip := declaredGzip || strings.HasSuffix(lower, ".gz") || strings.HasSuffix(lower, ".tgz")
	if strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz") || strings.HasSuffix(lower, ".tar") {
		return Result{Kind: KindBinary, Reason: "archive_container", Confidence: "high", KeepText: false, NeedsGzip: false}
	}

	namedLog := isNamedLogBasename(base)
	textish := looksTextSample(sample)

	// Named *.log / rotations always list as logs (draft.23 / 1.3.1). This must
	// precede path-wide binary and script/MIB exclusions so core-like parents or
	// "-mib" substrings cannot hide a genuine .log basename. KeepText remains a
	// separate safety decision for decoding/detection.
	if namedLog {
		keep := len(sample) == 0 || textish
		reason := "named_log_basename"
		if len(sample) > 0 && !textish {
			reason = "named_log_non_text_content"
			keep = false
		}
		return Result{Kind: KindLog, Reason: reason, Confidence: "high", KeepText: keep, NeedsGzip: needsGzip}
	}

	if isBinaryName(lower, base) {
		return Result{Kind: KindBinary, Reason: "binary_extension", Confidence: "high", KeepText: false}
	}

	underLog := strings.Contains(lower, "/log/") || strings.Contains(lower, "/logs/")
	underConf := strings.Contains(lower, "/conf/") || strings.Contains(lower, "/etc/") ||
		strings.Contains(lower, "/config/")
	underDiag := strings.Contains(lower, "/diagnostic/") || strings.Contains(lower, "/diagnostics/") ||
		strings.Contains(lower, "sosreport") || strings.Contains(lower, "/collect")
	inventory := strings.Contains(lower, "rpm") || strings.Contains(lower, "package") ||
		strings.Contains(base, "installed") || strings.Contains(base, "manifest")

	scriptOrMIB := isScriptOrMIB(base)
	settingsName := isSettingsName(base)
	explicitLogName := namedLog || isClassicLogBasename(base)

	structLog := false
	structCfg := false
	structJSONLog := false
	if len(sample) > 0 && textish {
		structLog = looksStructuredLog(sample)
		structCfg = looksStructuredConfig(sample, base)
		structJSONLog = looksJSONLogRecords(sample)
	}

	// Scripts and MIB definitions are never logs from keyword presence.
	if scriptOrMIB {
		return Result{Kind: KindDiagnostic, Reason: "script_or_mib", Confidence: "high", KeepText: true, NeedsGzip: needsGzip}
	}

	// Non-text state/binary under log dirs (extensionless / non-named) must not be
	// treated as readable logs. Named .log already returned above.
	if underLog && !explicitLogName && len(sample) > 0 && !textish {
		return Result{Kind: KindBinary, Reason: "log_dir_non_text_state", Confidence: "medium", KeepText: false}
	}

	csvChrono := strings.HasSuffix(base, ".csv") && looksChronologicalCSV(sample)

	// Configuration names / conf paths: severity words alone never win, but
	// chronological CSV / NDJSON / structured log content still classifies as log
	// even when the basename uses a settings-like extension (.json, .csv, …).
	if settingsName || (underConf && !explicitLogName && !structLog && !structJSONLog && !csvChrono) {
		if structLog || structJSONLog || csvChrono {
			return Result{Kind: KindLog, Reason: "log_content_overrides_settings_name", Confidence: "high", KeepText: true, NeedsGzip: needsGzip}
		}
		return Result{Kind: KindConfig, Reason: "path_or_name_config", Confidence: "high", KeepText: true, NeedsGzip: needsGzip}
	}

	// Settings file living under a log directory (including JSON objects).
	// Named .log basenames already returned above.
	if underLog && (settingsName || structCfg) && !structLog && !structJSONLog && !csvChrono {
		return Result{Kind: KindConfig, Reason: "log_dir_settings", Confidence: "high", KeepText: true, NeedsGzip: needsGzip}
	}

	if csvChrono {
		return Result{Kind: KindLog, Reason: "chronological_csv_log", Confidence: "high", KeepText: true, NeedsGzip: needsGzip}
	}
	if strings.HasSuffix(base, ".csv") && (underConf || settingsName) {
		return Result{Kind: KindConfig, Reason: "csv_settings", Confidence: "high", KeepText: true, NeedsGzip: needsGzip}
	}

	// Diagnostic directories / status dumps without chronological log structure.
	if underDiag && !structLog && !structJSONLog && !explicitLogName {
		return Result{Kind: KindDiagnostic, Reason: "diagnostic_path", Confidence: "high", KeepText: true, NeedsGzip: needsGzip}
	}

	if inventory && !structLog && !explicitLogName {
		return Result{Kind: KindInventory, Reason: "inventory_path", Confidence: "medium", KeepText: true, NeedsGzip: needsGzip}
	}

	// Confident logs: path/name or multi-line chronological / JSON log structure.
	if explicitLogName || underLog || structLog || structJSONLog {
		conf := "medium"
		reason := "path_or_content_log"
		if explicitLogName || underLog {
			conf = "high"
		}
		if structLog || structJSONLog {
			conf = "high"
			if structJSONLog {
				reason = "structured_json_log"
			} else {
				reason = "structured_text_log"
			}
		}
		return Result{Kind: KindLog, Reason: reason, Confidence: conf, KeepText: true, NeedsGzip: needsGzip}
	}

	if underConf || structCfg {
		return Result{Kind: KindConfig, Reason: "config_content", Confidence: "medium", KeepText: true, NeedsGzip: needsGzip}
	}
	if underDiag {
		return Result{Kind: KindDiagnostic, Reason: "diagnostic_path", Confidence: "medium", KeepText: true, NeedsGzip: needsGzip}
	}
	if textish && extensionless(base) {
		// Extensionless text without log structure stays unknown/diagnostic, not log.
		return Result{Kind: KindUnknown, Reason: "extensionless_text_unknown", Confidence: "low", KeepText: true, NeedsGzip: needsGzip}
	}
	if textish {
		return Result{Kind: KindUnknown, Reason: "readable_text_unknown", Confidence: "low", KeepText: true, NeedsGzip: needsGzip}
	}
	return Result{Kind: KindUnknown, Reason: "unclassified", Confidence: "low", KeepText: false, NeedsGzip: needsGzip}
}

func isSettingsName(base string) bool {
	return strings.HasSuffix(base, ".properties") || strings.HasSuffix(base, ".xml") ||
		strings.HasSuffix(base, ".yml") || strings.HasSuffix(base, ".yaml") ||
		strings.HasSuffix(base, ".conf") || strings.HasSuffix(base, ".cfg") ||
		strings.HasSuffix(base, ".ini") || strings.HasSuffix(base, ".json") ||
		base == "dbversion.txt" || strings.Contains(base, "install_log") ||
		base == "os-release" || base == "hostname" || strings.HasPrefix(base, "ansible") ||
		base == "extsyslogservers.csv" || strings.HasSuffix(base, "syslogservers.csv") ||
		strings.Contains(base, "syslogserver")
}

func isClassicLogBasename(base string) bool {
	return base == "messages" || strings.HasPrefix(base, "messages.") ||
		base == "syslog" || strings.HasPrefix(base, "syslog.") ||
		base == "dmesg" || strings.HasPrefix(base, "dmesg.")
}

// isNamedLogBasename is basename-anchored: *.log and common rotations/compression.
// Parent directories containing ".log" do not qualify unrelated children.
func isNamedLogBasename(base string) bool {
	if base == "" || base == "." || base == ".." {
		return false
	}
	// Settings extensions are never named logs (even if the letters "log" appear).
	// Do not call isScriptOrMIB here: "-mib" substrings in *.log names (e.g.
	// device-mib.log) must remain listable; real .mib/.sh files lack a .log suffix.
	if strings.HasSuffix(base, ".csv") || strings.HasSuffix(base, ".json") ||
		strings.HasSuffix(base, ".xml") || strings.HasSuffix(base, ".yml") ||
		strings.HasSuffix(base, ".yaml") || strings.HasSuffix(base, ".properties") ||
		strings.HasSuffix(base, ".conf") || strings.HasSuffix(base, ".cfg") ||
		strings.HasSuffix(base, ".ini") || strings.HasSuffix(base, ".txt") ||
		strings.HasSuffix(base, ".mib") || strings.HasSuffix(base, ".sh") ||
		strings.HasSuffix(base, ".bash") || strings.HasSuffix(base, ".py") ||
		strings.HasSuffix(base, ".pl") || strings.HasSuffix(base, ".ps1") {
		return false
	}
	name := base
	for _, suf := range []string{".gz", ".bz2", ".xz", ".zst"} {
		if strings.HasSuffix(name, suf) {
			name = strings.TrimSuffix(name, suf)
			break
		}
	}
	if strings.HasSuffix(name, ".log") {
		return true
	}
	// Rotations: app.log.1, app.log.12, app.log.2026-10-23
	const marker = ".log."
	idx := strings.Index(name, marker)
	if idx < 0 {
		return false
	}
	rest := name[idx+len(marker):]
	return isLogRotationSuffix(rest)
}

func isLogRotationSuffix(rest string) bool {
	if rest == "" {
		return false
	}
	// Pure numeric rotation (.1, .12).
	allDigit := true
	for _, r := range rest {
		if r < '0' || r > '9' {
			allDigit = false
			break
		}
	}
	if allDigit {
		return true
	}
	// Date-like: 2026-10-23 or 20261023
	digitDash := 0
	for _, r := range rest {
		if (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			digitDash++
			continue
		}
		return false
	}
	return digitDash >= 4
}

// isExplicitLogName retained for callers/tests: named .log* or classic messages/syslog/dmesg.
func isExplicitLogName(base string) bool {
	return isNamedLogBasename(base) || isClassicLogBasename(base)
}

func isScriptOrMIB(base string) bool {
	for _, ext := range []string{".sh", ".bash", ".ksh", ".csh", ".py", ".pl", ".ps1", ".rb", ".mib"} {
		if strings.HasSuffix(base, ext) {
			return true
		}
	}
	return strings.Contains(base, "-mib") || strings.HasSuffix(base, ".mib.txt")
}

func extensionless(base string) bool {
	return !strings.Contains(base, ".")
}

// looksStructuredLog requires chronological/record structure across lines — not severity substrings.
func looksStructuredLog(sample []byte) bool {
	lines := splitSampleLines(sample, 12)
	if len(lines) == 0 {
		return false
	}
	hits := 0
	commentish := 0
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if trim == "" {
			continue
		}
		if strings.HasPrefix(trim, "#") || strings.HasPrefix(trim, "//") || strings.HasPrefix(trim, ";") {
			commentish++
			continue
		}
		if looksSyslogLine(trim) || looksISOTimestampLine(trim) {
			hits++
		}
	}
	if hits >= 2 {
		return true
	}
	if hits == 1 && len(lines) <= 3 && commentish == 0 {
		return true
	}
	return false
}

func looksStructuredConfig(sample []byte, base string) bool {
	s := strings.ToLower(string(sample))
	trim := strings.TrimSpace(s)
	if strings.HasPrefix(trim, "<?xml") || strings.Contains(s, "<configuration") {
		return true
	}
	if strings.HasPrefix(trim, "[") && strings.Contains(s, "]") && strings.Contains(s, "=") {
		return true // ini-like
	}
	lines := splitSampleLines(sample, 20)
	kv := 0
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, ";") || strings.HasPrefix(t, "//") {
			continue
		}
		if strings.Contains(t, "=") && !looksSyslogLine(t) && !looksISOTimestampLine(t) {
			kv++
		}
	}
	if kv >= 1 && (isSettingsName(base) || kv >= 2) {
		return true
	}
	// JSON object settings (not array of log records)
	if strings.HasPrefix(trim, "{") && !looksJSONLogRecords(sample) {
		return true
	}
	return false
}

func looksChronologicalCSV(sample []byte) bool {
	if len(sample) == 0 {
		return false
	}
	lines := splitSampleLines(sample, 8)
	hits := 0
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(strings.ToLower(t), "host,") || strings.HasPrefix(strings.ToLower(t), "timestamp,") {
			continue
		}
		parts := strings.Split(t, ",")
		if len(parts) < 2 {
			continue
		}
		first := strings.TrimSpace(parts[0])
		if looksISOTimestampLine(first) || looksSyslogLine(first) {
			hits++
		}
	}
	return hits >= 2 || (hits == 1 && len(lines) <= 2)
}

func looksJSONLogRecords(sample []byte) bool {
	trim := bytes.TrimSpace(sample)
	if len(trim) == 0 {
		return false
	}
	// NDJSON: multiple lines starting with {
	lines := splitSampleLines(sample, 8)
	jsonLines := 0
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "{") && (strings.Contains(t, `"message"`) || strings.Contains(t, `"msg"`) ||
			strings.Contains(t, `"timestamp"`) || strings.Contains(t, `"time"`) || strings.Contains(t, `"level"`)) {
			jsonLines++
		}
	}
	if jsonLines >= 2 {
		return true
	}
	if jsonLines == 1 && len(lines) == 1 {
		return true
	}
	return false
}

func splitSampleLines(sample []byte, max int) []string {
	var out []string
	start := 0
	for i := 0; i < len(sample) && len(out) < max; i++ {
		if sample[i] == '\n' {
			out = append(out, string(sample[start:i]))
			start = i + 1
		}
	}
	if start < len(sample) && len(out) < max {
		out = append(out, string(sample[start:]))
	}
	return out
}

func looksSyslogLine(s string) bool {
	if len(s) < 15 {
		return false
	}
	months := []string{"Jan ", "Feb ", "Mar ", "Apr ", "May ", "Jun ", "Jul ", "Aug ", "Sep ", "Oct ", "Nov ", "Dec "}
	for _, m := range months {
		if strings.HasPrefix(s, m) {
			return true
		}
	}
	return looksISOTimestampLine(s)
}

func looksISOTimestampLine(s string) bool {
	// 2026-10-23 ... or 2026-10-23T11:38:03
	if len(s) < 19 {
		return false
	}
	if s[4] == '-' && s[7] == '-' && (s[10] == ' ' || s[10] == 'T') && s[13] == ':' && s[16] == ':' {
		return true
	}
	return false
}

func looksTextSample(sample []byte) bool {
	if len(sample) == 0 {
		return false
	}
	if bytes.HasPrefix(sample, []byte{0x1f, 0x8b}) {
		return false
	}
	n := len(sample)
	if n > 512 {
		n = 512
	}
	nonPrint := 0
	for i := 0; i < n; {
		r, size := utf8.DecodeRune(sample[i:n])
		if size == 0 {
			break
		}
		i += size
		if r == utf8.RuneError {
			nonPrint++
			continue
		}
		if r == 0 {
			return false
		}
		if r < 9 || (r > 13 && r < 32) {
			nonPrint++
		}
	}
	return nonPrint*10 < n
}

func isBinaryName(lower, base string) bool {
	for _, ext := range []string{".bin", ".so", ".dll", ".exe", ".class", ".jar", ".war", ".png", ".jpg", ".zip", ".core", ".pcap"} {
		if strings.HasSuffix(base, ext) || strings.HasSuffix(lower, ext) {
			return true
		}
	}
	// Match core dumps by basename (core.datasrv.123), not parent dirs like core.collector/.
	return strings.HasPrefix(base, "core.") || strings.Contains(base, "core.datasrv")
}
