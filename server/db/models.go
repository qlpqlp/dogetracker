package db

import (
	"database/sql"
	"fmt"
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
	// Drop existing headers table if it exists
	_, err := db.Exec(`DROP TABLE IF EXISTS headers CASCADE`)
	if err != nil {
		return fmt.Errorf("error dropping headers table: %v", err)
	}

	// Create blocks table
	_, err = db.Exec(`
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

	// Create headers table with BYTEA column
	_, err = db.Exec(`
		CREATE TABLE headers (
			hash VARCHAR(64) PRIMARY KEY,
			height BIGINT NOT NULL,
			header BYTEA NOT NULL,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return fmt.Errorf("error creating headers table: %v", err)
	}

	// Insert genesis block into headers table
	genesisHeader := []byte{
		0x01, 0x00, 0x00, 0x00, // Version
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, // Prev block
		0x1a, 0x91, 0xe3, 0xda, 0xce, 0x36, 0xe2, 0xbe, 0x3b, 0xf0, 0x30, 0xa6, 0x56, 0x79, 0xfe, 0x82,
		0x1a, 0xa1, 0xd6, 0xef, 0x92, 0xe7, 0xc9, 0x90, 0x2e, 0xb3, 0x18, 0x18, 0x2c, 0x35, 0x56, 0x91, // Merkle root
		0x24, 0x8d, 0x52, 0x52, // Time
		0xff, 0xff, 0x0f, 0x1e, // Bits
		0x67, 0x86, 0x01, 0x00, // Nonce
	}

	_, err = db.Exec(`
		INSERT INTO headers (hash, height, header)
		VALUES (
			'1a91e3dace36e2be3bf030a65679fe821aa1d6ef92e7c9902eb318182c355691',
			0,
			$1
		) ON CONFLICT (hash) DO NOTHING
	`, genesisHeader)
	if err != nil {
		return fmt.Errorf("error inserting genesis block: %v", err)
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
		VALUES (1, '1a91e3dace36e2be3bf030a65679fe821aa1d6ef92e7c9902eb318182c355691', 0)
		ON CONFLICT (id) DO NOTHING
	`)

	return err
}

// StoreBlockHeader stores a block header in the database
func StoreBlockHeader(db *sql.DB, hash string, height int64, header []byte) error {
	_, err := db.Exec(`
		INSERT INTO headers (hash, height, header)
		VALUES ($1, $2, $3)
		ON CONFLICT (hash) DO UPDATE
		SET height = $2, header = $3
	`, hash, height, header)
	return err
}

// GetBlockHeader retrieves a block header from the database
func GetBlockHeader(db *sql.DB, hash string) ([]byte, error) {
	var header []byte
	err := db.QueryRow(`
		SELECT header FROM headers WHERE hash = $1
	`, hash).Scan(&header)
	return header, err
}
