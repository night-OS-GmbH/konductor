package cluster

import "testing"

func TestParseTalosVersion(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Talos (v1.11.5)", "v1.11.5"},
		{"Talos (v1.9.0)", "v1.9.0"},
		{"Talos (v2.0.0)", "v2.0.0"},
		{"Ubuntu 22.04 LTS", ""},
		{"", ""},
		{"short", ""},
		{"no version here", ""},
		{"Talos (v1.11.5-beta.1)", "v1.11.5"}, // stops at non-digit/dot
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseTalosVersion(tt.input)
			if got != tt.want {
				t.Errorf("parseTalosVersion(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestRecommendedVersions(t *testing.T) {
	expected := []string{"metrics-server", "hetzner-ccm", "cert-manager", "konductor-operator"}
	for _, name := range expected {
		if _, ok := RecommendedVersions[name]; !ok {
			t.Errorf("RecommendedVersions missing entry for %q", name)
		}
	}
}
