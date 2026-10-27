package platform

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// DefaultDataRoot returns the OS default durable data location (not mandatory).
func DefaultDataRoot() (string, error) {
	switch runtime.GOOS {
	case "windows":
		base := os.Getenv("LOCALAPPDATA")
		if base == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			base = filepath.Join(home, "AppData", "Local")
		}
		return filepath.Join(base, "QuickAnalyser"), nil
	default:
		if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
			return filepath.Join(xdg, "quickanalyser"), nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".local", "share", "quickanalyser"), nil
	}
}

// DefaultConfigDir stores small startup preference pointers.
func DefaultConfigDir() (string, error) {
	switch runtime.GOOS {
	case "windows":
		base := os.Getenv("LOCALAPPDATA")
		if base == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			base = filepath.Join(home, "AppData", "Local")
		}
		return filepath.Join(base, "QuickAnalyser", "config"), nil
	default:
		if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
			return filepath.Join(xdg, "quickanalyser"), nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".config", "quickanalyser"), nil
	}
}

// ExecutableDir returns the directory containing the running binary.
func ExecutableDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		resolved = exe
	}
	return filepath.Dir(resolved), nil
}

// PortableDataRoot is QuickAnalyserData beside the executable.
func PortableDataRoot() (string, error) {
	dir, err := ExecutableDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "QuickAnalyserData"), nil
}

// EnsureDir creates a directory with restrictive permissions where applicable.
func EnsureDir(path string) error {
	perm := os.FileMode(0o700)
	if runtime.GOOS == "windows" {
		perm = 0o755
	}
	if err := os.MkdirAll(path, perm); err != nil {
		return fmt.Errorf("create directory %s: %w", path, err)
	}
	return nil
}
