package storage

import (
	"bytes"
	"compress/flate"
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files")

const (
	hashPNG  = "aaaa000000000000000000000000000000000000000000000000000000000001"
	hashPDF  = "bbbb000000000000000000000000000000000000000000000000000000000002"
	hashLogo = "cccc000000000000000000000000000000000000000000000000000000000003"
	hashArch = "dddd000000000000000000000000000000000000000000000000000000000004"
)

// fakeEnv is a space with two pages: Home (100, the page being converted)
// and Backend Guide (200).
type fakeEnv struct{ warnings []string }

func (e *fakeEnv) SelfID() string { return "100" }

func (e *fakeEnv) PageID(space, title string) (string, bool) {
	if space != "" && space != "DOC" {
		return "", false
	}
	switch title {
	case "Home":
		return "100", true
	case "Backend Guide":
		return "200", true
	}
	return "", false
}

func (e *fakeEnv) Attachment(owner, filename string) (string, bool) {
	switch owner + "/" + filename {
	case "/diagram.png", "Home/diagram.png":
		return hashPNG, true
	case "/spec.pdf":
		return hashPDF, true
	case "Backend Guide/logo.png":
		return hashLogo, true
	}
	return "", false
}

func (e *fakeEnv) Diagram(owner, name string) (string, bool) {
	if (owner == "" || owner == "100") && name == "arch" {
		return hashArch, true
	}
	return "", false
}

func (e *fakeEnv) Heading(pageID, anchor string) string {
	headings := map[string][]string{"100": {"Intro", "Next Steps"}, "200": {"Getting Started"}}
	if h, ok := HeadingForAnchor(headings[pageID], anchor); ok {
		return h
	}
	return anchor
}

func (e *fakeEnv) Warn(code, _ string) { e.warnings = append(e.warnings, code) }

// PageByURL knows the Cloud address of Backend Guide.
func (e *fakeEnv) PageByURL(href string) (string, string, string, bool) {
	page, anchor, _ := strings.Cut(href, "#")
	if strings.HasPrefix(page, "https://example.atlassian.net/wiki/spaces/DOC/pages/200") {
		return "200", "Backend Guide", anchor, true
	}
	return "", "", "", false
}

// UserName knows one Cloud account.
func (e *fakeEnv) UserName(accountID, _, _ string) (string, bool) {
	if accountID == "557058:abc" {
		return "Ada Lovelace", true
	}
	return "", false
}

// fakeWriter is the same space seen from the writing side.
type fakeWriter struct{ warnings []string }

func (w *fakeWriter) SelfID() string { return "100" }

func (w *fakeWriter) PageTitle(id string) (string, bool) {
	switch id {
	case "100":
		return "Home", true
	case "200":
		return "Backend Guide", true
	}
	return "", false
}

func (w *fakeWriter) Attachment(hash string) (string, string, bool) {
	switch hash {
	case hashPNG:
		return "diagram.png", "", true
	case hashPDF:
		return "spec.pdf", "", true
	case hashLogo:
		return "logo.png", "Backend Guide", true
	case hashArch:
		return "arch.png", "", true
	}
	return "", "", false
}

func (w *fakeWriter) Warn(code, _ string) { w.warnings = append(w.warnings, code) }

func golden(t *testing.T, path, got string) {
	t.Helper()
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test -update)", err)
	}
	if string(want) != got {
		t.Errorf("%s differs:\n--- got ---\n%s\n--- want ---\n%s", path, got, want)
	}
}

func warnLine(ws []string) string {
	sort.Strings(ws)
	return "<!-- warnings: " + strings.Join(ws, " ") + " -->\n"
}

func TestToMarkdownGolden(t *testing.T) {
	files, _ := filepath.Glob("testdata/tomd/*.xml")
	if len(files) == 0 {
		t.Fatal("no fixtures")
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			env := &fakeEnv{}
			md, err := ToMarkdown(string(src), env)
			if err != nil {
				t.Fatal(err)
			}
			golden(t, strings.TrimSuffix(f, ".xml")+".md", md+warnLine(env.warnings))
		})
	}
}

func TestFromMarkdownGolden(t *testing.T) {
	files, _ := filepath.Glob("testdata/frommd/*.md")
	if len(files) == 0 {
		t.Fatal("no fixtures")
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			w := &fakeWriter{}
			xml := FromMarkdown(string(src), w)
			if _, err := parse(xml); err != nil {
				t.Fatalf("output is not well formed: %v", err)
			}
			golden(t, strings.TrimSuffix(f, ".md")+".xml", indentXML(xml)+"\n"+warnLine(w.warnings))
		})
	}
}

// TestRoundTripIsStable checks that storage -> Markdown -> storage ->
// Markdown gives the same Markdown both times, so a Confluence mirror of a
// Confluence master does not change on every run.
func TestRoundTripIsStable(t *testing.T) {
	files, _ := filepath.Glob("testdata/tomd/*.xml")
	files2, _ := filepath.Glob("testdata/frommd/*.md")
	for _, f := range append(files, files2...) {
		t.Run(filepath.Base(f), func(t *testing.T) {
			src, _ := os.ReadFile(f)
			md1 := string(src)
			if strings.HasSuffix(f, ".xml") {
				var err error
				if md1, err = ToMarkdown(md1, &fakeEnv{}); err != nil {
					t.Fatal(err)
				}
			}
			xml1 := FromMarkdown(md1, &fakeWriter{})
			md2, err := ToMarkdown(xml1, &fakeEnv{})
			if err != nil {
				t.Fatal(err)
			}
			xml2 := FromMarkdown(md2, &fakeWriter{})
			md3, _ := ToMarkdown(xml2, &fakeEnv{})
			if md2 != md3 {
				t.Errorf("not stable:\n--- md2 ---\n%s\n--- md3 ---\n%s", md2, md3)
			}
			if xml1 != xml2 && strings.HasSuffix(f, ".md") && md1 == md2 {
				t.Errorf("storage not stable:\n%s\n%s", xml1, xml2)
			}
		})
	}
}

// indentXML puts each block-level element on its own line so golden files
// are readable.
func indentXML(s string) string {
	for _, tag := range []string{"<p>", "<h1>", "<h2>", "<h3>", "<ul>", "<ol>", "<li>", "<table>", "<tr>", "<hr/>", "<blockquote>", "<ac:structured-macro", "<ac:task-list>", "<ac:task>", "<ac:rich-text-body>"} {
		s = strings.ReplaceAll(s, tag, "\n"+tag)
	}
	return strings.TrimPrefix(s, "\n")
}

func TestToMarkdownToleratesSloppyMarkup(t *testing.T) {
	md, err := ToMarkdown(`<p>unclosed <b>bold<br>&nbsp;&hellip;</p></div><p>next`, &fakeEnv{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(md, "**bold") || !strings.Contains(md, "…") || !strings.Contains(md, "next") {
		t.Fatalf("md = %q", md)
	}
}

func TestPageProperties(t *testing.T) {
	src := `<p>intro</p>
<ac:structured-macro ac:name="details"><ac:rich-text-body><table><tbody>
<tr><th><p>Owner</p></th><td><p><ac:link><ri:user ri:account-id="557058:abc"/></ac:link></p></td></tr>
<tr><th>Status</th><td><ac:structured-macro ac:name="status"><ac:parameter ac:name="title">Done</ac:parameter></ac:structured-macro></td></tr>
<tr><th>Reviewers</th><td><ul><li>Ann</li><li>Bob <strong>B.</strong></li></ul></td></tr>
<tr><th>Due</th><td><time datetime="2026-12-01"/></td></tr>
<tr><th>owner</th><td>duplicate</td></tr>
</tbody></table></ac:rich-text-body></ac:structured-macro>
<ac:structured-macro ac:name="details"><ac:parameter ac:name="hidden">true</ac:parameter><ac:rich-text-body><table><tbody>
<tr><th>Team</th><th>Area</th></tr><tr><td>Ops</td><td>Infra</td></tr>
</tbody></table></ac:rich-text-body></ac:structured-macro>`
	got := PageProperties(src, &fakeEnv{})
	want := []Property{
		{"Owner", "Ada Lovelace"}, {"Status", "Done"}, {"Reviewers", []string{"Ann", "Bob B."}},
		{"Due", "2026-12-01"}, {"Team", "Ops"}, {"Area", "Infra"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}
}

func TestPlantUMLCloudTooLarge(t *testing.T) {
	var buf bytes.Buffer
	fw, _ := flate.NewWriter(&buf, flate.BestCompression)
	_, _ = fw.Write(bytes.Repeat([]byte("a"), maxPlantUML+10))
	_ = fw.Close()
	src := `<ac:structured-macro ac:name="plantumlcloud"><ac:parameter ac:name="compressed">true</ac:parameter><ac:parameter ac:name="data">` +
		base64.StdEncoding.EncodeToString(buf.Bytes()) + `</ac:parameter></ac:structured-macro>`
	env := &fakeEnv{}
	md, err := ToMarkdown(src, env)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(md, "```confluence-macro") || len(env.warnings) != 1 || env.warnings[0] != "macro-unsupported" {
		t.Fatalf("md = %.80q, warnings = %v", md, env.warnings)
	}
}
