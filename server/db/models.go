package db

import (
	"database/sql"
	"time"
)

// TrackedAddress represents a Dogecoin address being tracked
type TrackedAddress struct {
	ID                    int64     `json:"id"`
	Address               string    `json:"address"`
	Balance               float64   `json:"balance"`
	RequiredConfirmations int       `json:"required_confirmations"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`
}

// Transaction represents a Dogecoin transaction involving a tracked address
type Transaction struct {
	ID              int64     `json:"id"`
	AddressID       int64     `json:"address_id"`
	TxID            string    `json:"tx_id"`
	BlockHash       string    `json:"block_hash"`
	BlockHeight     int64     `json:"block_height"`
	Amount          float64   `json:"amount"`
	Fee             float64   `json:"fee"`       // Transaction fee in DOGE
	Timestamp       int64     `json:"timestamp"` // Transaction timestamp from blockchain
	IsIncoming      bool      `json:"is_incoming"`
	Confirmations   int       `json:"confirmations"`
	Status          string    `json:"status"`           // "pending" or "confirmed"
	SenderAddress   string    `json:"sender_address"`   // Address that sent the transaction
	ReceiverAddress string    `json:"receiver_address"` // Address that received the transaction
	CreatedAt       time.Time `json:"created_at"`
}

// UnspentOutput represents an unspent transaction output for a tracked address
type UnspentOutput struct {
	ID        int64     `json:"id"`
	AddressID int64     `json:"address_id"`
	TxID      string    `json:"tx_id"`
	Vout      int       `json:"vout"`
	Amount    float64   `json:"amount"`
	Script    string    `json:"script"`
	CreatedAt time.Time `json:"created_at"`
}

// InitDB initializes the database schema
func InitDB(db *sql.DB) error {
	// Create blocks table
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS blocks (
			hash VARCHAR(64) PRIMARY KEY,
			height BIGINT NOT NULL,
			version INTEGER NOT NULL,
			prev_block VARCHAR(64) NOT NULL,
			merkle_root VARCHAR(64) NOT NULL,
			time BIGINT NOT NULL,
			bits INTEGER NOT NULL,
			nonce INTEGER NOT NULL,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return err
	}

	// Create headers table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS headers (
			hash VARCHAR(64) PRIMARY KEY,
			height BIGINT NOT NULL,
			version INTEGER NOT NULL,
			prev_block VARCHAR(64) NOT NULL,
			merkle_root VARCHAR(64) NOT NULL,
			time BIGINT NOT NULL,
			bits INTEGER NOT NULL,
			nonce INTEGER NOT NULL,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return err
	}

	// Create tracked_addresses table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS tracked_addresses (
			id SERIAL PRIMARY KEY,
			address VARCHAR(34) UNIQUE NOT NULL,
			balance DECIMAL(20,8) NOT NULL DEFAULT 0,
			required_confirmations INTEGER NOT NULL DEFAULT 1,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return err
	}

	// Create transactions table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS transactions (
			id SERIAL PRIMARY KEY,
			address_id INTEGER NOT NULL REFERENCES tracked_addresses(id),
			tx_id VARCHAR(64) NOT NULL,
			block_hash VARCHAR(64),
			block_height BIGINT,
			amount DECIMAL(20,8) NOT NULL,
			fee DECIMAL(20,8) NOT NULL DEFAULT 0,
			timestamp BIGINT NOT NULL,
			is_incoming BOOLEAN NOT NULL,
			confirmations INTEGER NOT NULL DEFAULT 0,
			status VARCHAR(10) NOT NULL DEFAULT 'pending',
			sender_address VARCHAR(34),
			receiver_address VARCHAR(34),
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(address_id, tx_id)
		)
	`)
	if err != nil {
		return err
	}

	// Create unspent_outputs table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS unspent_outputs (
			id SERIAL PRIMARY KEY,
			address_id INTEGER NOT NULL REFERENCES tracked_addresses(id),
			tx_id VARCHAR(64) NOT NULL,
			vout INTEGER NOT NULL,
			amount DECIMAL(20,8) NOT NULL,
			script TEXT NOT NULL,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(address_id, tx_id, vout)
		)
	`)
	if err != nil {
		return err
	}

	// Create indexes
	_, err = db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_transactions_address_id ON transactions(address_id);
		CREATE INDEX IF NOT EXISTS idx_transactions_tx_id ON transactions(tx_id);
		CREATE INDEX IF NOT EXISTS idx_unspent_outputs_address_id ON unspent_outputs(address_id);
		CREATE INDEX IF NOT EXISTS idx_unspent_outputs_tx_id ON unspent_outputs(tx_id);
		CREATE INDEX IF NOT EXISTS idx_blocks_height ON blocks(height);
		CREATE INDEX IF NOT EXISTS idx_headers_height ON headers(height);
	`)

	if err != nil {
		return err
	}

	// Create last_processed_block table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS last_processed_block (
			id INTEGER PRIMARY KEY DEFAULT 1,
			block_hash VARCHAR(64) NOT NULL,
			block_height BIGINT NOT NULL,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return err
	}

	// Insert default row if not exists
	_, err = db.Exec(`
		INSERT INTO last_processed_block (id, block_hash, block_height)
		VALUES (1, '0e0bd6be24f5f426a505694bf46f60301a3a08dfdfda13854fdfe0ce7d455d6f', 0)
		ON CONFLICT (id) DO NOTHING
	`)

	return err
}
