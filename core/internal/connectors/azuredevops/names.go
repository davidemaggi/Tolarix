package azuredevops

import (
	"fmt"
	"net/url"
	"path"
	"sort"
	"strings"
	"unicode"
)

// Page file names follow the Azure DevOps wiki convention: a space in the
// title is "-" in the file name and characters with a meaning are
// percent-encoded ("-" itself is %2D). Encoding "%" too keeps it reversible.
var nameEncoder = strings.NewReplacer(
	"%", "%25", "-", "%2D", ":", "%3A", "<", "%3C", ">", "%3E", "*", "%2A", "?", "%3F",
	"|", "%7C", `"`, "%22", "/", "%2F", `\`, "%5C", "#", "%23",
)

// EncodeName turns a page title into its file name (without .md).
func EncodeName(title string) string {
	title = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cs, r) {
			return -1
		}
		return r
	}, strings.TrimSpace(title))
	if title == "" {
		title = "Untitled"
	}
	s := strings.ReplaceAll(nameEncoder.Replace(title), " ", "-")
	// No "." at the start or the end of a name.
	if strings.HasPrefix(s, ".") {
		s = "%2E" + s[1:]
	}
	if strings.HasSuffix(s, ".") {
		s = s[:len(s)-1] + "%2E"
	}
	return s
}

// DecodeName turns a file name (without .md) back into a page title.
func DecodeName(name string) string {
	s := strings.ReplaceAll(name, "-", " ")
	if v, err := url.PathUnescape(s); err == nil {
		return v
	}
	return s
}

// linkPath escapes a wiki path for a Markdown link destination. Names are
// already percent-encoded; only what would break the link is escaped.
func linkPath(p string) string {
	return strings.NewReplacer(" ", "%20", "(", "%28", ")", "%29", "<", "%3C", ">", "%3E").Replace(p)
}

// slug is the anchor Azure DevOps gives a heading: lower case, spaces as
// "-", other punctuation percent-encoded ("Install & Run" → install-%26-run).
func slug(heading string) string {
	return url.QueryEscape(anchorText(heading))
}

// anchorText is a heading as its decoded anchor: lower case, spaces as "-".
func anchorText(heading string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(heading)), " ", "-")
}

// githubSlug is the other common anchor style (punctuation dropped), which
// pages written for other tools often use.
func githubSlug(heading string) string {
	var b strings.Builder
	for _, r := range anchorText(heading) {
		if r == '-' || r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// parseOrder reads a .order file: one name per line.
func parseOrder(content string) []string {
	var out []string
	for _, l := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// sortByOrder sorts names by their position in a .order file; names it does
// not list follow in alphabetical order, as in the wiki.
func sortByOrder(names []string, order []string) {
	rank := map[string]int{}
	for i, n := range order {
		if _, dup := rank[n]; !dup {
			rank[n] = i
		}
	}
	sort.SliceStable(names, func(i, j int) bool {
		ri, iok := rank[names[i]]
		rj, jok := rank[names[j]]
		switch {
		case iok && jok:
			return ri < rj
		case iok != jok:
			return iok
		}
		li, lj := strings.ToLower(names[i]), strings.ToLower(names[j])
		if li != lj {
			return li < lj
		}
		return names[i] < names[j]
	})
}

func joinRel(dir, name string) string {
	if dir == "" {
		return name
	}
	if name == "" {
		return dir
	}
	return dir + "/" + name
}

func parentDir(rel string) string {
	if i := strings.LastIndexByte(rel, '/'); i >= 0 {
		return rel[:i]
	}
	return ""
}

func trimMD(p string) string {
	if strings.EqualFold(path.Ext(p), ".md") {
		return p[:len(p)-3]
	}
	return p
}

func isMD(p string) bool { return strings.EqualFold(path.Ext(p), ".md") }

// hidden reports whether any segment of rel starts with "." (.attachments,
// .order, .git…).
func hidden(rel string) bool {
	for _, s := range strings.Split(rel, "/") {
		if strings.HasPrefix(s, ".") {
			return true
		}
	}
	return false
}

func uniqueName(base string, used map[string]bool) string {
	name := base
	for i := 2; used[strings.ToLower(name)]; i++ {
		name = fmt.Sprintf("%s-(%d)", base, i)
	}
	used[strings.ToLower(name)] = true
	return name
}

var attachmentNameReplacer = strings.NewReplacer(
	" ", "-", "/", "-", `\`, "-", "#", "-", "?", "-", "%", "-", ":", "-", "*", "-",
	"<", "-", ">", "-", "|", "-", `"`, "-", "(", "-", ")", "-", "[", "-", "]", "-",
)

// attachmentName makes a file name safe for /.attachments links.
func attachmentName(name string) string {
	ext := strings.ToLower(path.Ext(name))
	base := strings.Trim(attachmentNameReplacer.Replace(strings.TrimSuffix(name, path.Ext(name))), ".-")
	if base == "" {
		base = "file"
	}
	return base + ext
}

var mediaTypes = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif",
	".svg": "image/svg+xml", ".webp": "image/webp", ".bmp": "image/bmp",
	".pdf": "application/pdf", ".drawio": "application/vnd.jgraph.mxfile",
	".mp4": "video/mp4", ".zip": "application/zip", ".txt": "text/plain", ".csv": "text/csv",
	".json": "application/json", ".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	".pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
}

func mediaType(name string) string {
	if t, ok := mediaTypes[strings.ToLower(path.Ext(name))]; ok {
		return t
	}
	return "application/octet-stream"
}
