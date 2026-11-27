package azuredevops

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/davidemaggi/tolarix/core/internal/connectors"
	"github.com/davidemaggi/tolarix/core/internal/model"
)

func TestLinksToThisWikiByAddress(t *testing.T) {
	f := newFake(t)
	addWiki(f)
	f.pagePaths = map[string]string{"42": "/Guide/Getting Started", "7": "/Gone"}
	base := f.url("")
	project := "/" + fakeOrg + "/" + url.PathEscape(fakeProject)
	links := []string{
		base + "/_wiki/wikis/Docs.wiki/42/Getting-Started",                    // by page id
		base + "/_wiki/wikis/Docs.wiki/42/Getting-Started#install",            // with an anchor
		base + "/_wiki/wikis/Docs.wiki?pagePath=%2FGuide%2FGetting%20Started", // by page path
		project + "/_wiki/wikis/Docs.wiki/99/FAQ",                             // server-relative, unknown id: by title
		base + "/_git/" + fakeRepo + "?path=/wiki/Guide/FAQ.md",               // the repository file
		base + "/_git/" + fakeRepo + "?path=/wiki/.attachments/a.png",         // an attachment
		base + "/_wiki/wikis/Docs.wiki/7/Gone",                                // this wiki, missing page
		base + "/_wiki/wikis/Other.wiki/42/Getting-Started",                   // another wiki
		base + "/_git/" + fakeRepo + "?path=/README.md",                       // outside the wiki folder
		base + "/_git/" + fakeRepo + "?path=/wiki/Guide/FAQ.md&version=GBdev", // another branch
		"https://dev.azure.com/elsewhere/Docs/_wiki/wikis/Docs.wiki/42/X",     // another organisation
		"https://example.com/page",
	}
	var body strings.Builder
	for i, l := range links {
		body.WriteString("[l" + string(rune('a'+i)) + "](" + l + ")\n")
	}
	f.commitFiles("main", map[string]string{
		"/wiki/Home.md":                  body.String(),
		"/wiki/Guide.md":                 "Guide\n",
		"/wiki/Guide/Getting-Started.md": "## Install\n",
		"/wiki/Guide/FAQ.md":             "FAQ\n",
		"/wiki/.attachments/a.png":       "PNG",
		"/README.md":                     "outside",
	})
	r := connect(t, f, "/_git/"+fakeRepo+"?path=/wiki")
	var ws connectors.Warnings
	tree, err := r.Snapshot(context.Background(), &ws)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSpace(tree.Pages["Home"].Body), "\n")
	att := ""
	for h := range tree.Attachments {
		att = h
	}
	want := []string{
		"[la](" + model.PageLink("Guide/Getting-Started", "") + ")",
		"[lb](" + model.PageLink("Guide/Getting-Started", "Install") + ")",
		"[lc](" + model.PageLink("Guide/Getting-Started", "") + ")",
		"[ld](" + model.PageLink("Guide/FAQ", "") + ")",
		"[le](" + model.PageLink("Guide/FAQ", "") + ")",
		"[lf](" + model.AttachmentLink(att) + ")",
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("link %d = %s, want %s", i, got[i], w)
		}
	}
	for i := len(want); i < len(links); i++ {
		if !strings.Contains(got[i], "("+links[i]+")") {
			t.Errorf("link %d = %s, want it unchanged", i, got[i])
		}
	}
	broken := 0
	for _, w := range ws.List {
		if w.Code == "broken-link" {
			broken++
		}
	}
	if broken != 1 {
		t.Errorf("broken-link warnings = %d, want 1 (the missing page): %+v", broken, ws.List)
	}
}

func TestOrgKey(t *testing.T) {
	for _, c := range [][2]string{
		{"https://dev.azure.com/Contoso", "https://contoso.visualstudio.com"},
		{"https://dev.azure.com/contoso", "https://Contoso.visualstudio.com"},
		{"https://ado.local/tfs/Coll", "https://ADO.local/tfs/Coll/"},
	} {
		if orgKey(c[0]) != orgKey(c[1]) {
			t.Errorf("%s and %s differ", c[0], c[1])
		}
	}
	if orgKey("https://dev.azure.com/a") == orgKey("https://dev.azure.com/b") {
		t.Error("different organisations match")
	}
}
