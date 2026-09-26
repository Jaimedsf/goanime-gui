package util

import "testing"

func TestForLog(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, in, want string
	}{
		{"plain text is untouched", "Português (Brasil)", "Português (Brasil)"},
		{"newline cannot start a forged entry", "pt\n2026/09/26 fake entry", "pt 2026/09/26 fake entry"},
		{"carriage return cannot overwrite the line", "pt\rhidden", "pt hidden"},
		{"ANSI escape is dropped", "\x1b[2Jcleared", "[2Jcleared"},
		{"tab survives", "a\tb", "a\tb"},
		{"other control characters are dropped", "a\x00b\x07c", "abc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := ForLog(tt.in); got != tt.want {
				t.Errorf("ForLog(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
