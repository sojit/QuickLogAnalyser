package profile

import (
	"bufio"
	"os"
	"path"
	"regexp"
	"strings"
)

// Source-role authorities for conflict resolution (higher wins).
const (
	RoleDeviceDashboard = "device_dashboard"
	RoleDiagnostic      = "diagnostic"
	RolePackage         = "package"
	RoleGeneric         = "generic"
	RoleServiceConfig   = "service_config"
)

type Observation struct {
	Field      string
	Raw        string
	Normalized string
	Confidence string
	Status     string
	Scope      string
	Role       string // semantic source role; empty treated as generic
	SourceID   string
	LineStart  int
	LineEnd    int
}

// ExtractionVersion bumps when profile/adapters change selection semantics.
const ExtractionVersion = "1.1.0"

func roleRank(role string) int {
	switch role {
	case RoleDeviceDashboard:
		return 5
	case RoleDiagnostic:
		return 4
	case RolePackage:
		return 3
	case RoleGeneric, "":
		return 2
	case RoleServiceConfig:
		return 1
	default:
		return 2
	}
}

type SourceInfo struct {
	ID         string
	RawPath    string
	CachePath  string
	SourceKind string
}

var (
	reHostname = regexp.MustCompile(`(?i)^(?:HOSTNAME|hostname)\s*[=:]\s*(\S+)`)
	rePretty   = regexp.MustCompile(`(?i)^PRETTY_NAME="?([^"\n]+)"?`)
	reKernel   = regexp.MustCompile(`(?i)^Linux version ([^\s]+)`)
	reMemTotal = regexp.MustCompile(`(?i)^MemTotal:\s+(\d+)\s+kB`)
	reIP       = regexp.MustCompile(`(?i)\binet\s+(\d+\.\d+\.\d+)\.(\d+)`)
	reProduct  = regexp.MustCompile(`(?i)\b(NetXplorer|NX Server|SGVE|vSG|AOS)\b`)
)

// OperationalField reports profile keys that must not appear as Overview cards.
func OperationalField(field string) bool {
	f := strings.ToLower(field)
	return strings.Contains(f, "rollback") || strings.Contains(f, "db_signal") ||
		strings.Contains(f, "oom") || strings.Contains(f, "crash") ||
		strings.HasSuffix(f, ".error") || strings.Contains(f, "panic")
}

func lowPathName(p string) string {
	return strings.ToLower(strings.ReplaceAll(p, `\`, `/`))
}

func profileCandidate(rawPath string) bool {
	l := strings.ToLower(rawPath)
	base := path.Base(l)
	if base == "os-release" || base == "hostname" || base == "version" ||
		base == "meminfo" || base == "cpuinfo" || base == "uname" ||
		strings.Contains(l, "os-release") || strings.Contains(l, "hostname") ||
		strings.Contains(l, "meminfo") || strings.Contains(l, "ifconfig") ||
		strings.Contains(l, "ip_addr") || strings.Contains(l, "product") ||
		strings.Contains(l, "status") || 		strings.HasSuffix(l, ".xml") ||
		strings.HasSuffix(l, ".properties") || strings.HasSuffix(l, ".conf") ||
		strings.Contains(l, "netxplorer") ||
		strings.Contains(l, "nxserver") || strings.Contains(l, "sgve") || strings.Contains(l, "vsg") ||
		strings.Contains(l, "install_log") || strings.Contains(l, "dbversion") ||
		strings.Contains(l, "system_info") {
		return true
	}
	return false
}

// ObserveGeneric extracts product-independent attributes from cached text sources.
func ObserveGeneric(sources []SourceInfo) []Observation {
	var out []Observation
	productHits := map[string]int{}
	for _, src := range sources {
		if !profileCandidate(src.RawPath) {
			continue
		}
		base := strings.ToLower(path.Base(src.RawPath))
		f, err := os.Open(src.CachePath)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		buf := make([]byte, 0, 64*1024)
		sc.Buffer(buf, 1<<20)
		lineNo := 0
		maxLines := 400
		for sc.Scan() {
			lineNo++
			if lineNo > maxLines {
				break
			}
			line := sc.Text()
			add := func(field, raw, conf string) {
				out = append(out, Observation{
					Field: field, Raw: raw, Normalized: raw, Confidence: conf,
					Status: "observed", Scope: "device", Role: RoleGeneric, SourceID: src.ID,
					LineStart: lineNo, LineEnd: lineNo,
				})
			}
			if m := reHostname.FindStringSubmatch(line); m != nil {
				hostVal := strings.Trim(m[1], `"'`)
				role := RoleGeneric
				// Application/CLI config hostname is a service endpoint, not device identity.
				lp := lowPathName(src.RawPath)
				if strings.Contains(base, "cli") || strings.Contains(base, "conf") ||
					strings.HasSuffix(base, ".properties") || strings.Contains(lp, "datacli") ||
					strings.Contains(lp, "datcli") {
					role = RoleServiceConfig
				}
				if base == "hostname" || strings.Contains(lp, "system_info") {
					role = RoleDeviceDashboard
				}
				out = append(out, Observation{
					Field: "hostname", Raw: hostVal, Normalized: hostVal, Confidence: "high",
					Status: "observed", Scope: "device", Role: role, SourceID: src.ID,
					LineStart: lineNo, LineEnd: lineNo,
				})
			}
			if base == "os-release" || strings.Contains(src.RawPath, "os-release") {
				if m := rePretty.FindStringSubmatch(line); m != nil {
					add("os.pretty_name", m[1], "high")
				}
			}
			if m := reKernel.FindStringSubmatch(line); m != nil {
				add("os.kernel", m[1], "high")
			}
			if m := reMemTotal.FindStringSubmatch(line); m != nil {
				add("memory.total_kb", m[1], "high")
			}
			if m := reIP.FindStringSubmatch(line); m != nil {
				ip := m[1] + "." + m[2]
				if !strings.HasPrefix(ip, "127.") {
					add("network.ipv4", ip, "medium")
				}
			}
			if m := reProduct.FindStringSubmatch(line); m != nil {
				productHits[m[1]]++
			}
			if strings.HasPrefix(strings.ToLower(line), "product=") {
				add("product.hint", strings.TrimSpace(line[len("product="):]), "medium")
			}
			if strings.HasPrefix(strings.ToLower(line), "version=") && (base == "version" || strings.Contains(src.RawPath, "version")) {
				add("software.version", strings.TrimSpace(line[len("version="):]), "medium")
			}
		}
		_ = f.Close()
	}
	if len(productHits) == 0 {
		out = append(out, Observation{
			Field: "product.identity", Raw: "Unknown product — generic analysis",
			Normalized: "unknown", Confidence: "high", Status: "unresolved", Scope: "device",
		})
	} else {
		best, n := "", 0
		for k, v := range productHits {
			if v > n {
				best, n = k, v
			}
		}
		out = append(out, Observation{
			Field: "product.identity", Raw: best, Normalized: strings.ToLower(best),
			Confidence: "medium", Status: "observed", Scope: "device",
		})
	}
	return out
}

// Resolve merges generic + adapter observations, drops operational fields from
// profile selection, deduplicates identical values, and prefers current/high
// confidence over historical for the same semantic field+scope key.
func Resolve(all []Observation) []Observation {
	filtered := make([]Observation, 0, len(all))
	for _, o := range all {
		if OperationalField(o.Field) {
			continue
		}
		if o.Scope == "" {
			o.Scope = "device"
		}
		if o.Status == "" {
			o.Status = "observed"
		}
		filtered = append(filtered, o)
	}
	// Deduplicate identical field/scope/raw/source/line
	seen := map[string]struct{}{}
	dedup := make([]Observation, 0, len(filtered))
	for _, o := range filtered {
		k := o.Field + "|" + o.Scope + "|" + o.Raw + "|" + o.SourceID + "|" + itoa(o.LineStart)
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		dedup = append(dedup, o)
	}
	return Select(dedup)
}

// Select marks preferred observations; keeps superseded/historical values unselected.
func Select(obs []Observation) []Observation {
	type key struct{ field, scope string }
	best := map[key]int{}
	rank := map[string]int{"high": 3, "medium": 2, "low": 1}
	statusRank := map[string]int{
		"observed": 4, "unresolved": 3, "historical": 1, "superseded": 0, "conflict": 2,
	}
	for i, o := range obs {
		k := key{o.Field, o.Scope}
		prev, ok := best[k]
		if !ok {
			best[k] = i
			continue
		}
		po := obs[prev]
		// Prefer non-historical statuses.
		if statusRank[o.Status] != statusRank[po.Status] {
			if statusRank[o.Status] > statusRank[po.Status] {
				best[k] = i
			}
			continue
		}
		// Prefer authoritative device dashboard / diagnostic over service-config endpoints.
		if roleRank(o.Role) != roleRank(po.Role) {
			if roleRank(o.Role) > roleRank(po.Role) {
				best[k] = i
			}
			continue
		}
		if rank[o.Confidence] > rank[po.Confidence] {
			best[k] = i
			continue
		}
		if rank[o.Confidence] == rank[po.Confidence] && o.Raw == po.Raw {
			// identical value — keep first
			continue
		}
		if rank[o.Confidence] == rank[po.Confidence] && o.Status == "observed" && po.Status == "observed" && o.Raw != po.Raw {
			// Conflict: keep higher-authority existing; mark loser superseded later
			continue
		}
	}
	for i := range obs {
		k := key{obs[i].Field, obs[i].Scope}
		if best[k] == i {
			if obs[i].Status == "historical" {
				// Historical never wins as selected current value when a better exists;
				// if it is the only candidate, keep status but mark selected for transparency? Design says retain behind history.
				// Leave selected=false for historical when another observed exists; if alone, still show as historical.
			}
			continue
		}
		if obs[i].Status != "historical" && obs[i].Status != "unresolved" {
			obs[i].Status = "superseded"
		}
	}
	return obs
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [16]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// CuratedOverviewKeys lists fields intended for primary Overview sections.
func CuratedOverviewKeys() []string {
	return []string{
		"product.family", "product.identity", "product.release", "product.build",
		"product.package_revision", "product.install_build", "product.installed_package",
		"product.aos_version", "product.raw_identifier", "product.install_time",
		"hostname", "network.ipv4", "collection.time",
		"os.pretty_name", "os.kernel", "platform", "cpu.model", "memory.total_kb", "uptime",
	}
}

func IsComponentField(field string) bool {
	return strings.HasPrefix(field, "component.")
}
