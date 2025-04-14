-- +migrate Up
CREATE TABLE IF NOT EXISTS transactions (
    id SERIAL PRIMARY KEY,
    tx_id VARCHAR(64) NOT NULL,
    address VARCHAR(34) NOT NULL,
    amount DECIMAL(20,8) NOT NULL,
    block_height BIGINT,
    confirmations INTEGER NOT NULL DEFAULT 0,
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(tx_id, address)
);

-- +migrate Down
DROP TABLE IF EXISTS transactions; 