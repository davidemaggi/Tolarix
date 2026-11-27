package filetree

import "testing"

func TestCleanTitle(t *testing.T) {
	for in, want := range map[string]string{
		"3.1.5\tReporter Additional Info": "3.1.5 Reporter Additional Info",
		"Line\nbreak":                     "Line break",
		" padded\t":                       "padded",
		"bell\x07":                        "bell",
		"plain":                           "plain",
	} {
		if got := CleanTitle(in); got != want {
			t.Errorf("CleanTitle(%q) = %q, want %q", in, got, want)
		}
	}
}
