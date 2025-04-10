package doge

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
)

// BlockDatabase interface for storing blocks and transactions
type BlockDatabase interface {
	StoreBlock(block *Block) error
	StoreTransaction(tx *Transaction, blockHash string, blockHeight uint32) error
	GetBlock(hash string) (*Block, error)
	GetTransaction(txid string) (*Transaction, error)
	GetBlockHeight() (uint32, error)
}

// SQLDatabase implements the BlockDatabase interface using SQL
type SQLDatabase struct {
	db *sql.DB
}

// NewSQLDatabase creates a new SQL database implementation
func NewSQLDatabase(db *sql.DB) *SQLDatabase {
	return &SQLDatabase{db: db}
}

// StoreBlock stores a block in the database
func (d *SQLDatabase) StoreBlock(block *Block) error {
	// Calculate block hash
	headerBytes := block.Header.Serialize()
	hash1 := sha256.Sum256(headerBytes)
	hash2 := sha256.Sum256(hash1[:])
	blockHash := hex.EncodeToString(hash2[:])

	// Start transaction
	tx, err := d.db.Begin()
	if err != nil {
		return fmt.Errorf("error starting transaction: %v", err)
	}
	defer tx.Rollback()

	// Store block header
	_, err = tx.Exec(`
		INSERT INTO blocks (hash, height, version, prev_block, merkle_root, time, bits, nonce)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (hash) DO NOTHING
	`, blockHash, block.Header.Height, block.Header.Version, block.Header.PrevBlock,
		block.Header.MerkleRoot, block.Header.Time, block.Header.Bits, block.Header.Nonce)
	if err != nil {
		return fmt.Errorf("error storing block header: %v", err)
	}

	// Store transactions
	for _, tx := range block.Tx {
		if err := d.StoreTransaction(&tx, blockHash, block.Header.Height); err != nil {
			return fmt.Errorf("error storing transaction: %v", err)
		}
	}

	// Commit transaction
	return tx.Commit()
}

// StoreTransaction stores a transaction in the database
func (d *SQLDatabase) StoreTransaction(tx *Transaction, blockHash string, blockHeight uint32) error {
	// Start transaction
	dbTx, err := d.db.Begin()
	if err != nil {
		return fmt.Errorf("error starting transaction: %v", err)
	}
	defer dbTx.Rollback()

	// Store transaction
	_, err = dbTx.Exec(`
		INSERT INTO transactions (txid, block_hash, block_height, version, lock_time)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (txid) DO NOTHING
	`, tx.TxID, blockHash, blockHeight, tx.Version, tx.LockTime)
	if err != nil {
		return fmt.Errorf("error storing transaction: %v", err)
	}

	// Store inputs
	for i, input := range tx.Inputs {
		_, err = dbTx.Exec(`
			INSERT INTO transaction_inputs (txid, index, prev_txid, prev_index, script_sig, sequence)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, tx.TxID, i, hex.EncodeToString(input.PreviousOutput.Hash[:]),
			input.PreviousOutput.Index, input.ScriptSig, input.Sequence)
		if err != nil {
			return fmt.Errorf("error storing transaction input: %v", err)
		}
	}

	// Store outputs
	for i, output := range tx.Outputs {
		_, err = dbTx.Exec(`
			INSERT INTO transaction_outputs (txid, index, value, script_pubkey)
			VALUES ($1, $2, $3, $4)
		`, tx.TxID, i, output.Value, output.ScriptPubKey)
		if err != nil {
			return fmt.Errorf("error storing transaction output: %v", err)
		}
	}

	// Commit transaction
	return dbTx.Commit()
}

// GetBlock retrieves a block from the database
func (d *SQLDatabase) GetBlock(hash string) (*Block, error) {
	// Query block header
	var block Block
	var prevBlock, merkleRoot []byte
	err := d.db.QueryRow(`
		SELECT height, version, prev_block, merkle_root, time, bits, nonce
		FROM blocks
		WHERE hash = $1
	`, hash).Scan(&block.Header.Height, &block.Header.Version, &prevBlock,
		&merkleRoot, &block.Header.Time, &block.Header.Bits, &block.Header.Nonce)
	if err != nil {
		return nil, fmt.Errorf("error querying block: %v", err)
	}
	copy(block.Header.PrevBlock[:], prevBlock)
	copy(block.Header.MerkleRoot[:], merkleRoot)

	// Query transactions
	rows, err := d.db.Query(`
		SELECT t.txid, t.version, t.lock_time
		FROM transactions t
		WHERE t.block_hash = $1
		ORDER BY t.id
	`, hash)
	if err != nil {
		return nil, fmt.Errorf("error querying transactions: %v", err)
	}
	defer rows.Close()

	// Process transactions
	for rows.Next() {
		var tx Transaction
		err := rows.Scan(&tx.TxID, &tx.Version, &tx.LockTime)
		if err != nil {
			return nil, fmt.Errorf("error scanning transaction: %v", err)
		}

		// Query inputs
		inputRows, err := d.db.Query(`
			SELECT prev_txid, prev_index, script_sig, sequence
			FROM transaction_inputs
			WHERE txid = $1
			ORDER BY index
		`, tx.TxID)
		if err != nil {
			return nil, fmt.Errorf("error querying inputs: %v", err)
		}
		defer inputRows.Close()

		// Process inputs
		for inputRows.Next() {
			var input TxInput
			var prevTxID string
			err := inputRows.Scan(&prevTxID, &input.PreviousOutput.Index,
				&input.ScriptSig, &input.Sequence)
			if err != nil {
				return nil, fmt.Errorf("error scanning input: %v", err)
			}
			hashBytes, err := hex.DecodeString(prevTxID)
			if err != nil {
				return nil, fmt.Errorf("error decoding prev_txid: %v", err)
			}
			copy(input.PreviousOutput.Hash[:], hashBytes)
			tx.Inputs = append(tx.Inputs, input)
		}

		// Query outputs
		outputRows, err := d.db.Query(`
			SELECT value, script_pubkey
			FROM transaction_outputs
			WHERE txid = $1
			ORDER BY index
		`, tx.TxID)
		if err != nil {
			return nil, fmt.Errorf("error querying outputs: %v", err)
		}
		defer outputRows.Close()

		// Process outputs
		for outputRows.Next() {
			var output TxOutput
			err := outputRows.Scan(&output.Value, &output.ScriptPubKey)
			if err != nil {
				return nil, fmt.Errorf("error scanning output: %v", err)
			}
			tx.Outputs = append(tx.Outputs, output)
		}

		block.Tx = append(block.Tx, tx)
	}

	return &block, nil
}

// GetTransaction retrieves a transaction from the database
func (d *SQLDatabase) GetTransaction(txid string) (*Transaction, error) {
	// Query transaction
	var tx Transaction
	err := d.db.QueryRow(`
		SELECT txid, version, lock_time
		FROM transactions
		WHERE txid = $1
	`, txid).Scan(&tx.TxID, &tx.Version, &tx.LockTime)
	if err != nil {
		return nil, fmt.Errorf("error querying transaction: %v", err)
	}

	// Query inputs
	rows, err := d.db.Query(`
		SELECT prev_txid, prev_index, script_sig, sequence
		FROM transaction_inputs
		WHERE txid = $1
		ORDER BY index
	`, txid)
	if err != nil {
		return nil, fmt.Errorf("error querying inputs: %v", err)
	}
	defer rows.Close()

	// Process inputs
	for rows.Next() {
		var input TxInput
		var prevTxID string
		err := rows.Scan(&prevTxID, &input.PreviousOutput.Index,
			&input.ScriptSig, &input.Sequence)
		if err != nil {
			return nil, fmt.Errorf("error scanning input: %v", err)
		}
		hashBytes, err := hex.DecodeString(prevTxID)
		if err != nil {
			return nil, fmt.Errorf("error decoding prev_txid: %v", err)
		}
		copy(input.PreviousOutput.Hash[:], hashBytes)
		tx.Inputs = append(tx.Inputs, input)
	}

	// Query outputs
	rows, err = d.db.Query(`
		SELECT value, script_pubkey
		FROM transaction_outputs
		WHERE txid = $1
		ORDER BY index
	`, txid)
	if err != nil {
		return nil, fmt.Errorf("error querying outputs: %v", err)
	}
	defer rows.Close()

	// Process outputs
	for rows.Next() {
		var output TxOutput
		err := rows.Scan(&output.Value, &output.ScriptPubKey)
		if err != nil {
			return nil, fmt.Errorf("error scanning output: %v", err)
		}
		tx.Outputs = append(tx.Outputs, output)
	}

	return &tx, nil
}

// GetBlockHeight retrieves the current block height from the database
func (d *SQLDatabase) GetBlockHeight() (uint32, error) {
	var height uint32
	err := d.db.QueryRow(`
		SELECT COALESCE(MAX(height), 0)
		FROM blocks
	`).Scan(&height)
	if err != nil {
		return 0, fmt.Errorf("error getting block height: %v", err)
	}
	return height, nil
}
