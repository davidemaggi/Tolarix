package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"

	"github.com/davidemaggi/tolarix/core/internal/connectors"
	"github.com/davidemaggi/tolarix/core/internal/model"
)

// fakeSource serves a canonical tree built in the test.
type fakeSource struct {
	tree  *model.Tree
	blobs map[string][]byte // by SourceRef
	err   error
}

func newSource() *fakeSource { return &fakeSource{tree: model.NewTree(), blobs: map[string][]byte{}} }

func (f *fakeSource) page(id, parent, title, body string, order int) *model.Page {
	p := &model.Page{ID: id, ParentID: parent, Name: title, Title: title, Body: body, Order: order}
	f.tree.Add(p)
	return p
}

func (f *fakeSource) attach(p *model.Page, name, content string) string {
	h := model.HashString(content)
	f.tree.Attachments[h] = &model.Attachment{Hash: h, Name: name, Size: int64(len(content)), SourceRef: "ref/" + name}
	f.blobs["ref/"+name] = []byte(content)
	p.Attachments = append(p.Attachments, h)
	p.Body += "\n![" + name + "](" + model.AttachmentLink(h) + ")"
	return h
}

func (f *fakeSource) Test(context.Context) error { return f.err }

func (f *fakeSource) Snapshot(context.Context, connectors.Warner) (*model.Tree, error) {
	if f.err != nil {
		return nil, f.err
	}
	// Deep-ish copy so the engine cannot mutate the test's tree.
	out := model.NewTree()
	for id, p := range f.tree.Pages {
		cp := *p
		cp.Attachments = append([]string(nil), p.Attachments...)
		out.Pages[id] = &cp
	}
	for h, a := range f.tree.Attachments {
		cp := *a
		out.Attachments[h] = &cp
	}
	return out, nil
}

func (f *fakeSource) OpenAttachment(_ context.Context, ref string) (io.ReadCloser, error) {
	b, ok := f.blobs[ref]
	if !ok {
		return nil, errors.New("no such attachment")
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (f *fakeSource) Browse(context.Context, string) ([]connectors.Node, error) { return nil, nil }

// fakeMirror is an in-memory file-like target; state survives sessions.
type fakeMirror struct {
	mu      sync.Mutex
	pages   map[string]string // key -> content ("" for folders)
	atts    map[string][]byte
	writes  int
	failKey string // WritePage fails for this key
}

func newMirror() *fakeMirror {
	return &fakeMirror{pages: map[string]string{}, atts: map[string][]byte{}}
}

func (m *fakeMirror) Test(context.Context) error { return nil }

func (m *fakeMirror) Open(context.Context, json.RawMessage) (connectors.Session, error) {
	return &fakeSession{m: m}, nil
}

func (m *fakeMirror) keys() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.pages))
	for k := range m.pages {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

type fakeSession struct{ m *fakeMirror }

func (s *fakeSession) Layout(tree *model.Tree, _ connectors.Warner) *connectors.Layout {
	l := &connectors.Layout{Pages: map[string]connectors.Placement{}, Attachments: map[string]string{}}
	tree.Walk(func(p *model.Page, _ int) {
		var parts []string
		for _, a := range tree.Ancestors(p) {
			parts = append(parts, a.Title)
		}
		parts = append(parts, p.Title)
		key := strings.Join(parts, "/") + ".md"
		if p.Placeholder {
			key = strings.Join(parts, "/") + "/"
		}
		parent := ""
		if p.ParentID != "" {
			parent = l.Pages[p.ParentID].Key
		}
		l.Pages[p.ID] = connectors.Placement{Key: key, ParentKey: parent, Title: p.Title}
		for _, h := range p.Attachments {
			if a := tree.Attachments[h]; a != nil {
				l.Attachments[h] = "att/" + a.Name
			}
		}
	})
	return l
}

func (s *fakeSession) Encode(_ *model.Tree, p *model.Page, l *connectors.Layout, _ connectors.Warner) ([]byte, error) {
	if p.Placeholder {
		return nil, nil
	}
	body := p.Body
	for h, key := range l.Attachments {
		body = strings.ReplaceAll(body, model.AttachmentLink(h), key)
	}
	return []byte("# " + p.Title + "\n" + body), nil
}

func (s *fakeSession) ContentHash(b []byte) string { return model.HashBytes(b) }

func (s *fakeSession) Inventory(context.Context) (map[string]connectors.MirrorItem, error) {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	out := map[string]connectors.MirrorItem{}
	for k, c := range s.m.pages {
		h := ""
		if !strings.HasSuffix(k, "/") {
			h = model.HashString(c)
		}
		out[k] = connectors.MirrorItem{ID: "id:" + k, Key: k, Hash: h}
	}
	return out, nil
}

func (s *fakeSession) WritePage(_ context.Context, w connectors.PageWrite) (connectors.MirrorItem, error) {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	key := w.Placement.Key
	if key == s.m.failKey {
		return connectors.MirrorItem{}, fmt.Errorf("simulated failure writing %s", key)
	}
	s.m.writes++
	if w.Existing != nil && w.Existing.Key != key {
		delete(s.m.pages, w.Existing.Key)
	}
	s.m.pages[key] = string(w.Content)
	h := ""
	if !w.Placeholder {
		h = model.HashBytes(w.Content)
	}
	return connectors.MirrorItem{ID: "id:" + key, Key: key, Hash: h}, nil
}

func (s *fakeSession) DeletePage(_ context.Context, it connectors.MirrorItem) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	s.m.writes++
	delete(s.m.pages, it.Key)
	return nil
}

func (s *fakeSession) ArchivePage(_ context.Context, it connectors.MirrorItem) (connectors.MirrorItem, error) {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	s.m.writes++
	c := s.m.pages[it.Key]
	delete(s.m.pages, it.Key)
	key := "Archived/" + it.Key
	s.m.pages[key] = c
	return connectors.MirrorItem{ID: "id:" + key, Key: key, Hash: it.Hash}, nil
}

func (s *fakeSession) HasAttachment(_ context.Context, key string) (bool, error) {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	_, ok := s.m.atts[key]
	return ok, nil
}

func (s *fakeSession) PutAttachment(_ context.Context, key string, r io.Reader) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	s.m.writes++
	s.m.atts[key] = b
	return nil
}

func (s *fakeSession) DeleteAttachment(_ context.Context, key string) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	s.m.writes++
	delete(s.m.atts, key)
	return nil
}

func (s *fakeSession) Finish(context.Context, bool) error { return nil }

// memStore is an in-memory MappingStore.
type memStore struct {
	mu   sync.Mutex
	maps map[string]map[string]Mapping
	atts map[string]map[string]string
}

func newStore() *memStore {
	return &memStore{maps: map[string]map[string]Mapping{}, atts: map[string]map[string]string{}}
}

func (s *memStore) LoadMappings(_ context.Context, t string) ([]Mapping, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Mapping
	for _, m := range s.maps[t] {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SourceID < out[j].SourceID })
	return out, nil
}

func (s *memStore) UpsertMapping(_ context.Context, t string, m Mapping) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.maps[t] == nil {
		s.maps[t] = map[string]Mapping{}
	}
	s.maps[t][m.SourceID] = m
	return nil
}

func (s *memStore) DeleteMapping(_ context.Context, t, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.maps[t], id)
	return nil
}

func (s *memStore) LoadAttachmentMappings(_ context.Context, t string) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]string{}
	for k, v := range s.atts[t] {
		out[k] = v
	}
	return out, nil
}

func (s *memStore) UpsertAttachmentMapping(_ context.Context, t, h, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.atts[t] == nil {
		s.atts[t] = map[string]string{}
	}
	s.atts[t][h] = key
	return nil
}

func (s *memStore) DeleteAttachmentMapping(_ context.Context, t, h string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.atts[t], h)
	return nil
}
