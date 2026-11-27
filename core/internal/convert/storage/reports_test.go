package storage

import (
	"strings"
	"testing"

	"github.com/davidemaggi/tolarix/core/internal/model"
)

// labelEnv adds three labelled pages to fakeEnv: two decisions with Page
// Properties, one how-to without.
type labelEnv struct{ fakeEnv }

var labelPages = map[string]struct {
	ref    PageRef
	labels []string
	props  []Property
}{
	"301": {PageRef{"301", "Use Postgres", "2024-01-01", "2024-03-01"}, []string{"decision"},
		[]Property{{"Status", "Done"}, {"Owner", "Ada | Bob"}}},
	"302": {PageRef{"302", "Adopt Go", "2024-02-01", "2024-02-15"}, []string{"decision", "backend"},
		[]Property{{"Status", "In progress"}, {"Stakeholders", []string{"Ada", "Bob"}}}},
	"303": {PageRef{"303", "Deploy", "2024-01-10", "2024-04-01"}, []string{"howto", "backend"}, nil},
}

func (e *labelEnv) PagesWithLabels(labels []string, all bool) []PageRef {
	var out []PageRef
	for _, id := range []string{"301", "302", "303"} {
		p := labelPages[id]
		hits := 0
		for _, l := range labels {
			for _, have := range p.labels {
				if l == have {
					hits++
				}
			}
		}
		if hits > 0 && (!all || hits == len(labels)) {
			out = append(out, p.ref)
		}
	}
	return out
}

func (e *labelEnv) PagePropertiesOf(id string) []Property { return labelPages[id].props }

func TestPagePropertiesReport(t *testing.T) {
	src := `<ac:structured-macro ac:name="detailssummary"><ac:parameter ac:name="cql">label = "decision" and space = currentSpace()</ac:parameter><ac:parameter ac:name="firstcolumn">Decision</ac:parameter></ac:structured-macro>`
	env := &labelEnv{}
	md, err := ToMarkdown(src, env)
	if err != nil {
		t.Fatal(err)
	}
	want := `| Decision | Status | Owner | Stakeholders |
| --- | --- | --- | --- |
| [Adopt Go](tolarix://page/302) | In progress |  | Ada, Bob |
| [Use Postgres](tolarix://page/301) | Done | Ada \| Bob |  |`
	if !strings.Contains(md, want) {
		t.Errorf("report:\n%s\nwant table:\n%s", md, want)
	}
	if !strings.HasPrefix(md, model.MacroStart) || !strings.Contains(md, model.MacroEnd) {
		t.Errorf("report is not wrapped in the live macro markers:\n%s", md)
	}
	if len(env.warnings) != 0 {
		t.Errorf("warnings = %v", env.warnings)
	}
	// Back to Confluence: the live macro, not the table.
	xml := FromMarkdown(md, &fakeWriter{})
	if xml != src {
		t.Errorf("FromMarkdown = %s\nwant %s", xml, src)
	}
}

func TestContentByLabel(t *testing.T) {
	for _, tc := range []struct {
		params string
		want   []string
	}{
		{`<ac:parameter ac:name="labels">backend</ac:parameter>`, []string{"Deploy", "Adopt Go"}}, // newest change first
		{`<ac:parameter ac:name="labels">backend,decision</ac:parameter><ac:parameter ac:name="operator">AND</ac:parameter>`, []string{"Adopt Go"}},
		{`<ac:parameter ac:name="cql">label in ("decision","howto")</ac:parameter><ac:parameter ac:name="sort">title</ac:parameter><ac:parameter ac:name="max">2</ac:parameter>`, []string{"Adopt Go", "Deploy"}},
	} {
		src := `<ac:structured-macro ac:name="contentbylabel">` + tc.params + `</ac:structured-macro>`
		md, err := ToMarkdown(src, &labelEnv{})
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, l := range strings.Split(md, "\n") {
			if t, ok := strings.CutPrefix(l, "- ["); ok {
				got = append(got, t[:strings.Index(t, "]")])
			}
		}
		if strings.Join(got, "|") != strings.Join(tc.want, "|") {
			t.Errorf("%s: pages = %v, want %v\n%s", tc.params, got, tc.want, md)
		}
	}
}

func TestLiveOnlyMacrosAreHidden(t *testing.T) {
	src := `<p>Before</p><ac:structured-macro ac:name="livesearch"><ac:parameter ac:name="spaceKey">DOC</ac:parameter></ac:structured-macro><ac:structured-macro ac:name="change-history"/><p>After</p>`
	env := &fakeEnv{}
	md, err := ToMarkdown(src, env)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(md, "confluence-macro") || len(env.warnings) != 0 {
		t.Errorf("live-only macros should be hidden without warnings; got %v\n%s", env.warnings, md)
	}
	if xml := FromMarkdown(md, &fakeWriter{}); xml != src {
		t.Errorf("FromMarkdown = %s\nwant %s", xml, src)
	}
	// Without the label index (other sources), the report is kept live but empty.
	md, _ = ToMarkdown(`<ac:structured-macro ac:name="detailssummary"><ac:parameter ac:name="label">x</ac:parameter></ac:structured-macro>`, &fakeEnv{})
	if !strings.HasPrefix(md, model.MacroStart) {
		t.Errorf("report without index = %q", md)
	}
}
