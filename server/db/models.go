package db

import (
	"database/sql"
	"fmt"
	"time"
)

// TrackedAddress represents a tracked address in the database
type TrackedAddress struct {
	ID                    int64     `json:"id"`
	Address               string    `json:"address"`
	Balance               float64   `json:"balance"`
	LastUpdated           time.Time `json:"last_updated"`
	RequiredConfirmations int       `json:"required_confirmations"`
	CreatedAt             time.Time `json:"created_at"`
}

// Transaction represents a transaction in the database
type Transaction struct {
	ID            int64     `json:"id"`
	AddressID     int64     `json:"address_id"`
	TxID          string    `json:"tx_id"`
	Amount        float64   `json:"amount"`
	BlockHash     string    `json:"block_hash"`
	BlockHeight   int64     `json:"block_height"`
	IsIncoming    bool      `json:"is_incoming"`
	Confirmations int       `json:"confirmations"`
	FromAddress   string    `json:"from_address"`
	ToAddress     string    `json:"to_address"`
	Status        string    `json:"status"` // 'pending' or 'confirmed'
	CreatedAt     time.Time `json:"created_at"`
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
	// Create tracked_addresses table
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS tracked_addresses (
			id SERIAL PRIMARY KEY,
			address TEXT UNIQUE NOT NULL,
			balance DOUBLE PRECISION DEFAULT 0,
			last_updated TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			required_confirmations INTEGER DEFAULT 6,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create tracked_addresses table: %v", err)
	}

	// Create transactions table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS transactions (
			id SERIAL PRIMARY KEY,
			address_id INTEGER REFERENCES tracked_addresses(id),
			tx_id TEXT NOT NULL,
			amount DOUBLE PRECISION NOT NULL,
			block_hash TEXT,
			block_height BIGINT,
			is_incoming BOOLEAN NOT NULL,
			confirmations INTEGER DEFAULT 0,
			from_address TEXT,
			to_address TEXT,
			status TEXT DEFAULT 'pending',
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(tx_id, address_id)
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create transactions table: %v", err)
	}

	// Create unspent_outputs table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS unspent_outputs (
			id SERIAL PRIMARY KEY,
			address_id INTEGER REFERENCES tracked_addresses(id),
			tx_id TEXT NOT NULL,
			vout INTEGER NOT NULL,
			amount DOUBLE PRECISION NOT NULL,
			script TEXT NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(tx_id, vout)
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create unspent_outputs table: %v", err)
	}

	// Create last_processed_block table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS last_processed_block (
			id INTEGER PRIMARY KEY DEFAULT 1,
			block_height BIGINT NOT NULL,
			block_hash TEXT NOT NULL,
			processed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			CONSTRAINT single_row CHECK (id = 1)
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create last_processed_block table: %v", err)
	}

	// Create indexes
	_, err = db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_transactions_address_id ON transactions(address_id);
		CREATE INDEX IF NOT EXISTS idx_transactions_tx_id ON transactions(tx_id);
		CREATE INDEX IF NOT EXISTS idx_transactions_from_address ON transactions(from_address);
		CREATE INDEX IF NOT EXISTS idx_transactions_to_address ON transactions(to_address);
		CREATE INDEX IF NOT EXISTS idx_unspent_outputs_address_id ON unspent_outputs(address_id);
		CREATE INDEX IF NOT EXISTS idx_unspent_outputs_tx_id ON unspent_outputs(tx_id);
	`)

	return err
}
