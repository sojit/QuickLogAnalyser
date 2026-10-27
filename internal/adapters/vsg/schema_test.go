package vsg

import (
	"os"
	"path/filepath"
	"testing"

	"quickanalyser/internal/profile"
)

func TestEnrichRealSystemPrefixedSchema(t *testing.T) {
	root := filepath.Join("..", "..", "..", "testdata", "sanitized", "sgve_schema")
	xmlPath := filepath.Join(root, "AOSDashboard", "system_info_lf.xml")
	if _, err := os.Stat(xmlPath); err != nil {
		t.Fatal(err)
	}
	infoPath := filepath.Join(root, "linux", "var", "log", "acp", "allotInfo-23_10_2026-07_21_40.log")
	cliPath := filepath.Join(root, "conf", "dataCli.conf")
	sources := []profile.SourceInfo{
		{ID: "xml", RawPath: "AOSDashboard/system_info_lf.xml", CachePath: xmlPath},
		{ID: "info", RawPath: "linux/var/log/acp/allotInfo-23_10_2026-07_21_40.log", CachePath: infoPath},
		{ID: "cli", RawPath: "conf/dataCli.conf", CachePath: cliPath, SourceKind: "configuration"},
	}
	obs := profile.Resolve(append(profile.ObserveGeneric(sources), Enrich(sources)...))
	selected := map[string]string{}
	for _, o := range obs {
		if o.Status == "superseded" || o.Status == "historical" {
			continue
		}
		if _, ok := selected[o.Field]; !ok {
			selected[o.Field] = o.Raw
		}
	}
	want := map[string]string{
		"hostname":                "sgve-example",
		"network.ipv4":            "192.0.2.10",
		"product.release":         "20.20.10",
		"product.build":           "257",
		"component.protocol_pack": "3.275.216",
		"product.family":          "vSG / SGVE",
		"component.acp_version":   "19.7.1",
		"os.pretty_name":          "Rocky Linux 9.7",
	}
	for k, v := range want {
		if selected[k] != v {
			t.Fatalf("%s=%q want %q (selected=%v)", k, selected[k], v, selected)
		}
	}
	if selected["hostname"] == "localhost" {
		t.Fatal("localhost service config must not win device hostname")
	}
}

func TestLegacyUnprefixedStillWorks(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "system_info_lf.xml")
	body := `<system_info>
  <hostname>legacy-host</hostname>
  <protocol_pack>1.2.3</protocol_pack>
  <aos_version>20.20.10 Build 257</aos_version>
  <product_type>SGVE</product_type>
  <acp_version>9.9.9</acp_version>
</system_info>`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	obs := profile.Resolve(Enrich([]profile.SourceInfo{{
		ID: "s", RawPath: "AOSDashboard/system_info_lf.xml", CachePath: p,
	}}))
	m := map[string]string{}
	for _, o := range obs {
		if o.Status != "superseded" && o.Status != "historical" {
			m[o.Field] = o.Raw
		}
	}
	if m["hostname"] != "legacy-host" || m["product.release"] != "20.20.10" {
		t.Fatalf("%v", m)
	}
}
