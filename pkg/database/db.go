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
			address VARCHAR(34) UNIQUE NOT NULL,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return fmt.Errorf("error creating addresses table: %v", err)
	}

	// Create transactions table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS transactions (
			id SERIAL PRIMARY KEY,
			tx_hash VARCHAR(64) NOT NULL,
			address_id INTEGER REFERENCES addresses(id),
			amount DECIMAL(20,8) NOT NULL,
			block_height BIGINT,
			confirmations INTEGER DEFAULT 0,
			is_spent BOOLEAN DEFAULT FALSE,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return fmt.Errorf("error creating transactions table: %v", err)
	}

	// Create unspent_transactions table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS unspent_transactions (
			id SERIAL PRIMARY KEY,
			tx_hash VARCHAR(64) NOT NULL,
			address_id INTEGER REFERENCES addresses(id),
			amount DECIMAL(20,8) NOT NULL,
			block_height BIGINT,
			confirmations INTEGER DEFAULT 0,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return fmt.Errorf("error creating unspent_transactions table: %v", err)
	}

	log.Println("Database schema initialized successfully")
	return nil
}
