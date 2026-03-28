package main

import (
	"strings"
	"testing"
)

func TestPlural(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{0, "s"},
		{1, ""},
		{2, "s"},
		{7, "s"},
		{100, "s"},
	}
	for _, tt := range tests {
		got := plural(tt.n)
		if got != tt.want {
			t.Errorf("plural(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

func TestFmtNum(t *testing.T) {
	tests := []struct {
		n    int64
		want string
	}{
		{0, "0"},
		{1, "1"},
		{999, "999"},
		{1000, "1.0k"},
		{1500, "1.5k"},
		{10_000, "10.0k"},
		{999_999, "1000.0k"},
		{1_000_000, "1.0M"},
		{1_500_000, "1.5M"},
		{10_000_000, "10.0M"},
	}
	for _, tt := range tests {
		got := fmtNum(tt.n)
		if got != tt.want {
			t.Errorf("fmtNum(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		s    string
		max  int
		want string
	}{
		{"short", 10, "short"},
		{"exact-fit!", 10, "exact-fit!"},
		{"this is way too long", 10, "this is w…"},
		{"", 5, ""},
		{"ab", 2, "ab"},
		{"abc", 2, "a…"},
	}
	for _, tt := range tests {
		got := truncate(tt.s, tt.max)
		if got != tt.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.s, tt.max, got, tt.want)
		}
	}
}

func TestRenderBar(t *testing.T) {
	// renderBar returns styled strings; strip ANSI to verify bar content.
	strip := func(s string) string {
		// ANSI escape codes start with ESC[ and end with 'm'
		var buf strings.Builder
		inEsc := false
		for _, r := range s {
			if r == '\x1b' {
				inEsc = true
				continue
			}
			if inEsc {
				if r == 'm' {
					inEsc = false
				}
				continue
			}
			buf.WriteRune(r)
		}
		return buf.String()
	}

	tests := []struct {
		rate  float64
		width int
		want  string
	}{
		{0.0, 10, "░░░░░░░░░░"},
		{0.5, 10, "█████░░░░░"},
		{1.0, 10, "██████████"},
		{1.5, 10, "██████████"}, // clamped at width
		{0.0, 20, "░░░░░░░░░░░░░░░░░░░░"},
		{1.0, 20, "████████████████████"},
	}
	for _, tt := range tests {
		got := strip(renderBar(tt.rate, tt.width))
		if got != tt.want {
			t.Errorf("renderBar(%.1f, %d) = %q, want %q", tt.rate, tt.width, got, tt.want)
		}
	}
}
