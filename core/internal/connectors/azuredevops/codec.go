package azuredevops

import (
	"regexp"
	"strings"

	"github.com/davidemaggi/tolarix/core/internal/model"
)

// Block-level dialect differences between canonical Markdown and the Azure
// DevOps wiki:
//
//	canonical                 Azure DevOps wiki
//	```mermaid … ```          ::: mermaid … :::
//	<!-- tolarix:toc -->      [[_TOC_]]
//	> [!note] Title           > **Note:** Title   (one way: the wiki has no callouts)
//	![a](x "width=300")       ![a](x =300x)

var (
	reFence      = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})\\s*([^`\\s]*)")
	reMermaidIn  = regexp.MustCompile(`^ {0,3}:::\s*mermaid\s*$`)
	reColonClose = regexp.MustCompile(`^ {0,3}:::\s*$`)
	reCallout    = regexp.MustCompile(`^(\s*(?:>\s?)*?)>\s?\[!([A-Za-z][\w-]*)\][+-]?\s*(.*)$`)
	reSizedImage = regexp.MustCompile(`(!\[(?:[^\[\]\n]|\[[^\[\]\n]*\])*\]\(\s*(?:<[^>\n]*>|[^()\s]+))\s+=(\d*)x(\d*)\s*\)`)
)

// fenceEnd returns the index of the line closing the fence opened at
// lines[i], or len(lines)-1 and false when the fence is never closed.
func fenceEnd(lines []string, i int, marker string) (int, bool) {
	for j := i + 1; j < len(lines); j++ {
		t := strings.TrimSpace(lines[j])
		if strings.HasPrefix(t, marker[:1]) && strings.Trim(t, marker[:1]) == "" && len(t) >= len(marker) {
			return j, true
		}
	}
	return len(lines) - 1, false
}

// encodeBlocks rewrites canonical block syntax for the wiki.
func encodeBlocks(src string) string {
	lines := strings.Split(src, "\n")
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		if m := reFence.FindStringSubmatch(l); m != nil {
			end, closed := fenceEnd(lines, i, m[1])
			if strings.EqualFold(m[2], "mermaid") {
				inner := lines[i+1:]
				if closed {
					inner = lines[i+1 : end]
				}
				out = append(out, "::: mermaid")
				out = append(out, inner...)
				out = append(out, ":::")
			} else {
				out = append(out, lines[i:end+1]...)
			}
			i = end
			continue
		}
		switch {
		case strings.TrimSpace(l) == model.TOCMarker:
			out = append(out, "[[_TOC_]]")
		case reCallout.MatchString(l):
			m := reCallout.FindStringSubmatch(l)
			label := strings.ToUpper(m[2][:1]) + strings.ToLower(m[2][1:])
			if m[3] != "" {
				out = append(out, m[1]+"> **"+label+":** "+m[3])
			} else {
				out = append(out, m[1]+"> **"+label+"**")
			}
		default:
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// decodeBlocks rewrites wiki block syntax to canonical Markdown. warn
// receives unsupported constructs.
func decodeBlocks(src string, warn func(code, msg string)) string {
	lines := strings.Split(src, "\n")
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		if m := reFence.FindStringSubmatch(l); m != nil {
			end, _ := fenceEnd(lines, i, m[1])
			out = append(out, lines[i:end+1]...)
			i = end
			continue
		}
		if reMermaidIn.MatchString(l) {
			out = append(out, "```mermaid")
			j := i + 1
			for ; j < len(lines) && !reColonClose.MatchString(lines[j]); j++ {
				out = append(out, lines[j])
			}
			out = append(out, "```")
			i = j
			continue
		}
		switch strings.TrimSpace(l) {
		case "[[_TOC_]]":
			out = append(out, model.TOCMarker)
		case "[[_TOSP_]]":
			warn("subpages-list", "the list of subpages ([[_TOSP_]]) is not shown in mirrors")
		default:
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// decodeImageSizes turns ![a](x =300x200) into ![a](x "width=300x200").
func decodeImageSizes(src string) string {
	return reSizedImage.ReplaceAllStringFunc(src, func(s string) string {
		m := reSizedImage.FindStringSubmatch(s)
		switch {
		case m[2] == "":
			return m[1] + ")"
		case m[3] == "":
			return m[1] + ` "width=` + m[2] + `")`
		default:
			return m[1] + ` "width=` + m[2] + "x" + m[3] + `")`
		}
	})
}

// headings lists the ATX headings of a Markdown body, outside code fences.
func headings(src string) []string {
	var out []string
	lines := strings.Split(src, "\n")
	for i := 0; i < len(lines); i++ {
		if m := reFence.FindStringSubmatch(lines[i]); m != nil {
			i, _ = fenceEnd(lines, i, m[1])
			continue
		}
		t := strings.TrimLeft(lines[i], " ")
		if len(lines[i])-len(t) > 3 || !strings.HasPrefix(t, "#") {
			continue
		}
		h := strings.TrimLeft(t, "#")
		if n := len(t) - len(h); n > 6 || (h != "" && h[0] != ' ' && h[0] != '\t') {
			continue
		}
		h = strings.TrimSpace(strings.TrimRight(strings.TrimSpace(h), "#"))
		out = append(out, h)
	}
	return out
}

// headingForSlug finds the heading whose (decoded) anchor is s.
func headingForSlug(hs []string, s string) (string, bool) {
	s = strings.ToLower(s)
	for _, h := range hs {
		if anchorText(h) == s {
			return h, true
		}
	}
	for _, h := range hs {
		if githubSlug(h) == s {
			return h, true
		}
	}
	return "", false
}
