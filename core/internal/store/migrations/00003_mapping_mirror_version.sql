-- Version token of the mirror page as Tolarix last wrote it, for targets that
-- rewrite stored content (Confluence normalises storage XML), where comparing
-- content hashes cannot detect edits made on the mirror.

-- +goose Up
ALTER TABLE page_mappings ADD COLUMN mirror_version TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE page_mappings DROP COLUMN mirror_version;
