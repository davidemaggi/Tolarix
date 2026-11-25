// Package engine computes and applies the change plan that brings each
// Mirror in line with the Master.
//
// One run: snapshot the Master once -> filter by scope -> render derived
// attachments -> for every target: layout, encode, diff against the mapping
// table and the target inventory -> plan -> (unless dry-run) apply. The
// mapping is updated after every successful operation, so an interrupted run
// resumes cleanly and a repeated run is a no-op.
package engine

import (
	"context"
	"log/slog"

	"github.com/davidemaggi/tolarix/core/internal/connectors"
)

// DeletePolicy says what happens on a mirror when a page leaves the Master.
type DeletePolicy string

const (
	DeleteOnMirror  DeletePolicy = "delete"
	ArchiveOnMirror DeletePolicy = "archive"
	KeepOnMirror    DeletePolicy = "keep"
)

// Options are the per-flow sync options.
type Options struct {
	DeletePolicy DeletePolicy `json:"deletePolicy"`
	// Attachments copies attachments to mirrors (default true).
	Attachments *bool `json:"attachments,omitempty"`
	// Banner adds a "this page is mirrored" notice at the top of every page.
	Banner bool `json:"banner"`
	// AdoptExisting lets Tolarix take over pages it did not create when they
	// sit exactly where a mirrored page goes. Off by default: they are left
	// alone and that page fails with an explanation.
	AdoptExisting bool `json:"adoptExisting"`
}

// AttachmentsEnabled applies the default (on).
func (o Options) AttachmentsEnabled() bool { return o.Attachments == nil || *o.Attachments }

// Policy applies the default (archive: nothing is lost by default).
func (o Options) Policy() DeletePolicy {
	switch o.DeletePolicy {
	case DeleteOnMirror, KeepOnMirror:
		return o.DeletePolicy
	default:
		return ArchiveOnMirror
	}
}

// Scope selects which Master pages a flow mirrors.
type Scope struct {
	Mode    string   `json:"mode"`              // "all" (default) | "selected"
	Include []string `json:"include,omitempty"` // subtree roots, when Mode is "selected"
	Exclude []string `json:"exclude,omitempty"` // subtrees removed in either mode
}

// Mapping links a Master page to its mirror page in one target.
type Mapping struct {
	SourceID   string
	MirrorID   string
	MirrorKey  string
	SourceHash string
	BodyHash   string
	MirrorHash string
	State      string // active | archived | kept
}

const (
	StateActive   = "active"
	StateArchived = "archived"
	StateKept     = "kept"
)

// MappingStore persists mappings per target.
type MappingStore interface {
	LoadMappings(ctx context.Context, targetID string) ([]Mapping, error)
	UpsertMapping(ctx context.Context, targetID string, m Mapping) error
	DeleteMapping(ctx context.Context, targetID, sourceID string) error
	LoadAttachmentMappings(ctx context.Context, targetID string) (map[string]string, error) // hash -> key
	UpsertAttachmentMapping(ctx context.Context, targetID, hash, key string) error
	DeleteAttachmentMapping(ctx context.Context, targetID, hash string) error
}

// Target is one mirror of the run.
type Target struct {
	ID      string // flow target id (mapping namespace)
	Name    string // for logs and reports
	Session connectors.Session
}

// Logger receives live run log lines (persisted as run events and streamed).
type Logger interface {
	Log(ctx context.Context, level slog.Level, msg string, attrs ...any)
}

// OpKind is one kind of change.
type OpKind string

const (
	OpCreate    OpKind = "create"
	OpUpdate    OpKind = "update"
	OpMove      OpKind = "move"
	OpDelete    OpKind = "delete"
	OpArchive   OpKind = "archive"
	OpKeep      OpKind = "keep"
	OpUnchanged OpKind = "unchanged"
	OpConflict  OpKind = "conflict"
)

// Op is one planned change, as shown in the plan and the run report.
type Op struct {
	Kind     OpKind `json:"kind"`
	SourceID string `json:"sourceId"`
	Title    string `json:"title"`
	From     string `json:"from,omitempty"` // current mirror key
	To       string `json:"to,omitempty"`   // new mirror key
	Reason   string `json:"reason,omitempty"`
	Error    string `json:"error,omitempty"`

	page    string // page ID in the tree ("" for orphans)
	mapping *Mapping
	adopt   *connectors.MirrorItem
}

// AttachmentOp is a planned attachment upload or removal.
type AttachmentOp struct {
	Kind  string `json:"kind"` // upload | delete
	Hash  string `json:"hash"`
	Name  string `json:"name,omitempty"`
	Key   string `json:"key"`
	Error string `json:"error,omitempty"`
}

// Counts summarises a target or a whole run.
type Counts struct {
	Created             int `json:"created"`
	Updated             int `json:"updated"`
	Moved               int `json:"moved"`
	Deleted             int `json:"deleted"`
	Archived            int `json:"archived"`
	Kept                int `json:"kept"`
	Unchanged           int `json:"unchanged"`
	Failed              int `json:"failed"`
	AttachmentsUploaded int `json:"attachmentsUploaded"`
	AttachmentsDeleted  int `json:"attachmentsDeleted"`
}

// Skipped is what the brief calls "skipped": pages left as they are.
func (c Counts) Skipped() int { return c.Unchanged + c.Kept }

// Changes is the number of writes (planned or done).
func (c Counts) Changes() int {
	return c.Created + c.Updated + c.Moved + c.Deleted + c.Archived + c.AttachmentsUploaded + c.AttachmentsDeleted
}

func (c *Counts) add(o Counts) {
	c.Created += o.Created
	c.Updated += o.Updated
	c.Moved += o.Moved
	c.Deleted += o.Deleted
	c.Archived += o.Archived
	c.Kept += o.Kept
	c.Unchanged += o.Unchanged
	c.Failed += o.Failed
	c.AttachmentsUploaded += o.AttachmentsUploaded
	c.AttachmentsDeleted += o.AttachmentsDeleted
}

// TargetReport is the outcome for one mirror.
type TargetReport struct {
	TargetID    string               `json:"targetId"`
	Name        string               `json:"name"`
	Counts      Counts               `json:"counts"`
	Ops         []Op                 `json:"ops"`
	Attachments []AttachmentOp       `json:"attachments,omitempty"`
	Warnings    []connectors.Warning `json:"warnings,omitempty"`
	Error       string               `json:"error,omitempty"` // the target could not be synced at all
}

// Report is the outcome of a run.
type Report struct {
	DryRun   bool                 `json:"dryRun"`
	Pages    int                  `json:"pages"` // Master pages in scope
	Counts   Counts               `json:"counts"`
	Targets  []TargetReport       `json:"targets"`
	Warnings []connectors.Warning `json:"warnings,omitempty"` // source-side warnings
}

// Status derives the run status from the report.
func (r *Report) Status() string {
	failedTargets := 0
	for _, t := range r.Targets {
		if t.Error != "" {
			failedTargets++
		}
	}
	switch {
	case len(r.Targets) > 0 && failedTargets == len(r.Targets):
		return "failed"
	case failedTargets > 0 || r.Counts.Failed > 0:
		return "partial"
	default:
		return "succeeded"
	}
}
