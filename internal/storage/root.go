package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"crypto/rand"
	"encoding/hex"

	"quickanalyser/internal/platform"
)

const (
	ModeDefault  = "default"
	ModeCustom   = "custom"
	ModePortable = "portable"
	rootMarker   = ".quickanalyser-root.json"
)

var ErrBusy = errors.New("storage root change requires idle jobs")

// SkipPreferenceSave prevents OpenOrInit from writing the user's normal storage.json.
// Tests must set this (or call OpenTestRoot) so TempDir paths cannot become the startup root.
var SkipPreferenceSave bool

// RootMeta identifies an owned working storage root.
type RootMeta struct {
	App            string `json:"app"`
	RootUUID       string `json:"root_uuid"`
	StorageFormat  int    `json:"storage_format"`
	CreatedUTC     string `json:"created_utc"`
	Mode           string `json:"mode"`
}

// Manager owns the active working storage root.
type Manager struct {
	Mode     string
	Root     string
	lock     *platform.InstanceLock
	busyJobs int
}

type preference struct {
	Mode string `json:"mode"`
	Root string `json:"root,omitempty"`
}

// ResolveStartup picks --data-dir, portable, remembered preference, or first-run default.
func ResolveStartup(dataDir string, portable bool) (*Manager, bool, error) {
	if dataDir != "" && portable {
		return nil, false, fmt.Errorf("conflicting --data-dir and --portable")
	}
	if dataDir != "" {
		abs, err := filepath.Abs(dataDir)
		if err != nil {
			return nil, false, err
		}
		// One-shot --data-dir must not overwrite the user's remembered storage root
		// (agent/smoke runs and temporary measure roots would otherwise stick on next double-click).
		prev := SkipPreferenceSave
		SkipPreferenceSave = true
		m, err := OpenOrInit(abs, ModeCustom)
		SkipPreferenceSave = prev
		return m, false, err
	}
	if portable {
		root, err := platform.PortableDataRoot()
		if err != nil {
			return nil, false, err
		}
		m, err := OpenOrInit(root, ModePortable)
		return m, false, err
	}
	pref, err := loadPreference()
	if err == nil && pref.Root != "" {
		m, err := OpenOrInit(pref.Root, pref.Mode)
		return m, false, err
	}
	def, err := platform.DefaultDataRoot()
	if err != nil {
		return nil, false, err
	}
	// First run: expose default but require UI confirmation before heavy use.
	needsSetup := pref == nil || pref.Root == ""
	if !needsSetup {
		m, err := OpenOrInit(def, ModeDefault)
		return m, false, err
	}
	m := &Manager{Mode: ModeDefault, Root: def}
	return m, true, nil
}

func OpenOrInit(root, mode string) (*Manager, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := platform.EnsureDir(abs); err != nil {
		return nil, err
	}
	metaPath := filepath.Join(abs, rootMarker)
	if _, err := os.Stat(metaPath); errors.Is(err, os.ErrNotExist) {
		entries, _ := os.ReadDir(abs)
		nonApp := 0
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".quickanalyser") {
				continue
			}
			nonApp++
		}
		if nonApp > 0 {
			child := filepath.Join(abs, "QuickAnalyserData")
			if err := platform.EnsureDir(child); err != nil {
				return nil, err
			}
			abs = child
			metaPath = filepath.Join(abs, rootMarker)
		}
		meta := RootMeta{
			App:           "QuickAnalyser",
			RootUUID:      newID(),
			StorageFormat: 1,
			CreatedUTC:    time.Now().UTC().Format(time.RFC3339),
			Mode:          mode,
		}
		if err := writeJSON(metaPath, meta); err != nil {
			return nil, err
		}
		for _, sub := range []string{"config", "jobs", "diagnostics"} {
			if err := platform.EnsureDir(filepath.Join(abs, sub)); err != nil {
				return nil, err
			}
		}
	} else if err != nil {
		return nil, err
	}
	lock, err := platform.AcquireInstanceLock(abs)
	if err != nil {
		return nil, err
	}
	m := &Manager{Mode: mode, Root: abs, lock: lock}
	if !SkipPreferenceSave {
		_ = savePreference(preference{Mode: mode, Root: abs})
	}
	return m, nil
}

// OpenTestRoot opens a storage root without persisting application preferences.
func OpenTestRoot(root string) (*Manager, error) {
	prev := SkipPreferenceSave
	SkipPreferenceSave = true
	defer func() { SkipPreferenceSave = prev }()
	return OpenOrInit(root, ModeCustom)
}

func (m *Manager) Close() {
	if m != nil && m.lock != nil {
		m.lock.Release()
		m.lock = nil
	}
}

func (m *Manager) SetBusy(delta int) {
	m.busyJobs += delta
	if m.busyJobs < 0 {
		m.busyJobs = 0
	}
}

func (m *Manager) Busy() bool { return m.busyJobs > 0 }

func (m *Manager) JobsDir() string { return filepath.Join(m.Root, "jobs") }

// JobDir returns the directory for id. Invalid IDs never join as path segments that
// can escape jobs/ (traversal IDs resolve under a rejected placeholder). Prefer SafeJobDir
// for destructive operations.
func (m *Manager) JobDir(id string) string {
	if !ValidJobID(id) {
		return filepath.Join(m.JobsDir(), RejectedJobDirName)
	}
	return filepath.Join(m.JobsDir(), id)
}

func (m *Manager) Info() map[string]any {
	free, total := DiskSpace(m.Root)
	return map[string]any{
		"mode":           m.Mode,
		"root":           m.Root,
		"free_bytes":     free,
		"total_bytes":    total,
		"busy":           m.Busy(),
		"needs_setup":    false,
		"portable_ready": m.Mode == ModePortable,
	}
}

func (m *Manager) ChangeRoot(path, mode string) (*Manager, error) {
	if m.Busy() {
		return nil, ErrBusy
	}
	// Open and lock the replacement first so a failed switch leaves the current root usable.
	next, err := OpenOrInit(path, mode)
	if err != nil {
		return nil, err
	}
	if filepath.Clean(next.Root) == filepath.Clean(m.Root) {
		next.Close()
		m.Mode = mode
		return m, nil
	}
	m.Close()
	return next, nil
}

func loadPreference() (*preference, error) {
	dir, err := platform.DefaultConfigDir()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(filepath.Join(dir, "storage.json"))
	if err != nil {
		return nil, err
	}
	var p preference
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func savePreference(p preference) error {
	if p.Mode == ModePortable {
		// Portable preference lives beside the data root.
		return writeJSON(filepath.Join(p.Root, "config", "storage-pointer.json"), p)
	}
	dir, err := platform.DefaultConfigDir()
	if err != nil {
		return err
	}
	if err := platform.EnsureDir(dir); err != nil {
		return err
	}
	return writeJSON(filepath.Join(dir, "storage.json"), p)
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func writeJSON(path string, v any) error {
	if err := platform.EnsureDir(filepath.Dir(path)); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
