-- +migrate Up
CREATE TABLE IF NOT EXISTS transactions (
    id SERIAL PRIMARY KEY,
    address_id INTEGER NOT NULL REFERENCES tracked_addresses(id),
    tx_id VARCHAR(64) NOT NULL,
    block_hash VARCHAR(64),
    block_height BIGINT,
    amount DECIMAL(20,8) NOT NULL,
    is_incoming BOOLEAN NOT NULL,
    confirmations INTEGER NOT NULL DEFAULT 0,
    from_address VARCHAR(34),
    to_address VARCHAR(34),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(address_id, tx_id)
);

-- +migrate Down
DROP TABLE IF EXISTS transactions; 