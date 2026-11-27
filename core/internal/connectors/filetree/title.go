package filetree

import (
	"strings"
	"unicode"
)

// CleanTitle prepares a page title for use as a file name: tabs and line
// breaks become spaces (a title like "3.1\tIntro" keeps its words apart),
// other control characters are dropped, and surrounding spaces are trimmed.
func CleanTitle(s string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		switch {
		case r == '\t' || r == '\n' || r == '\r' || r == '\v' || r == '\f':
			return ' '
		case unicode.IsControl(r) || unicode.Is(unicode.Cs, r):
			return -1
		}
		return r
	}, s))
}
