package masking

import "testing"

func TestLineMasksPasswordForms(t *testing.T) {
	cases := []struct {
		in, mustNot string
	}{
		{`password=dummy-secret`, "dummy-secret"},
		{`{"password":"dummy-secret"}`, "dummy-secret"},
		{`Authorization: Bearer abc.def.ghi`, "abc.def.ghi"},
		{`api_key: "supersecret"`, "supersecret"},
	}
	for _, c := range cases {
		out := Line(c.in)
		if out == c.in {
			t.Fatalf("unchanged: %q", c.in)
		}
		if contains(out, c.mustNot) {
			t.Fatalf("secret leaked in %q", out)
		}
	}
}

func contains(s, sub string) bool {
	return len(sub) > 0 && (s == sub || len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
