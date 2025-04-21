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
			from_address VARCHAR(34),
			to_address VARCHAR(34),
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

// GetTrackedAddresses returns all addresses being tracked
func (db *DB) GetTrackedAddresses() ([]string, error) {
	rows, err := db.Query("SELECT address FROM addresses")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var addresses []string
	for rows.Next() {
		var addr string
		if err := rows.Scan(&addr); err != nil {
			return nil, err
		}
		addresses = append(addresses, addr)
	}
	return addresses, nil
}

// InsertTransaction inserts a new transaction into the database
func (db *DB) InsertTransaction(txHash, address string, amount float64, height int64, fromAddress, toAddress string) error {
	// First get the address_id
	var addressID int64
	err := db.QueryRow("SELECT id FROM addresses WHERE address = $1", address).Scan(&addressID)
	if err != nil {
		return fmt.Errorf("error getting address ID: %v", err)
	}

	// Insert the transaction
	_, err = db.Exec(`
		INSERT INTO transactions (tx_hash, address_id, amount, block_height, confirmations, from_address, to_address, created_at)
		VALUES ($1, $2, $3, $4, 1, $5, $6, NOW())
		ON CONFLICT (address_id, tx_hash) DO UPDATE
		SET amount = $3,
			block_height = $4,
			from_address = $5,
			to_address = $6,
			updated_at = NOW()
	`, txHash, addressID, amount, height, fromAddress, toAddress)
	return err
}

// MarkTransactionSpent marks a transaction as spent in the database
func (db *DB) MarkTransactionSpent(txHash string) error {
	_, err := db.Exec(`
		DELETE FROM unspent_transactions
		WHERE tx_hash = $1
	`, txHash)
	return err
}

// InsertUnspentTransaction inserts a new unspent transaction
func (db *DB) InsertUnspentTransaction(txHash, address string, amount float64, height int64) error {
	// First get the address_id
	var addressID int64
	err := db.QueryRow("SELECT id FROM addresses WHERE address = $1", address).Scan(&addressID)
	if err != nil {
		return fmt.Errorf("error getting address ID: %v", err)
	}

	// Insert the unspent transaction
	_, err = db.Exec(`
		INSERT INTO unspent_transactions (tx_hash, address_id, amount, block_height, confirmations, created_at)
		VALUES ($1, $2, $3, $4, 1, NOW())
		ON CONFLICT (address_id, tx_hash) DO NOTHING
	`, txHash, addressID, amount, height)
	return err
}

// GetAddressBalance returns the current balance for an address
func (db *DB) GetAddressBalance(address string) (float64, error) {
	var balance float64
	err := db.QueryRow(`
		SELECT COALESCE(SUM(ut.amount), 0)
		FROM unspent_transactions ut
		JOIN addresses a ON ut.address_id = a.id
		WHERE a.address = $1
	`, address).Scan(&balance)
	return balance, err
}

// UpdateAddressBalance updates the balance for an address
func (db *DB) UpdateAddressBalance(address string, balance float64) error {
	_, err := db.Exec(`
		UPDATE addresses
		SET balance = $1, updated_at = NOW()
		WHERE address = $2
	`, balance, address)
	return err
}
