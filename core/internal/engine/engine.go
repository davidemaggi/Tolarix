package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"

	"github.com/davidemaggi/tolarix/core/internal/connectors"
	"github.com/davidemaggi/tolarix/core/internal/model"
	"github.com/davidemaggi/tolarix/core/internal/render"
)

// Engine runs flows. It holds no per-run state.
type Engine struct {
	Mappings  MappingStore
	Log       Logger
	Renderers []render.Renderer
}

// Request is one run.
type Request struct {
	Source     connectors.Source
	SourceName string // shown in the mirror banner
	Scope      Scope
	Options    Options
	Targets    []Target
	DryRun     bool
}

// BannerText is prepended (as a canonical callout) when Options.Banner is on.
// custom replaces the default English sentence; "{master}" in it is replaced
// by the Master connection name.
func BannerText(source, custom string) string {
	custom = strings.TrimSpace(custom)
	if custom == "" {
		return "> [!warning] Mirrored page\n> This page is mirrored from " + source +
			". Changes made here will be overwritten.\n\n"
	}
	custom = strings.ReplaceAll(custom, "{master}", source)
	var b strings.Builder
	b.WriteString("> [!warning]\n")
	for _, line := range strings.Split(strings.ReplaceAll(custom, "\r\n", "\n"), "\n") {
		b.WriteString(strings.TrimRight("> "+line, " ") + "\n")
	}
	b.WriteString("\n")
	return b.String()
}

// Run executes the request. It returns an error only when nothing could be
// done (e.g. the Master is unreachable); per-page and per-target failures are
// in the report.
func (e *Engine) Run(ctx context.Context, req Request) (*Report, error) {
	rep := &Report{DryRun: req.DryRun}
	var srcWarn connectors.Warnings
	var blobs *render.Pipeline

	e.log(ctx, slog.LevelInfo, "reading the Master")
	full, err := req.Source.Snapshot(ctx, &srcWarn)
	if err != nil {
		return rep, fmt.Errorf("read the Master: %w", err)
	}
	tree := ApplyScope(full, req.Scope, &srcWarn)
	rep.Pages = len(tree.Pages)
	e.log(ctx, slog.LevelInfo, "Master read", "pages", len(tree.Pages), "attachments", len(tree.Attachments))

	if req.Options.AttachmentsEnabled() {
		pipe := &render.Pipeline{Renderers: e.Renderers}
		pipe.Apply(ctx, tree, func(a *model.Attachment) (io.ReadCloser, error) {
			return req.Source.OpenAttachment(ctx, a.SourceRef)
		}, &srcWarn)
		blobs = pipe
	}
	if req.Options.Banner {
		banner := BannerText(firstNonEmpty(req.SourceName, "the Master wiki"), req.Options.BannerText)
		for _, p := range tree.Pages {
			if !p.Placeholder {
				p.Body = banner + p.Body
			}
		}
	}
	rep.Warnings = srcWarn.List
	for _, w := range srcWarn.List {
		e.log(ctx, slog.LevelWarn, w.Message, "page", w.PageID, "code", w.Code)
	}

	for _, t := range req.Targets {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		tr := e.runTarget(ctx, req, tree, t, blobs)
		rep.Counts.add(tr.Counts)
		rep.Targets = append(rep.Targets, tr)
	}
	return rep, nil
}

type desired struct {
	page      *model.Page
	placement connectors.Placement
	content   []byte
	hash      string
}

func (e *Engine) runTarget(ctx context.Context, req Request, tree *model.Tree, t Target, blobs *render.Pipeline) TargetReport {
	tr := TargetReport{TargetID: t.ID, Name: t.Name}
	var ws connectors.Warnings
	fail := func(format string, a ...any) TargetReport {
		tr.Error = fmt.Sprintf(format, a...)
		e.log(ctx, slog.LevelError, "mirror failed", "target", t.Name, "error", tr.Error)
		tr.Warnings = ws.List
		return tr
	}
	e.log(ctx, slog.LevelInfo, "planning mirror", "target", t.Name)
	s := t.Session

	layout := s.Layout(tree, &ws)
	if !req.Options.AttachmentsEnabled() {
		layout.Attachments = map[string]string{}
	}
	want := map[string]*desired{}
	var order []string
	var encErr error
	tree.Walk(func(p *model.Page, _ int) {
		if encErr != nil {
			return
		}
		content, err := s.Encode(tree, p, layout, &ws)
		if err != nil {
			encErr = err
			return
		}
		d := &desired{page: p, placement: layout.Pages[p.ID], content: content}
		if !p.Placeholder {
			d.hash = s.ContentHash(content)
		}
		want[p.ID] = d
		order = append(order, p.ID)
	})
	if encErr != nil {
		return fail("convert pages: %v", encErr)
	}

	inv, err := s.Inventory(ctx)
	if err != nil {
		return fail("list the pages already in the mirror: %v", err)
	}
	maps, err := e.Mappings.LoadMappings(ctx, t.ID)
	if err != nil {
		return fail("load the mapping table: %v", err)
	}
	attMaps, err := e.Mappings.LoadAttachmentMappings(ctx, t.ID)
	if err != nil {
		return fail("load the attachment mapping table: %v", err)
	}

	ops := plan(tree, want, order, maps, inv, req.Options, &ws)
	attOps, err := planAttachments(ctx, s, tree, layout, attMaps, req.Options)
	if err != nil {
		return fail("check attachments in the mirror: %v", err)
	}

	if !req.DryRun {
		store := e.Mappings
		var deferred *deferredStore
		if b, ok := s.(connectors.Buffering); ok && b.BuffersWrites() {
			deferred = &deferredStore{MappingStore: e.Mappings}
			store = deferred
		}
		ops, attOps = e.apply(ctx, req, t, store, tree, blobs, want, ops, attOps)
		ferr := s.Finish(ctx, ctx.Err() == nil)
		if ferr == nil && deferred != nil {
			ferr = deferred.flush(ctx)
		}
		if ferr != nil {
			tr.Error = fmt.Sprintf("finish writing: %v", ferr)
			if deferred != nil {
				markUnsaved(ops, attOps, ferr)
			}
		}
	}
	tr.Ops, tr.Attachments = ops, attOps
	tr.Counts = count(ops, attOps)
	tr.Warnings = ws.List
	for _, w := range ws.List {
		e.log(ctx, slog.LevelWarn, w.Message, "target", t.Name, "page", w.PageID, "code", w.Code)
	}
	e.log(ctx, slog.LevelInfo, "mirror done", "target", t.Name, "dry_run", req.DryRun,
		"created", tr.Counts.Created, "updated", tr.Counts.Updated, "moved", tr.Counts.Moved,
		"deleted", tr.Counts.Deleted, "archived", tr.Counts.Archived, "unchanged", tr.Counts.Unchanged,
		"failed", tr.Counts.Failed)
	return tr
}

// plan compares the desired state with the mapping table and the inventory.
func plan(tree *model.Tree, want map[string]*desired, order []string, maps []Mapping,
	inv map[string]connectors.MirrorItem, opts Options, w connectors.Warner) []Op {

	byID := map[string]*Mapping{}
	managedKey := map[string]string{} // mirror key -> source id
	for i := range maps {
		m := &maps[i]
		byID[m.SourceID] = m
		managedKey[m.MirrorKey] = m.SourceID
	}

	// Move detection for path-identity sources: a page whose ID disappeared
	// and a new page with the same body are the same page, renamed or moved.
	orphansByBody := map[string][]*Mapping{}
	for i := range maps {
		m := &maps[i]
		if _, still := want[m.SourceID]; !still && m.BodyHash != "" {
			orphansByBody[m.BodyHash] = append(orphansByBody[m.BodyHash], m)
		}
	}
	newByBody := map[string][]string{}
	for _, id := range order {
		p := want[id].page
		if _, mapped := byID[id]; !mapped && strings.TrimSpace(p.Body) != "" {
			newByBody[p.BodyHash()] = append(newByBody[p.BodyHash()], id)
		}
	}
	renamedFrom := map[string]*Mapping{} // new source id -> old mapping
	for body, ids := range newByBody {
		if olds := orphansByBody[body]; len(olds) == 1 && len(ids) == 1 {
			renamedFrom[ids[0]] = olds[0]
		}
	}
	claimed := map[string]bool{}
	for _, m := range renamedFrom {
		claimed[m.SourceID] = true
	}

	var ops []Op
	// Orphans first: deletes and archives free their keys for pages written after them.
	var orphans []*Mapping
	for i := range maps {
		m := &maps[i]
		if _, still := want[m.SourceID]; !still && !claimed[m.SourceID] {
			orphans = append(orphans, m)
		}
	}
	sort.Slice(orphans, func(i, j int) bool {
		di, dj := strings.Count(orphans[i].MirrorKey, "/"), strings.Count(orphans[j].MirrorKey, "/")
		if di != dj {
			return di > dj
		}
		return orphans[i].MirrorKey < orphans[j].MirrorKey
	})
	for _, m := range orphans {
		op := Op{SourceID: m.SourceID, Title: m.SourceID, From: m.MirrorKey, mapping: m}
		_, present := inv[m.MirrorKey]
		switch {
		case !present:
			op.Kind, op.Reason = OpDelete, "already gone from the mirror"
		case opts.Policy() == DeleteOnMirror:
			op.Kind, op.Reason = OpDelete, "removed from the Master"
		case m.MirrorHash == "" && opts.Policy() == ArchiveOnMirror:
			// A folder-only page has no content to archive; its children are archived on their own.
			op.Kind, op.Reason = OpDelete, "folder-only page removed from the Master"
		case opts.Policy() == KeepOnMirror || m.State == StateKept:
			op.Kind, op.Reason = OpKeep, "removed from the Master; kept on the mirror (delete policy: keep)"
		case m.State == StateArchived:
			op.Kind, op.Reason = OpUnchanged, "already archived"
		default:
			op.Kind, op.Reason = OpArchive, "removed from the Master"
		}
		ops = append(ops, op)
	}

	for _, id := range order {
		d := want[id]
		key := d.placement.Key
		op := Op{SourceID: id, Title: d.page.Title, To: key, page: id}
		m := byID[id]
		if m == nil {
			m = renamedFrom[id]
			if m != nil {
				op.Reason = "renamed or moved in the Master (was " + m.SourceID + ")"
			}
		}
		if m == nil {
			item, exists := inv[key]
			switch {
			case !exists || d.page.Placeholder:
				op.Kind = OpCreate
			case managedKey[key] != "":
				op.Kind = OpConflict
				op.Error = fmt.Sprintf("another mirrored page (%s) is still at %s; run the flow again after this run finishes", managedKey[key], key)
			case opts.AdoptExisting:
				op.Kind, op.Reason, op.adopt = OpUpdate, "adopted an existing page that Tolarix did not create", &item
			default:
				op.Kind = OpConflict
				op.Error = fmt.Sprintf("a page that Tolarix did not create already exists at %s. Rename or move it in the mirror, or turn on \"adopt existing pages\" for this mirror", key)
			}
			ops = append(ops, op)
			continue
		}

		op.mapping = m
		op.From = m.MirrorKey
		item, present := inv[m.MirrorKey]
		switch {
		case !present:
			op.Kind, op.Reason = OpCreate, "missing from the mirror (deleted by someone?); recreated"
			w.Warn(connectors.Warning{PageID: id, Code: "mirror-missing",
				Message: fmt.Sprintf("the mirror page %s was deleted outside Tolarix and is created again", m.MirrorKey)})
		case m.MirrorKey != key:
			op.Kind = OpMove
			if m.State == StateArchived {
				op.Reason = "back in the Master; restored from the archive"
			} else if op.Reason == "" {
				op.Reason = "moved or renamed in the Master"
			}
		case !d.page.Placeholder && editedOnMirror(item, m):
			op.Kind, op.Reason = OpUpdate, "changed on the mirror by someone; overwritten"
			w.Warn(connectors.Warning{PageID: id, Code: "mirror-edited",
				Message: fmt.Sprintf("the mirror page %s was edited outside Tolarix; the changes are overwritten", m.MirrorKey)})
		case d.hash != m.MirrorHash:
			op.Kind, op.Reason = OpUpdate, "changed in the Master"
		default:
			op.Kind = OpUnchanged
		}
		// Target occupied by an unrelated page?
		if op.Kind == OpMove {
			if _, taken := inv[key]; taken && !d.page.Placeholder {
				if owner := managedKey[key]; owner != "" && owner != m.SourceID {
					op.Kind = OpConflict
					op.Error = fmt.Sprintf("another mirrored page (%s) is still at %s; run the flow again after this run finishes", owner, key)
				} else if owner == "" && !opts.AdoptExisting {
					op.Kind = OpConflict
					op.Error = fmt.Sprintf("a page that Tolarix did not create already exists at %s. Rename or move it in the mirror, or turn on \"adopt existing pages\" for this mirror", key)
				}
			}
		}
		ops = append(ops, op)
	}
	return ops
}

// editedOnMirror reports whether a mirror page changed since Tolarix wrote it.
func editedOnMirror(item connectors.MirrorItem, m *Mapping) bool {
	if item.Version != "" || m.MirrorVersion != "" {
		return item.Version != m.MirrorVersion
	}
	return item.Hash != m.MirrorHash
}

// mirrorItem is the mirror page a mapping points to.
func (m *Mapping) mirrorItem() connectors.MirrorItem {
	return connectors.MirrorItem{ID: m.MirrorID, Key: m.MirrorKey, Hash: m.MirrorHash, Version: m.MirrorVersion}
}

func planAttachments(ctx context.Context, s connectors.Session, tree *model.Tree, l *connectors.Layout,
	current map[string]string, opts Options) ([]AttachmentOp, error) {
	var ops []AttachmentOp
	hashes := make([]string, 0, len(l.Attachments))
	for h := range l.Attachments {
		hashes = append(hashes, h)
	}
	sort.Strings(hashes)
	for _, h := range hashes {
		key := l.Attachments[h]
		if current[h] == key {
			ok, err := s.HasAttachment(ctx, key)
			if err != nil {
				return nil, err
			}
			if ok {
				continue
			}
		}
		name := ""
		if a := tree.Attachments[h]; a != nil {
			name = a.Name
		}
		ops = append(ops, AttachmentOp{Kind: "upload", Hash: h, Name: name, Key: key})
	}
	if opts.Policy() == DeleteOnMirror {
		var stale []string
		for h, key := range current {
			if l.Attachments[h] != key {
				stale = append(stale, h)
			}
		}
		sort.Strings(stale)
		for _, h := range stale {
			// Do not delete a key that is (re)used by another hash in this layout.
			inUse := false
			for _, k := range l.Attachments {
				if k == current[h] {
					inUse = true
				}
			}
			if !inUse {
				ops = append(ops, AttachmentOp{Kind: "delete", Hash: h, Key: current[h]})
			}
		}
	}
	return ops, nil
}

// apply executes the plan, updating the mapping after each success.
func (e *Engine) apply(ctx context.Context, req Request, t Target, store MappingStore, tree *model.Tree, blobs *render.Pipeline,
	want map[string]*desired, ops []Op, attOps []AttachmentOp) ([]Op, []AttachmentOp) {
	s := t.Session

	// Uploads before pages so links resolve as soon as a page appears.
	for i := range attOps {
		a := &attOps[i]
		if a.Kind != "upload" {
			continue
		}
		if err := ctx.Err(); err != nil {
			a.Error = err.Error()
			continue
		}
		if err := e.upload(ctx, req, s, tree, blobs, a); err != nil {
			a.Error = err.Error()
			e.log(ctx, slog.LevelError, "attachment upload failed", "target", t.Name, "file", a.Name, "error", err)
			continue
		}
		if err := store.UpsertAttachmentMapping(ctx, t.ID, a.Hash, a.Key); err != nil {
			a.Error = err.Error()
		}
	}

	for i := range ops {
		op := &ops[i]
		if op.Kind == OpUnchanged || op.Kind == OpConflict {
			if op.Kind == OpUnchanged && op.page != "" && op.mapping != nil {
				d := want[op.page]
				if op.mapping.SourceID != op.page || op.mapping.BodyHash != d.page.BodyHash() || op.mapping.State != StateActive {
					if err := persist(ctx, store, t, op, d, op.mapping.mirrorItem()); err != nil {
						op.Error = err.Error()
					}
				}
			}
			continue
		}
		if err := ctx.Err(); err != nil {
			op.Error = err.Error()
			continue
		}
		var err error
		switch op.Kind {
		case OpCreate, OpUpdate, OpMove:
			d := want[op.page]
			w := connectors.PageWrite{Placement: d.placement, Content: d.content, Placeholder: d.page.Placeholder}
			if op.adopt != nil {
				w.Existing = op.adopt
			} else if op.mapping != nil && op.Kind != OpCreate {
				existing := op.mapping.mirrorItem()
				w.Existing = &existing
			}
			var item connectors.MirrorItem
			item, err = s.WritePage(ctx, w)
			if err == nil {
				err = persist(ctx, store, t, op, d, item)
			}
		case OpDelete:
			if op.From != "" && op.Reason != "already gone from the mirror" {
				err = s.DeletePage(ctx, op.mapping.mirrorItem())
			}
			if err == nil {
				err = store.DeleteMapping(ctx, t.ID, op.mapping.SourceID)
			}
		case OpArchive:
			var item connectors.MirrorItem
			item, err = s.ArchivePage(ctx, op.mapping.mirrorItem())
			if err == nil {
				m := *op.mapping
				m.MirrorID, m.MirrorKey, m.MirrorHash, m.MirrorVersion, m.State = item.ID, item.Key, item.Hash, item.Version, StateArchived
				op.To = item.Key
				err = store.UpsertMapping(ctx, t.ID, m)
			}
		case OpKeep:
			if op.mapping.State != StateKept {
				m := *op.mapping
				m.State = StateKept
				err = store.UpsertMapping(ctx, t.ID, m)
			}
		}
		if err != nil {
			op.Error = err.Error()
			e.log(ctx, slog.LevelError, "page failed", "target", t.Name, "page", op.SourceID, "op", op.Kind, "error", err)
		}
	}

	for i := range attOps {
		a := &attOps[i]
		if a.Kind != "delete" {
			continue
		}
		if err := s.DeleteAttachment(ctx, a.Key); err != nil {
			a.Error = err.Error()
			continue
		}
		if err := store.DeleteAttachmentMapping(ctx, t.ID, a.Hash); err != nil {
			a.Error = err.Error()
		}
	}
	return ops, attOps
}

func persist(ctx context.Context, store MappingStore, t Target, op *Op, d *desired, item connectors.MirrorItem) error {
	m := Mapping{
		SourceID: d.page.ID, MirrorID: item.ID, MirrorKey: item.Key,
		SourceHash: d.page.ContentHash(), BodyHash: d.page.BodyHash(), MirrorHash: item.Hash,
		MirrorVersion: item.Version, State: StateActive,
	}
	if err := store.UpsertMapping(ctx, t.ID, m); err != nil {
		return err
	}
	if op.mapping != nil && op.mapping.SourceID != d.page.ID {
		return store.DeleteMapping(ctx, t.ID, op.mapping.SourceID)
	}
	return nil
}

func (e *Engine) upload(ctx context.Context, req Request, s connectors.Session, tree *model.Tree, blobs *render.Pipeline, a *AttachmentOp) error {
	att := tree.Attachments[a.Hash]
	if att == nil {
		return errors.New("attachment not found in the Master snapshot")
	}
	var r io.ReadCloser
	if b, ok := blobs.Blob(a.Hash); ok {
		r = io.NopCloser(bytes.NewReader(b))
	} else {
		var err error
		if r, err = req.Source.OpenAttachment(ctx, att.SourceRef); err != nil {
			return fmt.Errorf("read from the Master: %w", err)
		}
	}
	defer r.Close()
	return s.PutAttachment(ctx, a.Key, r)
}

func count(ops []Op, attOps []AttachmentOp) Counts {
	var c Counts
	for _, op := range ops {
		if op.Error != "" {
			c.Failed++
			continue
		}
		switch op.Kind {
		case OpCreate:
			c.Created++
		case OpUpdate:
			c.Updated++
		case OpMove:
			c.Moved++
		case OpDelete:
			c.Deleted++
		case OpArchive:
			c.Archived++
		case OpKeep:
			c.Kept++
		case OpUnchanged:
			c.Unchanged++
		}
	}
	for _, a := range attOps {
		switch {
		case a.Error != "":
			c.Failed++
		case a.Kind == "upload":
			c.AttachmentsUploaded++
		case a.Kind == "delete":
			c.AttachmentsDeleted++
		}
	}
	return c
}

func (e *Engine) log(ctx context.Context, lvl slog.Level, msg string, attrs ...any) {
	if e.Log != nil {
		e.Log.Log(ctx, lvl, msg, attrs...)
	}
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}
