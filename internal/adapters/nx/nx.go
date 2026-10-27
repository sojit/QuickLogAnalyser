package nx

import (
	"bufio"
	"os"
	"path"
	"regexp"
	"strings"

	"quickanalyser/internal/profile"
)

var (
	rePackage = regexp.MustCompile(`(?i)\b(netxplorer)-(\d+\.\d+\.\d+)-(\d+)\.([a-z0-9_]+)`)
	reInstall = regexp.MustCompile(`(?i)Installation of NetXplorer.*?completed.*?build\s+([0-9.]+)|NetXplorer.*?([0-9]{2}\.[0-9]{2})`)
	reInstallBuild = regexp.MustCompile(`(?i)\bbuild\s+([0-9]{2}\.[0-9]{2})\b`)
	reInstallTS    = regexp.MustCompile(`(\d{4}-\d{2}-\d{2}\s+\d{2}:\d{2}:\d{2}\s+[+-]\d{2})`)
	reDBComp       = regexp.MustCompile(`(?i)^(cfg|ltc|stc|smf)\s*[:=]\s*(\S+)`)
	reHostnameLine = regexp.MustCompile(`(?i)^(?:HOSTNAME|hostname)\s*[=:]\s*(\S+)`)
	reStatusTime   = regexp.MustCompile(`(?i)(?:status|collection).*?(\d{4}-\d{2}-\d{2}\s+\d{2}:\d{2}:\d{2}\s*[+-]?\d{0,4})`)
	rePrettyOS     = regexp.MustCompile(`(?i)(Rocky Linux|CentOS|Red Hat|Ubuntu|Debian)[^\n]*`)
	reKernel       = regexp.MustCompile(`(?i)(?:Linux version |^uname[^\n]*\s)(\d+\.\d+\.\d+-\S+)`)
	reMemTotal     = regexp.MustCompile(`(?i)MemTotal:\s+(\d+)\s+kB`)
	reCPUModel     = regexp.MustCompile(`(?i)model name\s*:\s*(.+)`)
	reUptime       = regexp.MustCompile(`(?i)^uptime[:\s]+(.+)`)
	rePlatform     = regexp.MustCompile(`(?i)(VMware Virtual Platform|KVM|VirtualBox|Xen)`)
	reIPv4         = regexp.MustCompile(`(?i)\binet\s+(\d+\.\d+\.\d+\.\d+)`)
)

// Enrich extracts NX/NetXplorer profile observations from evidence when present.
// Operational signals (rollback, crash) are intentionally omitted — they belong in Log Highlights.
func Enrich(sources []profile.SourceInfo) []profile.Observation {
	var out []profile.Observation
	for _, src := range sources {
		base := strings.ToLower(path.Base(src.RawPath))
		lowPath := strings.ToLower(strings.ReplaceAll(src.RawPath, `\`, `/`))
		f, err := os.Open(src.CachePath)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		buf := make([]byte, 0, 64*1024)
		sc.Buffer(buf, 2<<20)
		lineNo := 0
		maxLines := 4000
		if strings.Contains(base, "systemstatus") {
			maxLines = 2500
		}
		for sc.Scan() {
			lineNo++
			if lineNo > maxLines {
				break
			}
			line := sc.Text()
			add := func(field, raw, conf, scope, status string) {
				out = append(out, profile.Observation{
					Field: field, Raw: raw, Normalized: raw, Confidence: conf,
					Status: status, Scope: scope, SourceID: src.ID,
					LineStart: lineNo, LineEnd: lineNo,
				})
			}

			if strings.Contains(base, "systemstatus") || strings.Contains(lowPath, "systemstatusdata") {
				if m := rePackage.FindStringSubmatch(line); m != nil {
					add("product.family", "NX / NetXplorer", "high", "device", "observed")
					add("product.installed_package", m[0], "high", "device", "observed")
					add("product.release", m[2], "high", "device", "observed")
					add("product.package_revision", m[3], "high", "device", "observed")
				}
				if m := reHostnameLine.FindStringSubmatch(line); m != nil {
					add("hostname", m[1], "high", "device", "observed")
				}
				if lineNo <= 10 {
					if m := reInstallTS.FindStringSubmatch(line); m != nil && strings.Contains(strings.ToLower(line), "status") {
						add("collection.time", m[1], "high", "device", "observed")
					}
					// Early status header often has bare timestamp
					if lineNo <= 8 {
						if m := reInstallTS.FindStringSubmatch(line); m != nil {
							add("collection.time", m[1], "medium", "device", "observed")
						}
					}
				}
				if m := rePrettyOS.FindStringSubmatch(line); m != nil {
					add("os.pretty_name", strings.TrimSpace(m[0]), "high", "device", "observed")
				}
				if m := reKernel.FindStringSubmatch(line); m != nil {
					add("os.kernel", m[1], "high", "device", "observed")
				}
				if m := reMemTotal.FindStringSubmatch(line); m != nil {
					add("memory.total_kb", m[1], "high", "device", "observed")
				}
				if m := reCPUModel.FindStringSubmatch(line); m != nil {
					add("cpu.model", strings.TrimSpace(m[1]), "high", "device", "observed")
				}
				if m := rePlatform.FindStringSubmatch(line); m != nil {
					add("platform", m[1], "high", "device", "observed")
				}
				if m := reUptime.FindStringSubmatch(line); m != nil {
					add("uptime", strings.TrimSpace(m[1]), "medium", "device", "observed")
				}
				if m := reIPv4.FindStringSubmatch(line); m != nil && !strings.HasPrefix(m[1], "127.") {
					add("network.ipv4", m[1], "medium", "device", "observed")
				}
			}

			if strings.Contains(base, "install_log") || (strings.Contains(lowPath, "/conf/") && strings.Contains(base, "install")) {
				if m := reInstallBuild.FindStringSubmatch(line); m != nil {
					add("product.install_build", m[1], "high", "device", "observed")
				}
				if m := reInstall.FindStringSubmatch(line); m != nil {
					build := m[1]
					if build == "" {
						build = m[2]
					}
					if build != "" {
						add("product.install_build", build, "high", "device", "observed")
					}
				}
				if m := reInstallTS.FindStringSubmatch(line); m != nil {
					add("product.install_time", m[1], "high", "device", "observed")
				}
				if strings.Contains(strings.ToLower(line), "netxplorer") {
					add("product.family", "NX / NetXplorer", "high", "device", "observed")
				}
			}

			if base == "dbversion.txt" || strings.Contains(base, "dbversion") {
				if m := reDBComp.FindStringSubmatch(line); m != nil {
					add("component.schema."+strings.ToLower(m[1]), m[2], "high", "component", "observed")
				} else if parts := strings.Fields(line); len(parts) >= 2 {
					key := strings.ToLower(strings.Trim(parts[0], ":="))
					if key == "cfg" || key == "ltc" || key == "stc" || key == "smf" {
						add("component.schema."+key, parts[len(parts)-1], "high", "component", "observed")
					}
				}
			}

			// Historical installer log — mark as historical so it cannot win over installed package.
			if strings.Contains(base, "allot_netxplorer_inst") || strings.Contains(lowPath, "allot_netxplorer_inst") {
				if m := rePackage.FindStringSubmatch(line); m != nil {
					add("product.release", m[2], "medium", "device", "historical")
					add("product.installed_package", m[0], "medium", "device", "historical")
				}
			}
		}
		_ = f.Close()
	}
	return out
}
