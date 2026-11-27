// Package mdscan finds link-like syntax in Markdown without touching code.
//
// Converters rewrite links with regular expressions; mdscan tells them which
// byte ranges are "protected" (fenced code blocks, inline code spans, HTML
// comments, Obsidian %% comments) so a match starting there is left alone.
// It deliberately does not build an AST: unchanged text stays byte-identical,
// which keeps golden files readable and round trips exact.
package mdscan

import (
	"html"
	"regexp"
	"strings"
)

// Span is a half-open byte range [Start, End).
type Span struct{ Start, End int }

// Protected returns the sorted, non-overlapping protected spans of src.
func Protected(src string) []Span {
	var spans []Span
	lines := splitLinesKeepEnds(src)
	pos := 0
	inFence := false
	var fenceChar byte
	fenceLen, fenceStart := 0, 0
	// Inline scanning works on runs of non-fenced lines.
	runStart := 0
	flushRun := func(end int) {
		if end > runStart {
			spans = append(spans, inlineProtected(src[runStart:end], runStart)...)
		}
	}
	for _, ln := range lines {
		trimmed := strings.TrimLeft(ln, " ")
		indent := len(ln) - len(trimmed)
		if !inFence {
			if c, n := fenceOpen(trimmed); n > 0 && indent <= 3 {
				flushRun(pos)
				inFence, fenceChar, fenceLen, fenceStart = true, c, n, pos
			}
		} else if indent <= 3 && isFenceClose(trimmed, fenceChar, fenceLen) {
			spans = append(spans, Span{fenceStart, pos + len(ln)})
			inFence = false
			runStart = pos + len(ln)
		}
		pos += len(ln)
	}
	if inFence {
		spans = append(spans, Span{fenceStart, len(src)})
	} else {
		flushRun(len(src))
	}
	return spans
}

func fenceOpen(s string) (byte, int) {
	if s == "" || (s[0] != '`' && s[0] != '~') {
		return 0, 0
	}
	c := s[0]
	n := 0
	for n < len(s) && s[n] == c {
		n++
	}
	if n < 3 {
		return 0, 0
	}
	if c == '`' && strings.Contains(s[n:], "`") {
		return 0, 0 // backtick fences cannot have backticks in the info string
	}
	return c, n
}

func isFenceClose(s string, c byte, n int) bool {
	s = strings.TrimRight(s, " \t\r\n")
	if len(s) < n {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] != c {
			return false
		}
	}
	return true
}

// inlineProtected finds code spans, HTML comments and %% comments in text
// outside fenced blocks. offset is added to every span.
func inlineProtected(s string, offset int) []Span {
	var spans []Span
	i := 0
	for i < len(s) {
		switch {
		case s[i] == '\\' && i+1 < len(s):
			i += 2
			continue
		case s[i] == '`':
			n := run(s, i, '`')
			if end := findBacktickRun(s, i+n, n); end >= 0 {
				spans = append(spans, Span{offset + i, offset + end + n})
				i = end + n
				continue
			}
			i += n
			continue
		case strings.HasPrefix(s[i:], "<!--"):
			end := strings.Index(s[i+4:], "-->")
			stop := len(s)
			if end >= 0 {
				stop = i + 4 + end + 3
			}
			spans = append(spans, Span{offset + i, offset + stop})
			i = stop
			continue
		case strings.HasPrefix(s[i:], "%%"):
			end := strings.Index(s[i+2:], "%%")
			if end >= 0 {
				stop := i + 2 + end + 2
				spans = append(spans, Span{offset + i, offset + stop})
				i = stop
				continue
			}
		}
		i++
	}
	return spans
}

func run(s string, i int, c byte) int {
	n := 0
	for i+n < len(s) && s[i+n] == c {
		n++
	}
	return n
}

// findBacktickRun returns the index of the next run of exactly n backticks.
func findBacktickRun(s string, from, n int) int {
	for i := from; i < len(s); {
		if s[i] != '`' {
			i++
			continue
		}
		m := run(s, i, '`')
		if m == n {
			return i
		}
		i += m
	}
	return -1
}

func splitLinesKeepEnds(s string) []string {
	var out []string
	for len(s) > 0 {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			out = append(out, s)
			break
		}
		out = append(out, s[:i+1])
		s = s[i+1:]
	}
	return out
}

// IsProtected reports whether pos falls inside one of spans (sorted).
func IsProtected(spans []Span, pos int) bool {
	lo, hi := 0, len(spans)
	for lo < hi {
		m := (lo + hi) / 2
		switch {
		case pos < spans[m].Start:
			hi = m
		case pos >= spans[m].End:
			lo = m + 1
		default:
			return true
		}
	}
	return false
}

// ReplaceFunc replaces every match of re in src that does not start in a
// protected span. fn receives the submatch strings and returns the
// replacement; returning ok=false keeps the original text.
func ReplaceFunc(src string, re *regexp.Regexp, fn func(m []string) (string, bool)) string {
	return ReplaceFuncAt(src, re, func(_ int, m []string) (string, bool) { return fn(m) })
}

// InTableRow reports whether the line containing pos looks like a GFM table row.
func InTableRow(src string, pos int) bool {
	start := strings.LastIndexByte(src[:pos], '\n') + 1
	return strings.HasPrefix(strings.TrimLeft(src[start:pos], " \t"), "|")
}

// ReplaceFuncAt is ReplaceFunc that also passes the match start offset.
func ReplaceFuncAt(src string, re *regexp.Regexp, fn func(start int, m []string) (string, bool)) string {
	spans := Protected(src)
	var b strings.Builder
	last := 0
	for _, idx := range re.FindAllStringSubmatchIndex(src, -1) {
		if IsProtected(spans, idx[0]) {
			continue
		}
		m := make([]string, len(idx)/2)
		for g := range m {
			if idx[2*g] >= 0 {
				m[g] = src[idx[2*g]:idx[2*g+1]]
			}
		}
		rep, ok := fn(idx[0], m)
		if !ok {
			continue
		}
		b.WriteString(src[last:idx[0]])
		b.WriteString(rep)
		last = idx[1]
	}
	if last == 0 {
		return src
	}
	b.WriteString(src[last:])
	return b.String()
}

// Common patterns.
var (
	// MarkdownLink matches [text](url "title") and ![alt](url "title").
	// Groups: 1 "!" or "", 2 text, 3 url (angle brackets kept), 4 title (without quotes).
	MarkdownLink = regexp.MustCompile(`(!?)\[((?:[^\[\]\n]|\[[^\[\]\n]*\])*)\]\(\s*(<[^>\n]*>|[^()\s]+)(?:\s+"([^"\n]*)")?\s*\)`)
	// WikiLink matches [[target]] and ![[target]]. Groups: 1 "!" or "", 2 inner text.
	WikiLink = regexp.MustCompile(`(!?)\[\[([^\[\]\n]+?)\]\]`)
	// HTMLLink matches an href or src attribute holding a canonical link, as
	// in the HTML tables of canonical Markdown, with the space before it.
	// Groups: 1 attribute, 2 URL.
	HTMLLink = regexp.MustCompile(`\s(href|src)="(tolarix://[^"]*)"`)
)

// ReplaceHTMLLinks rewrites the canonical links in href and src attributes.
// fn gets the attribute name and the unescaped URL and returns the new
// destination; ok=false drops the attribute (the link shows as text, the
// image as its description).
func ReplaceHTMLLinks(src string, fn func(attr, u string) (dest string, ok bool)) string {
	return ReplaceFunc(src, HTMLLink, func(m []string) (string, bool) {
		dest, ok := fn(m[1], html.UnescapeString(m[2]))
		if !ok {
			return "", true
		}
		return " " + m[1] + `="` + html.EscapeString(dest) + `"`, true
	})
}

// SplitFrontMatter separates a leading YAML front matter block ("---" lines).
// It returns the YAML (without delimiters) and the remaining body.
func SplitFrontMatter(src string) (yaml string, body string, ok bool) {
	s := strings.TrimPrefix(src, string(rune(0xFEFF))) // UTF-8 byte order mark
	if !strings.HasPrefix(s, "---\n") && !strings.HasPrefix(s, "---\r\n") {
		return "", src, false
	}
	rest := s[strings.IndexByte(s, '\n')+1:]
	pos := 0
	for _, ln := range splitLinesKeepEnds(rest) {
		if t := strings.TrimRight(ln, "\r\n"); t == "---" || t == "..." {
			return rest[:pos], rest[pos+len(ln):], true
		}
		pos += len(ln)
	}
	return "", src, false
}
