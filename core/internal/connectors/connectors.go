// Package connectors defines the contract between the sync engine and each
// wiki format, plus the registry the CLI, API and UI use to discover formats.
//
// Adding a format = one new subpackage that implements Source and/or Target
// and calls Register in init. The engine never imports a connector directly.
package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"sync"

	"github.com/davidemaggi/tolarix/core/internal/model"
	"github.com/davidemaggi/tolarix/core/internal/secrets"
)

// Kind identifies a connector type, e.g. "obsidian".
type Kind string

// Connector is a configured endpoint.
type Connector interface {
	// Test checks reachability and permissions, returning an error a
	// non-technical user can act on.
	Test(ctx context.Context) error
}

// Source reads a Master.
type Source interface {
	Connector
	// Snapshot reads the whole wiki as a canonical tree. Scope filtering is
	// applied by the engine afterwards.
	Snapshot(ctx context.Context, w Warner) (*model.Tree, error)
	// OpenAttachment returns the bytes of an attachment by its SourceRef.
	OpenAttachment(ctx context.Context, sourceRef string) (io.ReadCloser, error)
	// Browse lists the direct children of parentID ("" = roots) for the
	// lazily loaded scope picker.
	Browse(ctx context.Context, parentID string) ([]Node, error)
}

// Node is one entry in the scope picker.
type Node struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	HasChildren bool   `json:"hasChildren"`
}

// Target writes Mirrors.
type Target interface {
	Connector
	// Open starts a write session at a location inside the target (for
	// example a folder in a vault, or a root page in a space). location is the
	// flow target's connector-specific JSON.
	Open(ctx context.Context, location json.RawMessage) (Session, error)
}

// Placement is where a page goes in a target. Key is unique within the
// target session and stable for the same placement (a path for file-based
// targets, a title for Confluence).
type Placement struct {
	Key       string
	ParentKey string
	Title     string
}

// Layout maps every canonical page and attachment to its place in a target.
type Layout struct {
	Pages       map[string]Placement // by page ID
	Attachments map[string]string    // attachment hash -> attachment key
}

// MirrorItem is a page as it exists in a target.
type MirrorItem struct {
	ID   string // the target's own id (equal to Key for file-based targets)
	Key  string
	Hash string // hash of the stored content, comparable with WritePage results
}

// PageWrite asks a session to create, update or move a page.
type PageWrite struct {
	Existing    *MirrorItem // nil to create
	Placement   Placement
	Content     []byte
	Placeholder bool
}

// Session is one target write session. Writes may be buffered until Finish
// (git-based targets push one commit).
type Session interface {
	// Layout places the tree. It must be deterministic for the same input.
	Layout(tree *model.Tree, w Warner) *Layout
	// Encode renders a page in the target format, rewriting canonical links
	// through layout. Unsupported features degrade and are reported to w.
	Encode(tree *model.Tree, p *model.Page, layout *Layout, w Warner) ([]byte, error)
	// ContentHash is the hash WritePage will report for content.
	ContentHash(content []byte) string
	// Inventory lists pages currently in the target location, by Key.
	Inventory(ctx context.Context) (map[string]MirrorItem, error)
	WritePage(ctx context.Context, w PageWrite) (MirrorItem, error)
	DeletePage(ctx context.Context, item MirrorItem) error
	// ArchivePage moves a page to the target's archive area.
	ArchivePage(ctx context.Context, item MirrorItem) (MirrorItem, error)
	HasAttachment(ctx context.Context, key string) (bool, error)
	PutAttachment(ctx context.Context, key string, r io.Reader) error
	DeleteAttachment(ctx context.Context, key string) error
	// Finish flushes buffered writes. commit=false discards them.
	Finish(ctx context.Context, commit bool) error
}

// Warning is a non-fatal issue recorded in the run report.
type Warning struct {
	PageID  string `json:"pageId,omitempty"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Warner collects warnings.
type Warner interface {
	Warn(w Warning)
}

// WarnFunc adapts a function to Warner.
type WarnFunc func(Warning)

func (f WarnFunc) Warn(w Warning) { f(w) }

// Discard ignores warnings.
var Discard Warner = WarnFunc(func(Warning) {})

// Warnings is a simple collecting Warner (safe for concurrent use).
type Warnings struct {
	mu   sync.Mutex
	List []Warning
}

func (ws *Warnings) Warn(w Warning) {
	ws.mu.Lock()
	ws.List = append(ws.List, w)
	ws.mu.Unlock()
}

// Field describes one configuration field for the connection wizard.
type Field struct {
	Name     string `json:"name"`
	Type     string `json:"type"` // string | url | path | secret | bool
	Required bool   `json:"required"`
	Secret   bool   `json:"secret,omitempty"`
}

// Spec registers a connector kind.
type Spec struct {
	Kind        Kind
	DisplayName string
	CanRead     bool
	CanWrite    bool
	Fields      []Field
	// LocationFields describe the flow target location JSON.
	LocationFields []Field
	// New builds a connector from its stored configuration and secret JSON.
	New func(config json.RawMessage, secret secrets.String) (Connector, error)
}

var (
	regMu    sync.RWMutex
	registry = map[Kind]Spec{}
)

// Register adds a connector kind. It panics on duplicates (programming error).
func Register(s Spec) {
	regMu.Lock()
	defer regMu.Unlock()
	if _, dup := registry[s.Kind]; dup {
		panic("connectors: duplicate kind " + string(s.Kind))
	}
	registry[s.Kind] = s
}

// Lookup returns the spec for kind.
func Lookup(kind Kind) (Spec, bool) {
	regMu.RLock()
	defer regMu.RUnlock()
	s, ok := registry[kind]
	return s, ok
}

// All returns every registered spec sorted by kind.
func All() []Spec {
	regMu.RLock()
	defer regMu.RUnlock()
	out := make([]Spec, 0, len(registry))
	for _, s := range registry {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Kind < out[j].Kind })
	return out
}

// Build constructs a connector of kind from config and secret.
func Build(kind Kind, config json.RawMessage, secret secrets.String) (Connector, error) {
	s, ok := Lookup(kind)
	if !ok {
		return nil, fmt.Errorf("unknown wiki type %q", kind)
	}
	return s.New(config, secret)
}
