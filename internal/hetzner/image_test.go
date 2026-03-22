package hetzner

import "testing"

func TestArchFromServerType(t *testing.T) {
	tests := []struct {
		serverType string
		want       string
	}{
		{"cpx31", "amd64"},
		{"cpx42", "amd64"},
		{"cx22", "amd64"},
		{"cx33", "amd64"},
		{"cax11", "arm64"},
		{"cax21", "arm64"},
		{"cax31", "arm64"},
		{"ccx13", "amd64"},
		{"", "amd64"},
	}
	for _, tt := range tests {
		if got := ArchFromServerType(tt.serverType); got != tt.want {
			t.Errorf("ArchFromServerType(%q) = %q, want %q", tt.serverType, got, tt.want)
		}
	}
}

func TestBuilderServerType(t *testing.T) {
	tests := []struct {
		arch string
		want string
	}{
		{"amd64", "cx22"},
		{"arm64", "cax11"},
		{"", "cx22"},
	}
	for _, tt := range tests {
		if got := builderServerType(tt.arch); got != tt.want {
			t.Errorf("builderServerType(%q) = %q, want %q", tt.arch, got, tt.want)
		}
	}
}
