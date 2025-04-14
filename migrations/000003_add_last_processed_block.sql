-- +migrate Up
CREATE TABLE IF NOT EXISTS last_processed_block (
    id SERIAL PRIMARY KEY,
    block_height BIGINT NOT NULL,
    block_hash VARCHAR(64) NOT NULL,
    processed_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Insert initial record
INSERT INTO last_processed_block (block_height, block_hash) 
VALUES (0, '0e0bd6be24f5f426a505694bf46f60301a3a08dfdfda13854fdfe0ce7d455d6f')
ON CONFLICT DO NOTHING;

-- +migrate Down
DROP TABLE IF EXISTS last_processed_block; 