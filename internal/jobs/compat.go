package jobs

import (
	"encoding/json"
	"strings"

	"quickanalyser/internal/appinfo"
	"quickanalyser/internal/classify"
	"quickanalyser/internal/config"
	"quickanalyser/internal/detection"
	"quickanalyser/internal/diag"
	"quickanalyser/internal/profile"
	"quickanalyser/internal/store"
)

func logReuse(code, jobID, reason string) {
	attrs := map[string]any{"reason_code": sanitizeReasonCode(reason)}
	if jobID != "" {
		attrs["job_id"] = jobID
	}
	if code == "reuse.hit" {
		attrs["compatible"] = true
	}
	if code == "reuse.miss" {
		attrs["compatible"] = false
	}
	// Best-effort; never fail analysis if diagnostics are unavailable.
	func() {
		defer func() { _ = recover() }()
		diag.Instance().Info(code, attrs)
	}()
}

func sanitizeReasonCode(reason string) string {
	r := strings.ToLower(strings.TrimSpace(reason))
	switch {
	case strings.Contains(r, "integrity"):
		return "evidence_integrity"
	case strings.Contains(r, "incomplete"):
		return "evidence_verify_incomplete"
	case strings.Contains(r, "schema"):
		return "schema_mismatch"
	case strings.Contains(r, "settings"):
		return "settings_mismatch"
	case strings.Contains(r, "classifier"):
		return "classifier_mismatch"
	case strings.Contains(r, "profile"):
		return "profile_mismatch"
	case strings.Contains(r, "parser"):
		return "parser_mismatch"
	case strings.Contains(r, "engine"):
		return "engine_mismatch"
	case strings.Contains(r, "rule"):
		return "rule_mismatch"
	case strings.Contains(r, "app version"):
		return "app_version_mismatch"
	case strings.Contains(r, "partial"):
		return "partial_analysis"
	default:
		return "incompatible"
	}
}

// ParserVersion identifies archive/evidence parsing semantics independent of app label.
const ParserVersion = "1.0.0"

func compatibilityFlagsJSON(limits config.Limits) string {
	fp := analysisSettingsFingerprint(limits)
	settings := map[string]any{}
	for k, v := range fp {
		settings[k] = v
	}
	payload := map[string]any{
		"classifier_version": classify.Version,
		"profile_version":    profile.ExtractionVersion,
		"engine_version":     appinfo.Version(),
		"parser_version":     ParserVersion,
		"schema_version":     store.SchemaVersion,
		"rule_bundle":        detection.RuleBundleVersion,
		"overview":           "curated",
		"needs_reanalysis":   false,
		"settings":           settings,
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return `{"needs_reanalysis":true}`
	}
	return string(b)
}
