package profile

import "testing"

func TestSelectPrefersDeviceDashboardOverServiceLocalhost(t *testing.T) {
	obs := Resolve([]Observation{
		{Field: "hostname", Raw: "localhost", Confidence: "high", Status: "observed", Scope: "device", Role: RoleServiceConfig, SourceID: "cli", LineStart: 1, LineEnd: 1},
		{Field: "hostname", Raw: "sgve-example", Confidence: "high", Status: "observed", Scope: "device", Role: RoleDeviceDashboard, SourceID: "xml", LineStart: 5, LineEnd: 5},
	})
	var selected string
	for _, o := range obs {
		if o.Field == "hostname" && o.Status != "superseded" && o.Status != "historical" {
			selected = o.Raw
			break
		}
	}
	if selected != "sgve-example" {
		t.Fatalf("selected=%q", selected)
	}
}
