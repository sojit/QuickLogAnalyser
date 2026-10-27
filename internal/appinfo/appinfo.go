package appinfo

import (
	_ "embed"
	"runtime"
	"strings"
)

//go:embed version.txt
var versionFile string

const (
	ProductName      = "QuickAnalyser"
	DeveloperDisplay = "by Soji"
	CopyrightNotice  = "Copyright (c) 2026 Soji"
	LicenseID        = "MIT"
)

var (
	// Commit, BuildTime, and Dirty are injected by build scripts when available.
	Commit    = "unknown"
	BuildTime = "unknown"
	Dirty     = "true"
)

// Version returns the canonical release label from VERSION / embedded file.
func Version() string {
	v := strings.TrimSpace(versionFile)
	if v == "" {
		return "QA-v1.0-2026"
	}
	return v
}

// BuildIdentity summarizes build metadata for About/footer/exports.
func BuildIdentity() map[string]string {
	return map[string]string{
		"product":   ProductName,
		"signature": DeveloperDisplay,
		"version":   Version(),
		"commit":    Commit,
		"buildTime": BuildTime,
		"dirty":     Dirty,
		"go":        runtime.Version(),
		"os":        runtime.GOOS,
		"arch":      runtime.GOARCH,
		"license":   LicenseID,
		"copyright": CopyrightNotice,
		"status":    "development_unreleased",
	}
}
