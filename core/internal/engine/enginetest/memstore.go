// Package enginetest holds helpers for tests that drive the real engine
// (connector end-to-end tests).
package enginetest

import (
	"context"
	"sort"
	"sync"

	"github.com/davidemaggi/tolarix/core/internal/engine"
)

// MemStore is an in-memory engine.MappingStore.
type MemStore struct {
	mu   sync.Mutex
	maps map[string]map[string]engine.Mapping
	atts map[string]map[string]string
}

var _ engine.MappingStore = (*MemStore)(nil)

// NewMemStore returns an empty store.
func NewMemStore() *MemStore {
	return &MemStore{maps: map[string]map[string]engine.Mapping{}, atts: map[string]map[string]string{}}
}

func (m *MemStore) LoadMappings(_ context.Context, tid string) ([]engine.Mapping, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]engine.Mapping, 0, len(m.maps[tid]))
	for _, v := range m.maps[tid] {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SourceID < out[j].SourceID })
	return out, nil
}

func (m *MemStore) UpsertMapping(_ context.Context, tid string, x engine.Mapping) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.maps[tid] == nil {
		m.maps[tid] = map[string]engine.Mapping{}
	}
	m.maps[tid][x.SourceID] = x
	return nil
}

func (m *MemStore) DeleteMapping(_ context.Context, tid, sid string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.maps[tid], sid)
	return nil
}

func (m *MemStore) LoadAttachmentMappings(_ context.Context, tid string) (map[string]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]string{}
	for k, v := range m.atts[tid] {
		out[k] = v
	}
	return out, nil
}

func (m *MemStore) UpsertAttachmentMapping(_ context.Context, tid, h, k string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.atts[tid] == nil {
		m.atts[tid] = map[string]string{}
	}
	m.atts[tid][h] = k
	return nil
}

func (m *MemStore) DeleteAttachmentMapping(_ context.Context, tid, h string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.atts[tid], h)
	return nil
}

// Len counts the page and attachment mappings of every target.
func (m *MemStore) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, x := range m.maps {
		n += len(x)
	}
	for _, x := range m.atts {
		n += len(x)
	}
	return n
}
