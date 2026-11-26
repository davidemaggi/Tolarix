package obsidian

import "testing"

func TestSanitizeNameAvoidsInvalidFileNames(t *testing.T) {
	for in, want := range map[string]string{
		"Getting started":  "Getting started",
		"a/b: c?":          "a-b- c-",
		"..":               "Untitled",
		" . ":              "Untitled",
		"Trailing dots...": "Trailing dots",
		"CON":              "CON_",
		"nul":              "nul_",
		"com1":             "com1_",
		"Com1.v2":          "Com1_.v2",
		"COM0":             "COM0",
		"Console":          "Console",
		"LPT10":            "LPT10",
	} {
		if got := sanitizeName(in); got != want {
			t.Errorf("sanitizeName(%q) = %q, want %q", in, got, want)
		}
	}
	if got := sanitizeFileName("aux.PNG"); got != "aux_.png" {
		t.Errorf("sanitizeFileName = %q", got)
	}
}
