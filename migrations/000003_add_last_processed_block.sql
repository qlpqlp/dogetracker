-- +migrate Up
CREATE TABLE IF NOT EXISTS last_processed_block (
    id SERIAL PRIMARY KEY,
    block_height BIGINT NOT NULL,
    block_hash VARCHAR(64) NOT NULL,
    processed_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- +migrate Down
DROP TABLE IF EXISTS last_processed_block; 