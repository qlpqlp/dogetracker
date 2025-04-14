-- +migrate Up
CREATE TABLE IF NOT EXISTS unspent_outputs (
    id SERIAL PRIMARY KEY,
    address_id INTEGER NOT NULL REFERENCES tracked_addresses(id),
    tx_id VARCHAR(64) NOT NULL,
    vout INTEGER NOT NULL,
    amount DECIMAL(20,8) NOT NULL,
    script TEXT NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(address_id, tx_id, vout)
);

CREATE INDEX IF NOT EXISTS idx_unspent_outputs_address_id ON unspent_outputs(address_id);
CREATE INDEX IF NOT EXISTS idx_unspent_outputs_tx_id ON unspent_outputs(tx_id);

-- +migrate Down
DROP TABLE IF EXISTS unspent_outputs; 