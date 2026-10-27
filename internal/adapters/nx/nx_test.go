package nx

import (
	"os"
	"path/filepath"
	"testing"

	"quickanalyser/internal/profile"
)

func TestEnrichInstalledReleaseNotHistorical(t *testing.T) {
	dir := t.TempDir()
	status := filepath.Join(dir, "systemStatusData.log")
	install := filepath.Join(dir, "install_log.txt")
	dbver := filepath.Join(dir, "dbversion.txt")
	hist := filepath.Join(dir, "Allot_netxplorer_inst.log")
	mustWrite(t, status, "hostname = nx-example.invalid\n"+
		"Status collection 2026-10-23 11:38:03 +04\n"+
		"netxplorer-20.21.10-9.x86_64\n"+
		"Rocky Linux 9.7 (Blue Onyx)\n"+
		"Linux version 5.14.0-611.16.1.el9_7.x86_64\n"+
		"MemTotal:       16109352 kB\n"+
		"model name      : Intel Core Ultra 9 275HX\n"+
		"VMware Virtual Platform\n"+
		"inet 192.0.2.20\n")
	mustWrite(t, install, "Installation of NetXplorer completed successfully build 09.09 at 2026-10-13 16:14:15 +04\n")
	mustWrite(t, dbver, "cfg: 20.21.10b09\nltc: 20.21.10b09\nstc: 20.21.10b09\nsmf: 20.21.10b09\n")
	mustWrite(t, hist, "netxplorer-20.20.10-28.x86_64\n")

	obs := Enrich([]profile.SourceInfo{
		{ID: "s1", RawPath: "log/systemStatusData.log", CachePath: status},
		{ID: "s2", RawPath: "conf/install_log.txt", CachePath: install},
		{ID: "s3", RawPath: "conf/dbversion.txt", CachePath: dbver},
		{ID: "s4", RawPath: "log/Allot_netxplorer_inst.log", CachePath: hist},
	})
	resolved := profile.Resolve(obs)
	got := map[string]profile.Observation{}
	for _, o := range resolved {
		if o.Status != "superseded" && o.Status != "historical" {
			got[o.Field] = o
		}
	}
	if got["product.release"].Raw != "20.21.10" {
		t.Fatalf("release=%v", got["product.release"])
	}
	if got["product.package_revision"].Raw != "9" {
		t.Fatalf("pkg rev=%v", got["product.package_revision"])
	}
	if got["product.install_build"].Raw != "09.09" {
		t.Fatalf("install build=%v", got["product.install_build"])
	}
	if got["component.schema.cfg"].Raw != "20.21.10b09" {
		t.Fatalf("cfg=%v", got["component.schema.cfg"])
	}
	for _, o := range resolved {
		if profile.OperationalField(o.Field) {
			t.Fatalf("operational field leaked: %s", o.Field)
		}
		if o.Field == "product.release" && o.Raw == "20.20.10" && o.Status != "historical" && o.Status != "superseded" {
			t.Fatal("historical release must not be selected")
		}
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
