package main

import "testing"

func TestParseEnvBinding(t *testing.T) {
	cases := []struct {
		spec, name, env string
		ok              bool
	}{
		{"GITHUB_TOKEN", "GITHUB_TOKEN", "GITHUB_TOKEN", true},
		{"GITHUB_TOKEN=GH_TOKEN", "GITHUB_TOKEN", "GH_TOKEN", true},
		{"API_KEY=api_key", "API_KEY", "api_key", true},
		{"lower", "", "", false},
		{"A=1BAD", "", "", false},
		{"A=", "", "", false},
		{"=A", "", "", false},
	}
	for _, c := range cases {
		n, e, err := parseEnvBinding(c.spec)
		if (err == nil) != c.ok {
			t.Errorf("parseEnvBinding(%q) err = %v, want ok=%v", c.spec, err, c.ok)
			continue
		}
		if c.ok && (n != c.name || e != c.env) {
			t.Errorf("parseEnvBinding(%q) = %q, %q", c.spec, n, e)
		}
	}
}
