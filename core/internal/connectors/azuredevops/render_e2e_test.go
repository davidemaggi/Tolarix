package azuredevops

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/davidemaggi/tolarix/core/internal/connectors/obsidian"
	"github.com/davidemaggi/tolarix/core/internal/engine"
	"github.com/davidemaggi/tolarix/core/internal/model"
	"github.com/davidemaggi/tolarix/core/internal/render"
)

type fakeRenderer struct{}

func (fakeRenderer) Name() string { return "fake" }
func (fakeRenderer) Match(a *model.Attachment) bool {
	return strings.HasSuffix(a.Name, ".drawio")
}
func (fakeRenderer) Render(context.Context, render.Input) ([]render.Output, error) {
	return []render.Output{{Name: "arch.png", MediaType: "image/png", Content: []byte("\x89PNG\r\n\x1a\nrendered")}}, nil
}

func TestRenderedDiagramIsPushed(t *testing.T) {
	f := newFake(t)
	seedWiki(f)
	vault := t.TempDir()
	writeFile(t, vault, "Design.md", "# Design\n\n![[arch.drawio]]\n")
	writeFile(t, vault, "arch.drawio", "<mxfile/>")
	src, err := obsidian.Open(vault)
	if err != nil {
		t.Fatal(err)
	}
	repo := connect(t, f, "/_wiki/wikis/Docs.wiki")
	s, err := repo.Open(context.Background(), json.RawMessage(`{"folder":"Mirror"}`))
	if err != nil {
		t.Fatal(err)
	}
	e := &engine.Engine{Mappings: newHarness(t, f).store, Renderers: []render.Renderer{fakeRenderer{}}}
	rep, err := e.Run(context.Background(), engine.Request{Source: src, SourceName: "Vault",
		Targets: []engine.Target{{ID: "t1", Name: "ADO", Session: s}}})
	if err != nil {
		t.Fatal(err)
	}
	if tr := rep.Targets[0]; tr.Error != "" {
		t.Fatal(tr.Error)
	}
	if f.file("main", "/wiki/Mirror/.attachments/arch.png") == "" {
		t.Fatal("rendered PNG not pushed")
	}
	// The image inside the link to the diagram is rewritten too (it used to
	// keep its tolarix:// address and show as a broken image).
	if got, want := f.file("main", "/wiki/Mirror/Design.md"), "[![arch.drawio](./.attachments/arch.png)](./.attachments/arch.drawio)"; !strings.Contains(got, want) {
		t.Fatalf("page = %q, want %q", got, want)
	}
}
