package confluence

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/davidemaggi/tolarix/core/internal/connectors"
	"github.com/davidemaggi/tolarix/core/internal/connectors/obsidian"
	"github.com/davidemaggi/tolarix/core/internal/engine"
	"github.com/davidemaggi/tolarix/core/internal/engine/enginetest"
	"github.com/davidemaggi/tolarix/core/internal/model"
	"github.com/davidemaggi/tolarix/core/internal/secrets"
)

func connectCloud(t *testing.T, f *fakeConfluence, path string) *Confluence {
	t.Helper()
	c, err := NewCloud(Config{URL: f.url(path), Email: f.email}, secrets.New(f.token))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCloudRegistrationAndNew(t *testing.T) {
	spec, ok := connectors.Lookup(CloudKind)
	if !ok || !spec.CanRead || !spec.CanWrite {
		t.Fatalf("spec = %+v, %v", spec, ok)
	}
	var names []string
	for _, f := range spec.Fields {
		names = append(names, f.Name)
	}
	if strings.Join(names, ",") != "url,email,token" {
		t.Fatalf("fields = %v", names)
	}

	for in, want := range map[string]Address{
		"https://acme.atlassian.net/wiki/spaces/DOC/overview":             {BaseURL: "https://acme.atlassian.net/wiki", SpaceKey: "DOC"},
		"https://acme.atlassian.net/wiki/spaces/DOC/pages/123/Some+Title": {BaseURL: "https://acme.atlassian.net/wiki", SpaceKey: "DOC", PageID: "123"},
		"https://acme.atlassian.net/spaces/DOC":                           {BaseURL: "https://acme.atlassian.net/wiki", SpaceKey: "DOC"},
	} {
		c, err := NewCloud(Config{URL: in, Email: "me@example.com"}, secrets.New("t"))
		if err != nil || c.addr != want || !c.cloud {
			t.Errorf("NewCloud(%q) = %+v, %v; want %+v", in, c.addr, err, want)
		}
	}
	if _, err := NewCloud(Config{URL: "https://acme.atlassian.net/wiki/spaces/DOC"}, secrets.New("t")); err == nil || !strings.Contains(err.Error(), "email") {
		t.Fatalf("missing email: %v", err)
	}
	if _, err := NewCloud(Config{URL: "https://acme.atlassian.net/wiki/spaces/DOC", Email: "a@b"}, secrets.String{}); err == nil || !strings.Contains(err.Error(), "API token") {
		t.Fatalf("missing token: %v", err)
	}
}

func TestCloudTestGivesFriendlyErrors(t *testing.T) {
	f := newCloudFake(t)
	ctx := context.Background()
	if err := connectCloud(t, f, "/spaces/DOC/overview").Test(ctx); err != nil {
		t.Fatalf("Test: %v", err)
	}
	if err := connectCloud(t, f, "/spaces/NOPE").Test(ctx); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("missing space: %v", err)
	}
	bad, _ := NewCloud(Config{URL: f.url("/spaces/DOC"), Email: f.email}, secrets.New("wrong"))
	if err := bad.Test(ctx); err == nil || !strings.Contains(err.Error(), "email and the API token") {
		t.Fatalf("bad token: %v", err)
	}
	// A page address resolves the space key through the page's space id.
	c := connectCloud(t, f, "/spaces/DOC/pages/"+f.home+"/Home")
	if err := c.Test(ctx); err != nil || c.addr.PageTitle != "Home" {
		t.Fatalf("page address: %+v, %v", c.addr, err)
	}
}

// seedCloud builds a Cloud space with folders, smart links and an ADF panel.
func seedCloud(f *fakeConfluence) (guide, team, people string) {
	guide = f.addPage("Backend Guide", f.home, `<h2>Getting Started</h2><p>Install it.</p>`)
	team = f.addFolder("Team", f.home)
	people = f.addPage("People", team, `<p>Who is who.</p>`)
	nested := f.addFolder("Old Stuff", team)
	f.addPage("Retired", nested, `<p>Gone.</p>`)
	f.addPage("Other Root", "", `<p>Elsewhere</p>`)

	link := f.srv.URL + "/wiki/spaces/DOC/pages/" + guide + "/Backend+Guide#Getting-Started"
	f.mu.Lock()
	f.pages[f.home].Body = `<p>Welcome. Read <a href="` + link + `">` + link + `</a> and ` +
		`<a href="` + f.srv.URL + `/wiki/spaces/DOC/pages/` + people + `">the team</a> or ` +
		`<a href="https://example.com/x">elsewhere</a>.</p>` +
		`<ac:adf-extension><ac:adf-node type="panel"><ac:adf-attribute key="panel-type">note</ac:adf-attribute>` +
		`<ac:adf-content><p>Remember the <ac:image><ri:attachment ri:filename="photo.png"/></ac:image> photo.</p></ac:adf-content></ac:adf-node>` +
		`<ac:adf-fallback><div class="panel"><p>fallback</p></div></ac:adf-fallback></ac:adf-extension>`
	f.mu.Unlock()
	f.addAtt(f.home, "photo.png", []byte("PNG-photo"))
	return guide, team, people
}

func TestCloudSnapshot(t *testing.T) {
	f := newCloudFake(t)
	seedCloud(f)
	c := connectCloud(t, f, "/spaces/DOC/overview")
	var ws connectors.Warnings
	tree, err := c.Snapshot(context.Background(), &ws)
	if err != nil {
		t.Fatal(err)
	}
	out := dumpTree(tree)
	tree.Walk(func(p *model.Page, _ int) {
		if p.Placeholder {
			out += "placeholder " + p.Title + "\n"
		}
	})
	for _, w := range ws.List {
		out += fmt.Sprintf("warning %s %s\n", w.PageID, w.Code)
	}
	golden(t, "snapshot-cloud.txt", out)

	for _, a := range tree.Attachments {
		rc, err := c.OpenAttachment(context.Background(), a.SourceRef)
		if err != nil {
			t.Fatalf("open %s: %v", a.Name, err)
		}
		h, _, _ := model.HashReader(rc)
		_ = rc.Close()
		if h != a.Hash {
			t.Fatalf("%s: hash mismatch", a.Name)
		}
	}
}

func TestCloudSubtreeAndBrowse(t *testing.T) {
	f := newCloudFake(t)
	guide, team, people := seedCloud(f)
	ctx := context.Background()

	c := connectCloud(t, f, "/spaces/DOC/pages/"+f.home)
	tree, err := c.Snapshot(ctx, connectors.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if len(tree.Pages) != 6 || tree.Pages[f.home].ParentID != "" || tree.Pages[people].ParentID != team || !tree.Pages[team].Placeholder {
		t.Fatalf("subtree = %s", dumpTree(tree))
	}

	roots, err := connectCloud(t, f, "/spaces/DOC").Browse(ctx, "")
	if err != nil || len(roots) != 2 || roots[0].Title != "Home" || !roots[0].HasChildren {
		t.Fatalf("roots = %+v, %v", roots, err)
	}
	top, err := c.Browse(ctx, "")
	if err != nil || len(top) != 1 || top[0].ID != f.home || !top[0].HasChildren {
		t.Fatalf("page root = %+v, %v", top, err)
	}
	kids, err := c.Browse(ctx, f.home)
	if err != nil || len(kids) != 2 || kids[0].ID != guide || kids[1].ID != team {
		t.Fatalf("kids = %+v, %v", kids, err)
	}
	inFolder, err := c.Browse(ctx, team)
	if err != nil || len(inFolder) != 2 || inFolder[0].ID != people {
		t.Fatalf("folder = %+v, %v", inFolder, err)
	}
}

func TestCloudMirrorEndToEnd(t *testing.T) {
	f := newCloudFake(t)
	vault := t.TempDir()
	writeFile(t, vault, "Guide.md", "# Guide\n\nSee [[Setup#Install]].\n\n> [!warning] Careful\n> Sharp.\n")
	writeFile(t, vault, "Guide/Setup.md", "## Install\n\n![[shot.png]]\n")
	writeFile(t, vault, "Guide/shot.png", "PNG-shot")
	writeFile(t, vault, "Notes.md", "Some notes.\n")
	src, err := obsidian.Open(vault)
	if err != nil {
		t.Fatal(err)
	}
	// No parent and a space address: mirrors go under the homepage.
	h := &mirrorHarness{t: t, f: f, vault: vault, src: src, conf: connectCloud(t, f, "/spaces/DOC"), store: enginetest.NewMemStore(), loc: "{}"}

	tr := h.run(engine.Options{})
	if got := kinds(tr); got != "create:Guide, create:Notes, create:Setup" {
		t.Fatalf("run 1 ops = %s", got)
	}
	if h.parentTitle("Guide") != "Home" || h.parentTitle("Setup") != "Guide" {
		t.Fatal("wrong hierarchy")
	}
	if g := f.byTitle("Guide").Body; !strings.Contains(g, `<ac:link ac:anchor="Install"><ri:page ri:content-title="Setup"/>`) || !strings.Contains(g, `ac:name="note"`) {
		t.Errorf("Guide body:\n%s", g)
	}
	if setup := f.byTitle("Setup"); f.attsOf(setup.ID)["shot.png"] != "PNG-shot" {
		t.Errorf("attachment not uploaded: %v", f.attsOf(setup.ID))
	}

	f.writes()
	tr = h.run(engine.Options{})
	if got := kinds(tr); strings.Contains(got, "create") || strings.Contains(got, "update") {
		t.Fatalf("run 2 ops = %s", got)
	}
	if w := f.writes(); len(w) != 0 {
		t.Fatalf("run 2 wrote %v", w)
	}

	// Change the image and a body: update with version+1, attachment replaced.
	writeFile(t, vault, "Guide/shot.png", "PNG-shot-2")
	writeFile(t, vault, "Notes.md", "Better notes.\n")
	h.run(engine.Options{})
	if !strings.Contains(f.byTitle("Notes").Body, "Better") || f.byTitle("Notes").Version != 2 {
		t.Fatalf("Notes = %+v", f.byTitle("Notes"))
	}
	if got := f.attsOf(f.byTitle("Setup").ID)["shot.png"]; got != "PNG-shot-2" {
		t.Fatalf("attachment = %q", got)
	}

	// Remove Notes: archived under "Archived – Home", below the homepage.
	if err := os.Remove(filepath.Join(vault, "Notes.md")); err != nil {
		t.Fatal(err)
	}
	h.run(engine.Options{})
	if f.byTitle("Notes (archived)") == nil || h.parentTitle("Notes (archived)") != "Archived – Home" || h.parentTitle("Archived – Home") != "Home" {
		t.Fatal("not archived where expected")
	}

	// Remove the image with the delete policy: the attachment is deleted
	// through v2.
	writeFile(t, vault, "Guide/Setup.md", "## Install\n\nNo picture.\n")
	if err := os.Remove(filepath.Join(vault, "Guide", "shot.png")); err != nil {
		t.Fatal(err)
	}
	h.run(engine.Options{DeletePolicy: engine.DeleteOnMirror})
	if got := f.attsOf(f.byTitle("Setup").ID); len(got) != 0 {
		t.Fatalf("attachments left: %v", got)
	}
}
