-- +migrate Up
ALTER TABLE tracked_addresses ADD COLUMN required_confirmations INTEGER NOT NULL DEFAULT 6;

-- +migrate Down
ALTER TABLE tracked_addresses DROP COLUMN required_confirmations; 