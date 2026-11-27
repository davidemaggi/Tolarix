package render

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/davidemaggi/tolarix/core/internal/connectors"
	"github.com/davidemaggi/tolarix/core/internal/model"
)

type fakeRenderer struct {
	calls int
	fail  bool
}

func (f *fakeRenderer) Name() string                   { return "fake" }
func (f *fakeRenderer) Match(a *model.Attachment) bool { return strings.HasSuffix(a.Name, ".drawio") }
func (f *fakeRenderer) Render(_ context.Context, in Input) ([]Output, error) {
	f.calls++
	if f.fail {
		return nil, errors.New("no renderer available")
	}
	rc, _ := in.Open()
	b, _ := io.ReadAll(rc)
	return []Output{{Name: strings.TrimSuffix(in.Source.Name, ".drawio") + ".png", MediaType: "image/png", Content: append([]byte("PNG:"), b...)}}, nil
}

func setup() *model.Tree {
	tr := model.NewTree()
	tr.Attachments["src"] = &model.Attachment{Hash: "src", Name: "arch.drawio"}
	tr.Attachments["img"] = &model.Attachment{Hash: "img", Name: "photo.png"}
	tr.Add(&model.Page{ID: "a", Title: "A", Attachments: []string{"src", "img"},
		Body: "![arch.drawio](tolarix://att/src) and [edit](tolarix://att/src) and ![p](tolarix://att/img)\n"})
	tr.Add(&model.Page{ID: "b", Title: "B", Order: 1, Attachments: []string{"src"}, Body: "![](tolarix://att/src)\n"})
	return tr
}

func open(a *model.Attachment) (io.ReadCloser, error) { return NewBlobReader([]byte(a.Name)), nil }

func TestPipelineRendersOncePerSourceAndRewritesBodies(t *testing.T) {
	tr := setup()
	r := &fakeRenderer{}
	p := &Pipeline{Renderers: []Renderer{r}}
	p.Apply(context.Background(), tr, open, connectors.Discard)

	if r.calls != 1 {
		t.Fatalf("rendered %d times, want 1 (cached per run)", r.calls)
	}
	derived := model.HashBytes([]byte("PNG:arch.drawio"))
	if a := tr.Attachments[derived]; a == nil || a.Role != model.RoleRendered || a.DerivedFrom != "src" || a.Name != "arch.png" {
		t.Fatalf("derived attachment: %+v", a)
	}
	if tr.Attachments["src"].Role != model.RoleDiagramSource {
		t.Fatal("source role not set")
	}
	want := "[![arch.drawio](tolarix://att/" + derived + ")](tolarix://att/src) and [![edit](tolarix://att/" + derived + ")](tolarix://att/src) and ![p](tolarix://att/img)\n"
	if got := tr.Pages["a"].Body; got != want {
		t.Fatalf("body:\n%s\nwant:\n%s", got, want)
	}
	if !strings.Contains(tr.Pages["b"].Body, "[![arch.png]") {
		t.Fatalf("empty alt should use the rendered name: %s", tr.Pages["b"].Body)
	}
	if b, ok := p.Blob(derived); !ok || string(b) != "PNG:arch.drawio" {
		t.Fatal("blob not kept")
	}
	if got := tr.Pages["a"].Attachments; len(got) != 3 {
		t.Fatalf("page attachments %v", got)
	}
}

func TestPipelineFailureIsAWarning(t *testing.T) {
	tr := setup()
	var ws connectors.Warnings
	p := &Pipeline{Renderers: []Renderer{&fakeRenderer{fail: true}}}
	before := tr.Pages["a"].Body
	p.Apply(context.Background(), tr, open, &ws)
	if tr.Pages["a"].Body != before {
		t.Fatal("body must stay unchanged when rendering fails")
	}
	if len(ws.List) != 1 || ws.List[0].Code != "render-failed" { // one warning per source, not per page
		t.Fatalf("warnings %+v", ws.List)
	}
}

func TestNilPipelineIsNoop(t *testing.T) {
	var p *Pipeline
	p.Apply(context.Background(), setup(), open, connectors.Discard)
}
