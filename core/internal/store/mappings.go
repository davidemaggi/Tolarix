package store

import (
	"context"

	"github.com/davidemaggi/tolarix/core/internal/engine"
)

var _ engine.MappingStore = (*Store)(nil)

func (s *Store) LoadMappings(ctx context.Context, targetID string) ([]engine.Mapping, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT source_page_id, mirror_page_id, mirror_path, source_hash, body_hash, mirror_hash, state
		FROM page_mappings WHERE target_id = ? ORDER BY source_page_id`, targetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []engine.Mapping
	for rows.Next() {
		var m engine.Mapping
		if err := rows.Scan(&m.SourceID, &m.MirrorID, &m.MirrorKey, &m.SourceHash, &m.BodyHash, &m.MirrorHash, &m.State); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) UpsertMapping(ctx context.Context, targetID string, m engine.Mapping) error {
	if m.State == "" {
		m.State = engine.StateActive
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO page_mappings
		(target_id, source_page_id, mirror_page_id, mirror_path, source_hash, body_hash, mirror_hash, state, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (target_id, source_page_id) DO UPDATE SET
			mirror_page_id = excluded.mirror_page_id, mirror_path = excluded.mirror_path,
			source_hash = excluded.source_hash, body_hash = excluded.body_hash,
			mirror_hash = excluded.mirror_hash, state = excluded.state, updated_at = excluded.updated_at`,
		targetID, m.SourceID, m.MirrorID, m.MirrorKey, m.SourceHash, m.BodyHash, m.MirrorHash, m.State, now())
	return err
}

func (s *Store) DeleteMapping(ctx context.Context, targetID, sourceID string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM page_mappings WHERE target_id = ? AND source_page_id = ?`, targetID, sourceID)
	return err
}

func (s *Store) LoadAttachmentMappings(ctx context.Context, targetID string) (map[string]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT content_hash, mirror_ref FROM attachment_mappings WHERE target_id = ?`, targetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var h, ref string
		if err := rows.Scan(&h, &ref); err != nil {
			return nil, err
		}
		out[h] = ref
	}
	return out, rows.Err()
}

func (s *Store) UpsertAttachmentMapping(ctx context.Context, targetID, hash, key string) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO attachment_mappings (target_id, content_hash, mirror_ref, updated_at)
		VALUES (?, ?, ?, ?) ON CONFLICT (target_id, content_hash) DO UPDATE SET mirror_ref = excluded.mirror_ref, updated_at = excluded.updated_at`,
		targetID, hash, key, now())
	return err
}

func (s *Store) DeleteAttachmentMapping(ctx context.Context, targetID, hash string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM attachment_mappings WHERE target_id = ? AND content_hash = ?`, targetID, hash)
	return err
}
