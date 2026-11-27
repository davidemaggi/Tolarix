-- Body hash of the Master page, used to recognise renames/moves of pages
-- whose id is their path (Obsidian, Azure DevOps).

-- +goose Up
ALTER TABLE page_mappings ADD COLUMN body_hash TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE page_mappings DROP COLUMN body_hash;
