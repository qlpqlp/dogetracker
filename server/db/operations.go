package db

import (
	"database/sql"
)

// DBOrTx is an interface that both *sql.DB and *sql.Tx satisfy
type DBOrTx interface {
	Query(query string, args ...interface{}) (*sql.Rows, error)
	QueryRow(query string, args ...interface{}) *sql.Row
	Exec(query string, args ...interface{}) (sql.Result, error)
}

// DB operations for tracked addresses
func GetOrCreateAddress(db DBOrTx, address string) (*TrackedAddress, error) {
	var addr TrackedAddress

	// Try to get existing address
	err := db.QueryRow(`
		SELECT id, address, balance, created_at, updated_at 
		FROM tracked_addresses 
		WHERE address = $1
	`, address).Scan(&addr.ID, &addr.Address, &addr.Balance, &addr.CreatedAt, &addr.UpdatedAt)

	if err == sql.ErrNoRows {
		// Create new address
		err = db.QueryRow(`
			INSERT INTO tracked_addresses (address, balance) 
			VALUES ($1, 0) 
			RETURNING id, address, balance, created_at, updated_at
		`, address).Scan(&addr.ID, &addr.Address, &addr.Balance, &addr.CreatedAt, &addr.UpdatedAt)
		if err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}

	return &addr, nil
}

// Get address balance and details
func GetAddressDetails(db *sql.DB, address string) (*TrackedAddress, []Transaction, []UnspentOutput, error) {
	addr, err := GetOrCreateAddress(db, address)
	if err != nil {
		return nil, nil, nil, err
	}

	// Get transactions
	rows, err := db.Query(`
		SELECT id, address_id, tx_id, block_hash, block_height, amount, is_incoming, confirmations, created_at
		FROM transactions
		WHERE address_id = $1
		ORDER BY created_at DESC
	`, addr.ID)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close()

	var transactions []Transaction
	for rows.Next() {
		var tx Transaction
		err := rows.Scan(&tx.ID, &tx.AddressID, &tx.TxID, &tx.BlockHash, &tx.BlockHeight,
			&tx.Amount, &tx.IsIncoming, &tx.Confirmations, &tx.CreatedAt)
		if err != nil {
			return nil, nil, nil, err
		}
		transactions = append(transactions, tx)
	}

	// Get unspent outputs
	rows, err = db.Query(`
		SELECT id, address_id, tx_id, vout, amount, script, created_at
		FROM unspent_outputs
		WHERE address_id = $1
	`, addr.ID)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close()

	var unspentOutputs []UnspentOutput
	for rows.Next() {
		var output UnspentOutput
		err := rows.Scan(&output.ID, &output.AddressID, &output.TxID, &output.Vout,
			&output.Amount, &output.Script, &output.CreatedAt)
		if err != nil {
			return nil, nil, nil, err
		}
		unspentOutputs = append(unspentOutputs, output)
	}

	return addr, transactions, unspentOutputs, nil
}

// Update address balance
func UpdateAddressBalance(db DBOrTx, addressID int64, balance float64) error {
	_, err := db.Exec(`
		UPDATE tracked_addresses 
		SET balance = $1, updated_at = CURRENT_TIMESTAMP 
		WHERE id = $2
	`, balance, addressID)
	return err
}

// Add transaction
func AddTransaction(db DBOrTx, tx *Transaction) error {
	_, err := db.Exec(`
		INSERT INTO transactions (address_id, tx_id, block_hash, block_height, amount, is_incoming, confirmations)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (address_id, tx_id) DO UPDATE 
		SET block_hash = $3, block_height = $4, confirmations = $7
	`, tx.AddressID, tx.TxID, tx.BlockHash, tx.BlockHeight, tx.Amount, tx.IsIncoming, tx.Confirmations)
	return err
}

// Add unspent output
func AddUnspentOutput(db *sql.DB, output *UnspentOutput) error {
	_, err := db.Exec(`
		INSERT INTO unspent_outputs (address_id, tx_id, vout, amount, script)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (address_id, tx_id, vout) DO UPDATE 
		SET amount = $4, script = $5
	`, output.AddressID, output.TxID, output.Vout, output.Amount, output.Script)
	return err
}

// Remove unspent output (when spent)
func RemoveUnspentOutput(db *sql.DB, addressID int64, txID string, vout int) error {
	_, err := db.Exec(`
		DELETE FROM unspent_outputs 
		WHERE address_id = $1 AND tx_id = $2 AND vout = $3
	`, addressID, txID, vout)
	return err
}

// GetLastProcessedBlock returns the last processed block hash and height
func GetLastProcessedBlock(db *sql.DB) (string, int64, error) {
	var blockHash string
	var blockHeight int64
	err := db.QueryRow(`
		SELECT block_hash, block_height 
		FROM last_processed_block 
		WHERE id = 1
	`).Scan(&blockHash, &blockHeight)
	if err != nil {
		return "", 0, err
	}
	return blockHash, blockHeight, nil
}

// UpdateLastProcessedBlock updates the last processed block hash and height
func UpdateLastProcessedBlock(db *sql.DB, blockHash string, blockHeight int64) error {
	_, err := db.Exec(`
		UPDATE last_processed_block 
		SET block_hash = $1, block_height = $2, updated_at = CURRENT_TIMESTAMP 
		WHERE id = 1
	`, blockHash, blockHeight)
	return err
}

// GetAllTrackedAddresses returns a list of all tracked addresses
func GetAllTrackedAddresses(db *sql.DB) ([]string, error) {
	rows, err := db.Query(`
		SELECT address 
		FROM tracked_addresses 
		ORDER BY created_at DESC
	`)
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

// UpdateAddressBalanceWithTransaction updates the address balance and adds a transaction
func UpdateAddressBalanceWithTransaction(db *sql.DB, address string, tx *Transaction) error {
	// Start a transaction
	txDB, err := db.Begin()
	if err != nil {
		return err
	}
	defer txDB.Rollback() // Rollback if not committed

	// Get or create the address
	addr, err := GetOrCreateAddress(txDB, address)
	if err != nil {
		return err
	}

	// Add the transaction
	tx.AddressID = addr.ID
	if err := AddTransaction(txDB, tx); err != nil {
		return err
	}

	// Calculate new balance
	var newBalance float64
	err = txDB.QueryRow(`
		SELECT COALESCE(SUM(amount), 0)
		FROM transactions
		WHERE address_id = $1
	`, addr.ID).Scan(&newBalance)
	if err != nil {
		return err
	}

	// Update address balance
	if err := UpdateAddressBalance(txDB, addr.ID, newBalance); err != nil {
		return err
	}

	// Commit the transaction
	return txDB.Commit()
}

// RecalculateAddressBalance recalculates the balance for an address based on all transactions
func RecalculateAddressBalance(db *sql.DB, address string) error {
	// Get the address
	addr, err := GetOrCreateAddress(db, address)
	if err != nil {
		return err
	}

	// Calculate new balance
	var newBalance float64
	err = db.QueryRow(`
		SELECT COALESCE(SUM(amount), 0)
		FROM transactions
		WHERE address_id = $1
	`, addr.ID).Scan(&newBalance)
	if err != nil {
		return err
	}

	// Update address balance
	return UpdateAddressBalance(db, addr.ID, newBalance)
}
