// Package render turns source attachments into derived attachments that
// mirrors can display (first use: draw.io diagrams -> PNG). See
// docs/dev/renderers.md for the design.
//
// Renderers run in the engine after the Master snapshot and before encoding,
// so every target benefits. A failed render is a warning, never a failed run.
package render

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/davidemaggi/tolarix/core/internal/connectors"
	"github.com/davidemaggi/tolarix/core/internal/model"
)

// Input is what a renderer receives for one matching attachment.
type Input struct {
	Page     *model.Page
	Source   *model.Attachment
	Open     func() (io.ReadCloser, error)
	Siblings []*model.Attachment // other attachments of the same page (e.g. an existing preview)
	OpenAny  func(a *model.Attachment) (io.ReadCloser, error)
}

// Output is one derived file.
type Output struct {
	Name      string
	MediaType string
	Content   []byte
}

// Renderer produces derived attachments.
type Renderer interface {
	Name() string
	Match(a *model.Attachment) bool
	Render(ctx context.Context, in Input) ([]Output, error)
}

// Pipeline runs renderers over a tree and keeps the derived bytes in memory
// until they are uploaded.
type Pipeline struct {
	Renderers []Renderer
	blobs     map[string][]byte
}

// Blob returns derived content by hash, if this pipeline produced it.
func (p *Pipeline) Blob(hash string) ([]byte, bool) {
	if p == nil {
		return nil, false
	}
	b, ok := p.blobs[hash]
	return b, ok
}

// Apply renders every matching attachment referenced by a page. For each
// output it registers a RoleRendered attachment and rewrites the page body:
// a link or image pointing at the source becomes an image of the rendered
// file wrapped in a link to the source, so the diagram stays editable:
//
//	![name](tolarix://att/<source>)  ->  [![name](tolarix://att/<png>)](tolarix://att/<source>)
func (p *Pipeline) Apply(ctx context.Context, tree *model.Tree,
	open func(a *model.Attachment) (io.ReadCloser, error), w connectors.Warner) {
	if p == nil || len(p.Renderers) == 0 {
		return
	}
	if p.blobs == nil {
		p.blobs = map[string][]byte{}
	}
	rendered := map[string]string{} // source hash -> derived hash (cache within the run)
	tree.Walk(func(pg *model.Page, _ int) {
		siblings := make([]*model.Attachment, 0, len(pg.Attachments))
		for _, h := range pg.Attachments {
			if a := tree.Attachments[h]; a != nil {
				siblings = append(siblings, a)
			}
		}
		for _, src := range siblings {
			r := p.match(src)
			if r == nil {
				continue
			}
			derived, done := rendered[src.Hash]
			if !done {
				outs, err := r.Render(ctx, Input{
					Page: pg, Source: src, Siblings: siblings, OpenAny: open,
					Open: func() (io.ReadCloser, error) { return open(src) },
				})
				if err != nil || len(outs) == 0 {
					msg := "produced no output"
					if err != nil {
						msg = err.Error()
					}
					w.Warn(connectors.Warning{PageID: pg.ID, Code: "render-failed",
						Message: fmt.Sprintf("%s could not render %q (%s); the mirror gets the source file only", r.Name(), src.Name, msg)})
					rendered[src.Hash] = ""
					continue
				}
				out := outs[0]
				derived = model.HashBytes(out.Content)
				p.blobs[derived] = out.Content
				tree.Attachments[derived] = &model.Attachment{
					Hash: derived, Name: out.Name, Size: int64(len(out.Content)), MediaType: out.MediaType,
					Role: model.RoleRendered, DerivedFrom: src.Hash,
				}
				src.Role = model.RoleDiagramSource
				rendered[src.Hash] = derived
			}
			if derived == "" {
				continue
			}
			pg.Body = wrapRendered(pg.Body, src.Hash, derived, tree.Attachments[derived].Name)
			pg.Attachments = appendUnique(pg.Attachments, derived)
		}
	})
}

func (p *Pipeline) match(a *model.Attachment) Renderer {
	if a.Role == model.RoleRendered {
		return nil
	}
	for _, r := range p.Renderers {
		if r.Match(a) {
			return r
		}
	}
	return nil
}

func appendUnique(list []string, h string) []string {
	for _, x := range list {
		if x == h {
			return list
		}
	}
	return append(list, h)
}

// NewBlobReader is a small helper for renderers' tests.
func NewBlobReader(b []byte) io.ReadCloser { return io.NopCloser(bytes.NewReader(b)) }
