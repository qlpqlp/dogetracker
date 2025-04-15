package database

import (
	"database/sql"
	"fmt"
	"log"

	_ "github.com/lib/pq"
)

type DB struct {
	*sql.DB
}

func NewDB(host string, port int, user, password, dbname string) (*DB, error) {
	connStr := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		host, port, user, password, dbname)

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("error opening database: %v", err)
	}

	if err = db.Ping(); err != nil {
		return nil, fmt.Errorf("error connecting to database: %v", err)
	}

	return &DB{db}, nil
}

func (db *DB) InitSchema() error {
	// Create addresses table
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS addresses (
			id SERIAL PRIMARY KEY,
			address VARCHAR(34) NOT NULL UNIQUE,
			balance DECIMAL(20,8) NOT NULL DEFAULT 0,
			required_confirmations INTEGER NOT NULL DEFAULT 1,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW()
		)
	`)
	if err != nil {
		return fmt.Errorf("error creating addresses table: %v", err)
	}

	// Create transactions table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS transactions (
			id SERIAL PRIMARY KEY,
			address_id INTEGER NOT NULL REFERENCES addresses(id),
			tx_hash VARCHAR(64) NOT NULL,
			amount DECIMAL(20,8) NOT NULL,
			block_height INTEGER NOT NULL,
			confirmations INTEGER NOT NULL DEFAULT 0,
			is_spent BOOLEAN NOT NULL DEFAULT FALSE,
			is_confirmed BOOLEAN NOT NULL DEFAULT FALSE,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
			UNIQUE(address_id, tx_hash)
		)
	`)
	if err != nil {
		return fmt.Errorf("error creating transactions table: %v", err)
	}

	// Create unspent_transactions table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS unspent_transactions (
			id SERIAL PRIMARY KEY,
			address_id INTEGER NOT NULL REFERENCES addresses(id),
			tx_hash VARCHAR(64) NOT NULL,
			amount DECIMAL(20,8) NOT NULL,
			block_height INTEGER NOT NULL,
			confirmations INTEGER NOT NULL DEFAULT 0,
			is_confirmed BOOLEAN NOT NULL DEFAULT FALSE,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
			UNIQUE(address_id, tx_hash)
		)
	`)
	if err != nil {
		return fmt.Errorf("error creating unspent_transactions table: %v", err)
	}

	// Create processed_blocks table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS processed_blocks (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			height INTEGER NOT NULL,
			hash VARCHAR(64) NOT NULL,
			processed_at TIMESTAMP NOT NULL DEFAULT NOW()
		)
	`)
	if err != nil {
		return fmt.Errorf("error creating processed_blocks table: %v", err)
	}

	// No need for the trigger anymore since we're using a single row with id=1
	log.Println("Database schema initialized successfully")
	return nil
}

// GetLastProcessedBlock returns the latest processed block
func (db *DB) GetLastProcessedBlock() (*ProcessedBlock, error) {
	var block ProcessedBlock
	err := db.QueryRow(`
		SELECT id, height, hash, processed_at
		FROM processed_blocks
		WHERE id = 1
	`).Scan(&block.ID, &block.Height, &block.Hash, &block.ProcessedAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("error getting last processed block: %v", err)
	}
	return &block, nil
}

// SaveProcessedBlock saves/updates the processed block
func (db *DB) SaveProcessedBlock(height int64, hash string) error {
	_, err := db.Exec(`
		INSERT INTO processed_blocks (id, height, hash)
		VALUES (1, $1, $2)
		ON CONFLICT (id) DO UPDATE
		SET height = $1,
			hash = $2,
			processed_at = CURRENT_TIMESTAMP
	`, height, hash)
	if err != nil {
		return fmt.Errorf("error saving processed block: %v", err)
	}
	return nil
}
