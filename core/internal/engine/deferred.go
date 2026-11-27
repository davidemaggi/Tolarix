package engine

import (
	"context"
	"fmt"
)

// deferredStore records mapping changes instead of saving them, for sessions
// that only apply writes in Finish (connectors.Buffering). flush saves them
// once Finish has succeeded, so a failed push leaves the mapping table as it
// was and the next run plans the same changes again.
type deferredStore struct {
	MappingStore // reads pass through
	writes       []func(ctx context.Context) error
}

func (d *deferredStore) UpsertMapping(_ context.Context, targetID string, m Mapping) error {
	d.writes = append(d.writes, func(ctx context.Context) error { return d.MappingStore.UpsertMapping(ctx, targetID, m) })
	return nil
}

func (d *deferredStore) DeleteMapping(_ context.Context, targetID, sourceID string) error {
	d.writes = append(d.writes, func(ctx context.Context) error { return d.MappingStore.DeleteMapping(ctx, targetID, sourceID) })
	return nil
}

func (d *deferredStore) UpsertAttachmentMapping(_ context.Context, targetID, hash, key string) error {
	d.writes = append(d.writes, func(ctx context.Context) error {
		return d.MappingStore.UpsertAttachmentMapping(ctx, targetID, hash, key)
	})
	return nil
}

func (d *deferredStore) DeleteAttachmentMapping(_ context.Context, targetID, hash string) error {
	d.writes = append(d.writes, func(ctx context.Context) error {
		return d.MappingStore.DeleteAttachmentMapping(ctx, targetID, hash)
	})
	return nil
}

func (d *deferredStore) flush(ctx context.Context) error {
	for _, w := range d.writes {
		if err := w(ctx); err != nil {
			return fmt.Errorf("save the mapping table: %w", err)
		}
	}
	d.writes = nil
	return nil
}

// markUnsaved reports every change of a buffered session as failed when
// its final write (the push) did not happen.
func markUnsaved(ops []Op, attOps []AttachmentOp, err error) {
	msg := "not saved: " + err.Error()
	for i := range ops {
		switch ops[i].Kind {
		case OpUnchanged, OpConflict:
		default:
			if ops[i].Error == "" {
				ops[i].Error = msg
			}
		}
	}
	for i := range attOps {
		if attOps[i].Error == "" {
			attOps[i].Error = msg
		}
	}
}
