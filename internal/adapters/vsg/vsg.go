package vsg

import (
	"bufio"
	"os"
	"path"
	"regexp"
	"strings"

	"quickanalyser/internal/profile"
)

var (
	reAOSVer       = regexp.MustCompile(`(?i)(\d+\.\d+\.\d+)\s+Build\s+(\d+)`)
	reAOSPacked    = regexp.MustCompile(`(?i)AOS\.SGVE\.(\d+\.\d+\.\d+)_B(\d+)`)
	reAllotACP     = regexp.MustCompile(`(?i)\bACP\s*[:=]\s*([0-9]+(?:\.[0-9]+)*)`)
	reAllotOS      = regexp.MustCompile(`(?i)\b(?:OS|Operating System)\s*[:=]\s*(.+)`)
	reRockyLine    = regexp.MustCompile(`(?i)\b(Rocky Linux[^\n,;]*)`)
	reKernelLine   = regexp.MustCompile(`(?i)\b(?:Kernel|uname)\s*[:=]\s*([^\s]+)`)
	reCPULine      = regexp.MustCompile(`(?i)\b(?:CPU|processor)\s*[:=]\s*(.+)`)
	reUptimeLine   = regexp.MustCompile(`(?i)\buptime\s*[:=]\s*(.+)`)
	rePlatformLine = regexp.MustCompile(`(?i)\b(?:Platform|Hypervisor|DMI|Product Name)\s*[:=]\s*(.+)`)
	// Bounded element extractors for AOSDashboard/system_info_lf.xml real schema and legacy aliases.
	reSystemTag = regexp.MustCompile(`(?i)<(system_hostname|system_ip|system_protocol_pack_version|system_aos_version|system_device_type|system_acp_version|hostname|host_name|aos_version|product_type|product|protocol_pack|protocol_pack_version|acp_version|management_ip)[^>]*>([^<]+)</`)
)

// Enrich extracts SGVE/vSG profile observations from real dashboard/diagnostic schemas.
// Recognizes system_* tags in AOSDashboard/system_info_lf.xml and bounded allotInfo lines.
// Never invents sample values; ACP/protocol-pack/AOS/OS remain separate scopes.
func Enrich(sources []profile.SourceInfo) []profile.Observation {
	var out []profile.Observation
	for _, src := range sources {
		lowPath := strings.ToLower(strings.ReplaceAll(src.RawPath, `\`, `/`))
		base := strings.ToLower(path.Base(src.RawPath))
		switch {
		case strings.Contains(base, "system_info") || strings.Contains(lowPath, "aosdashboard"):
			out = append(out, enrichSystemInfo(src)...)
		case strings.Contains(lowPath, "allotinfo") || strings.Contains(base, "allotinfo"):
			out = append(out, enrichAllotInfo(src)...)
		case strings.Contains(lowPath, "sgve") || strings.Contains(lowPath, "vsg") || strings.Contains(lowPath, "/aos"):
			out = append(out, enrichSystemInfo(src)...)
		}
	}
	return out
}

func enrichSystemInfo(src profile.SourceInfo) []profile.Observation {
	f, err := os.Open(src.CachePath)
	if err != nil {
		return nil
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 2<<20)
	var out []profile.Observation
	lineNo := 0
	for sc.Scan() {
		lineNo++
		if lineNo > 20000 {
			break
		}
		for _, m := range reSystemTag.FindAllStringSubmatch(sc.Text(), -1) {
			out = append(out, mapSystemInfoField(src, strings.ToLower(m[1]), strings.TrimSpace(m[2]), lineNo)...)
		}
	}
	return out
}

func mapSystemInfoField(src profile.SourceInfo, key, val string, line int) []profile.Observation {
	add := func(field, raw, conf, scope, role string) profile.Observation {
		return profile.Observation{
			Field: field, Raw: raw, Normalized: raw, Confidence: conf,
			Status: "observed", Scope: scope, Role: role, SourceID: src.ID,
			LineStart: line, LineEnd: line,
		}
	}
	var out []profile.Observation
	switch key {
	case "system_hostname", "hostname", "host_name":
		out = append(out, add("hostname", val, "high", "device", profile.RoleDeviceDashboard))
	case "system_ip", "management_ip":
		out = append(out, add("network.ipv4", val, "high", "device", profile.RoleDeviceDashboard))
	case "system_protocol_pack_version", "protocol_pack", "protocol_pack_version":
		out = append(out, add("component.protocol_pack", val, "high", "component", profile.RoleDeviceDashboard))
	case "system_aos_version", "aos_version":
		out = append(out, add("product.aos_version", val, "high", "device", profile.RoleDeviceDashboard))
		if m := reAOSVer.FindStringSubmatch(val); m != nil {
			out = append(out, add("product.release", m[1], "high", "device", profile.RoleDeviceDashboard))
			out = append(out, add("product.build", m[2], "high", "device", profile.RoleDeviceDashboard))
		} else {
			out = append(out, add("product.release", val, "medium", "device", profile.RoleDeviceDashboard))
		}
	case "system_device_type", "product_type", "product":
		low := strings.ToLower(val)
		if strings.Contains(low, "sgve") || low == "vsg" {
			out = append(out, add("product.family", "vSG / SGVE", "high", "device", profile.RoleDeviceDashboard))
		}
		out = append(out, add("product.raw_identifier", val, "high", "device", profile.RoleDeviceDashboard))
	case "system_acp_version", "acp_version":
		out = append(out, add("component.acp_version", val, "high", "component", profile.RoleDeviceDashboard))
	}
	return out
}

func enrichAllotInfo(src profile.SourceInfo) []profile.Observation {
	f, err := os.Open(src.CachePath)
	if err != nil {
		return nil
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 2<<20)
	var out []profile.Observation
	lineNo := 0
	add := func(field, raw, conf, scope string) {
		out = append(out, profile.Observation{
			Field: field, Raw: strings.TrimSpace(raw), Normalized: strings.TrimSpace(raw),
			Confidence: conf, Status: "observed", Scope: scope, Role: profile.RoleDiagnostic,
			SourceID: src.ID, LineStart: lineNo, LineEnd: lineNo,
		})
	}
	for sc.Scan() {
		lineNo++
		if lineNo > 20000 {
			break
		}
		line := sc.Text()
		if m := reAOSPacked.FindStringSubmatch(line); m != nil {
			add("product.release", m[1], "medium", "device")
			add("product.build", m[2], "medium", "device")
			add("product.aos_version", "AOS.SGVE."+m[1]+"_B"+m[2], "medium", "device")
		}
		if m := reAllotACP.FindStringSubmatch(line); m != nil {
			add("component.acp_version", m[1], "high", "component")
		}
		if m := reRockyLine.FindStringSubmatch(line); m != nil {
			add("os.pretty_name", strings.TrimSpace(m[1]), "high", "device")
		} else if m := reAllotOS.FindStringSubmatch(line); m != nil {
			add("os.pretty_name", strings.TrimSpace(m[1]), "medium", "device")
		}
		if m := reKernelLine.FindStringSubmatch(line); m != nil {
			add("os.kernel", strings.TrimSpace(m[1]), "medium", "device")
		}
		if m := reCPULine.FindStringSubmatch(line); m != nil {
			add("cpu.model", strings.TrimSpace(m[1]), "medium", "device")
		}
		if m := reUptimeLine.FindStringSubmatch(line); m != nil {
			add("uptime", strings.TrimSpace(m[1]), "low", "device")
		}
		if m := rePlatformLine.FindStringSubmatch(line); m != nil {
			add("platform", strings.TrimSpace(m[1]), "medium", "device")
		}
		low := strings.ToLower(line)
		if strings.Contains(low, "sgve") && !strings.Contains(low, "error") {
			add("product.family", "vSG / SGVE", "medium", "device")
		}
	}
	return out
}
