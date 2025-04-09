package db

import (
	"database/sql"
)

// DB operations for tracked addresses
func GetOrCreateAddress(db *sql.DB, address string) (*TrackedAddress, error) {
	return GetOrCreateAddressWithConfirmations(db, address, 1) // Default to 1 confirmation
}

func GetOrCreateAddressWithConfirmations(db *sql.DB, address string, requiredConfirmations int) (*TrackedAddress, error) {
	var addr TrackedAddress

	// Try to get existing address
	err := db.QueryRow(`
		SELECT id, address, balance, required_confirmations, created_at, updated_at 
		FROM tracked_addresses 
		WHERE address = $1
	`, address).Scan(&addr.ID, &addr.Address, &addr.Balance, &addr.RequiredConfirmations, &addr.CreatedAt, &addr.UpdatedAt)

	if err == sql.ErrNoRows {
		// Create new address
		err = db.QueryRow(`
			INSERT INTO tracked_addresses (address, balance, required_confirmations) 
			VALUES ($1, 0, $2) 
			RETURNING id, address, balance, required_confirmations, created_at, updated_at
		`, address, requiredConfirmations).Scan(&addr.ID, &addr.Address, &addr.Balance, &addr.RequiredConfirmations, &addr.CreatedAt, &addr.UpdatedAt)
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
		SELECT id, address_id, tx_id, block_hash, block_height, amount, is_incoming, confirmations, status, created_at
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
			&tx.Amount, &tx.IsIncoming, &tx.Confirmations, &tx.Status, &tx.CreatedAt)
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
func UpdateAddressBalance(db *sql.DB, addressID int64, balance float64) error {
	_, err := db.Exec(`
		UPDATE tracked_addresses 
		SET balance = $1, updated_at = CURRENT_TIMESTAMP 
		WHERE id = $2
	`, balance, addressID)
	return err
}

// Add transaction
func AddTransaction(db *sql.DB, tx *Transaction) error {
	_, err := db.Exec(`
		INSERT INTO transactions (address_id, tx_id, block_hash, block_height, amount, fee, timestamp, is_incoming, confirmations, status, sender_address, receiver_address)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (address_id, tx_id) DO UPDATE 
		SET block_hash = $3, block_height = $4, amount = $5, fee = $6, timestamp = $7, is_incoming = $8, confirmations = $9, status = $10, sender_address = $11, receiver_address = $12
	`, tx.AddressID, tx.TxID, tx.BlockHash, tx.BlockHeight, tx.Amount, tx.Fee, tx.Timestamp, tx.IsIncoming, tx.Confirmations, tx.Status, tx.SenderAddress, tx.ReceiverAddress)
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

// GetOrCreateAddressWithDetails gets or creates an address and returns all its details
func GetOrCreateAddressWithDetails(db *sql.DB, address string, requiredConfirmations int) (*TrackedAddress, []Transaction, []UnspentOutput, error) {
	addr, err := GetOrCreateAddressWithConfirmations(db, address, requiredConfirmations)
	if err != nil {
		return nil, nil, nil, err
	}

	// Get transactions
	rows, err := db.Query(`
		SELECT id, address_id, tx_id, block_hash, block_height, amount, is_incoming, confirmations, status, created_at
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
			&tx.Amount, &tx.IsIncoming, &tx.Confirmations, &tx.Status, &tx.CreatedAt)
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
