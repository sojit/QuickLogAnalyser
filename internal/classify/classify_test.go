package classify

import (
	"strings"
	"testing"
)

func TestAuditNonLogContentMustNotBecomeLogs(t *testing.T) {
	cases := []struct {
		name, path, body string
		want             Kind
	}{
		{"ansible_comment", "NX/conf/ha/drbd/ansible.cfg",
			"# If 'true' unparsed inventory sources become fatal errors, they are warnings otherwise.\n", KindConfig},
		{"shell_script", "NX/conf/ha/GeoRedundancy.sh",
			"#!/bin/sh\necho \"warning: connection refused\"\n", KindDiagnostic},
		{"mib_definition", "NX/conf/ha/COROSYNC-MIB.MIB",
			"COROSYNC-MIB DEFINITIONS ::= BEGIN\nDESCRIPTION \"A warning notification\"\nEND\n", KindDiagnostic},
		{"config_under_log", "NX/log/app.conf",
			"# warning: choose a timeout\ntimeout=30\n", KindConfig},
		{"diagnostic", "NX/diagnostic/status.txt",
			"warning: command not found\n", KindDiagnostic},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ClassifyMember(c.path, []byte(c.body), false)
			if got.Kind == KindLog {
				t.Fatalf("non-log classified as log: kind=%s reason=%s conf=%s", got.Kind, got.Reason, got.Confidence)
			}
			if got.Kind != c.want {
				t.Fatalf("want %s got %s reason=%s", c.want, got.Kind, got.Reason)
			}
		})
	}
}

func TestGenuineLogsStillDetected(t *testing.T) {
	cases := []struct {
		name, path, body string
	}{
		{"messages", "root/log/messages", "Oct 23 11:00:01 host kernel: oops\nOct 23 11:00:02 host kernel: recover\n"},
		{"log_under_conf", "root/conf/custom/app.log", "2026-10-23 11:00:01 ERROR boom\n2026-10-23 11:00:02 INFO ok\n"},
		{"gz_name", "root/var/log/app.log.gz", "", /* name alone */},
		{"json_ndjson", "root/opt/svc/out.json",
			"{\"timestamp\":\"2026-10-23T11:00:01Z\",\"level\":\"error\",\"message\":\"fail\"}\n{\"timestamp\":\"2026-10-23T11:00:02Z\",\"level\":\"info\",\"message\":\"ok\"}\n"},
		{"extensionless_syslog", "root/var/log/messages", "Oct 23 11:00:01 host sshd: fail\nOct 23 11:00:02 host sshd: ok\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gz := c.name == "gz_name"
			body := c.body
			if gz {
				body = "" // path-based; NeedsGzip
			}
			got := ClassifyMember(c.path, []byte(body), gz)
			if got.Kind != KindLog {
				t.Fatalf("want log got %s reason=%s", got.Kind, got.Reason)
			}
		})
	}
}

func TestClassifyBasics(t *testing.T) {
	r := ClassifyMember("root/conf/install_log.txt", []byte("Installation of NetXplorer completed successfully"), false)
	if r.Kind != KindConfig {
		t.Fatalf("install_log: %v", r)
	}
	r = ClassifyMember("root/core.datasrv.123", nil, false)
	if r.Kind != KindBinary {
		t.Fatalf("core: %v", r)
	}
	r = ClassifyMember("root/var/log/app.log.gz", nil, true)
	if r.Kind != KindLog || !r.NeedsGzip {
		t.Fatalf("gz log: %v", r)
	}
	if Version != "1.3.1" {
		t.Fatalf("classifier version want 1.3.1 got %s", Version)
	}
}

func TestAuditAllNamedLogsSurviveEarlierExclusions(t *testing.T) {
	// Audit 2026-10-27: path-wide /core. and basename "-mib" must not hide *.log.
	for _, p := range []string{
		"snapshot/core.collector/service.log",
		"snapshot/logs/device-mib.log",
	} {
		t.Run(p, func(t *testing.T) {
			r := ClassifyMember(p, []byte("2026-10-27 10:00:00 INFO test\n"), false)
			if r.Kind != KindLog {
				t.Errorf("named .log hidden: kind=%s reason=%s", r.Kind, r.Reason)
			}
			if !r.KeepText {
				t.Errorf("text named log should KeepText: %v", r)
			}
		})
	}
}

func TestLogDirNonTextStateNotReadableLog(t *testing.T) {
	// NUL-bearing extensionless state under /logs/ must not be decoded as text log.
	body := []byte("table\x00history\x00state")
	r := ClassifyMember("product/logs/table_history.state", body, false)
	if r.Kind == KindLog && r.KeepText {
		t.Fatalf("binary state under logs must not be readable log: %v", r)
	}
	if r.KeepText {
		t.Fatalf("KeepText must be false for non-text state: %v", r)
	}
}

func TestRetainedSGVEExtensionlessKeyValueUnderLogs(t *testing.T) {
	// Retained residual (draft.23 audit): extensionless SGVE-like key=value under
	// /logs/ still classifies via log_dir_settings / config heuristics, not as
	// named .log. Documented until a dedicated extensionless-log contract lands.
	r := ClassifyMember("sgve/logs/ctmMsgChanges", []byte("key=value\nmode=safe\n"), false)
	if r.Kind == KindLog {
		t.Fatalf("unexpected KindLog for extensionless kv under logs without named-log contract: %v", r)
	}
}

func TestNamedLogBasenameAlwaysLog(t *testing.T) {
	cases := []struct {
		name, path, body string
		wantKeep         bool
		wantReason       string
	}{
		{"kv_under_log", "product/log/agent.log", "timeout=30\nmode=safe\nwarning=1\n", true, "named_log_basename"},
		{"kv_under_conf", "product/conf/custom/app.log", "key=value\nenabled=true\n", true, "named_log_basename"},
		{"empty", "var/log/empty.log", "", true, "named_log_basename"},
		{"hidden", "opt/svc/.agent.log", "2026-10-23 INFO start\n", true, "named_log_basename"},
		{"rotation_num", "var/log/app.log.1", "line\n", true, "named_log_basename"},
		{"rotation_gz", "var/log/app.log.12.gz", "", true, "named_log_basename"},
		{"rotation_date", "var/log/app.log.2026-10-23", "x\n", true, "named_log_basename"},
		{"binary_named", "var/log/blob.log", string([]byte{0x00, 0x01, 0xff, 0xfe, 0x00, 0x10}), false, "named_log_non_text_content"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ClassifyMember(c.path, []byte(c.body), strings.HasSuffix(c.path, ".gz"))
			if got.Kind != KindLog {
				t.Fatalf("want log got %s reason=%s", got.Kind, got.Reason)
			}
			if got.KeepText != c.wantKeep {
				t.Fatalf("KeepText want %v got %v reason=%s", c.wantKeep, got.KeepText, got.Reason)
			}
			if got.Reason != c.wantReason {
				t.Fatalf("reason want %s got %s", c.wantReason, got.Reason)
			}
		})
	}
}

func TestNamedLogDoesNotCatchNonLogNames(t *testing.T) {
	cases := []struct {
		name, path, body string
		want             Kind // empty = any non-log
	}{
		{"csv_with_log_letters", "catalog/config.csv", "a,b\n1,2\n", ""},
		{"logistics_name", "data/catalog.logistics", "warning=1\n", ""},
		{"settings_in_logs", "product/logs/app.conf", "timeout=30\n", KindConfig},
		{"script", "bin/run.sh", "#!/bin/sh\necho warning\n", KindDiagnostic},
		{"child_under_dot_log_dir", "backup.log/settings.json", "{\"a\":1}\n", KindConfig},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ClassifyMember(c.path, []byte(c.body), false)
			if got.Kind == KindLog {
				t.Fatalf("must not be log: %v", got)
			}
			if c.want != "" && got.Kind != c.want {
				t.Fatalf("want %s got %s reason=%s", c.want, got.Kind, got.Reason)
			}
		})
	}
}
