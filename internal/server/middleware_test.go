package server

import "testing"

func TestRedactQuery(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"id=foo&v=1", "id=foo&v=1"},
		{"token=secret123", "token=REDACTED"},
		{"id=foo&token=secret&v=1", "id=foo&token=REDACTED&v=1"},
		{"id=foo&v=1&fmt=deb", "id=foo&v=1&fmt=deb"},
	}
	for _, tt := range tests {
		if got := redactQuery(tt.input); got != tt.want {
			t.Errorf("redactQuery(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
