package vsg

import (
	"os"
	"path/filepath"
	"testing"

	"quickanalyser/internal/profile"
)

func TestEnrichAOSVersion(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "system_info_lf.xml")
	body := `<system_info>
  <hostname>sgve-example</hostname>
  <protocol_pack>3.275.216</protocol_pack>
  <aos_version>20.20.10 Build 257</aos_version>
  <product_type>SGVE</product_type>
  <acp_version>1.2.3</acp_version>
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
	if m["product.family"] != "vSG / SGVE" {
		t.Fatalf("family=%q", m["product.family"])
	}
	if m["product.release"] != "20.20.10" {
		t.Fatalf("release=%q", m["product.release"])
	}
	if m["product.build"] != "257" {
		t.Fatalf("build=%q", m["product.build"])
	}
	if m["component.protocol_pack"] != "3.275.216" {
		t.Fatalf("pp=%q", m["component.protocol_pack"])
	}
	if m["component.acp_version"] != "1.2.3" {
		t.Fatalf("acp=%q", m["component.acp_version"])
	}
}
