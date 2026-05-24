package sampler

import "testing"

func TestCleanTitle(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"Did The Test Of Time - YouTube - Chromium", "Did The Test Of Time - YouTube"},
		{"main.go - screentyme - Visual Studio Code", "main.go - screentyme"},
		{"~/Work/Sidechick", "~/Work/Sidechick"},
		{"Home / X - Chromium", "Home / X"},
		{"ba llb course in nepal - Google Search - Chromium", "ba llb course in nepal - Google Search"},
		{"", ""},
		{" - ", ""},
		{"No separator here", "No separator here"},
		{"Only one - separator", "Only one"},
	}
	for _, tc := range cases {
		got := CleanTitle(tc.in)
		if got != tc.want {
			t.Errorf("CleanTitle(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
