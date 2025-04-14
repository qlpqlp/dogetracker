package mempool

import (
	"database/sql"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/dogeorg/dogetracker/pkg/spec"
	"github.com/dogeorg/dogetracker/server/db"
)

type MempoolTracker struct {
	client           spec.Blockchain
	db               *sql.DB
	trackedAddresses map[string]bool
}

// TrackedAddress represents a tracked address in the database
type TrackedAddress struct {
	ID                    int64
	Address               string
	Balance               float64
	LastUpdated           time.Time
	RequiredConfirmations int
}

// Transaction represents a transaction in the database
type Transaction struct {
	ID            int64
	TxID          string
	Address       string
	Amount        float64
	BlockHeight   int64
	Confirmations int
	Status        string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// NewMempoolTracker creates a new MempoolTracker
func NewMempoolTracker(client spec.Blockchain, db *sql.DB) *MempoolTracker {
	return &MempoolTracker{
		client:           client,
		db:               db,
		trackedAddresses: make(map[string]bool),
	}
}

func (t *MempoolTracker) IsAddressTracked(address string) bool {
	return t.trackedAddresses[address]
}

func (t *MempoolTracker) AddTrackedAddress(address string) {
	t.trackedAddresses[address] = true
}

func (t *MempoolTracker) RemoveTrackedAddress(address string) {
	delete(t.trackedAddresses, address)
}

func (t *MempoolTracker) processTransaction(txID string, blockHash string, height int64) error {
	// Get raw transaction
	rawTx, err := t.client.GetRawTransaction(txID)
	if err != nil {
		return fmt.Errorf("failed to get raw transaction: %v", err)
	}

	// Get transaction details from RPC
	txDetails, err := t.client.DecodeRawTransaction(rawTx)
	if err != nil {
		return fmt.Errorf("failed to decode transaction: %v", err)
	}

	// Process transaction inputs and outputs
	for _, vin := range txDetails.Vin {
		if vin.TxID != "" { // Skip coinbase
			prevTx, err := t.client.GetRawTransaction(vin.TxID)
			if err != nil {
				log.Printf("Failed to get previous transaction %s: %v", vin.TxID, err)
				continue
			}

			prevTxDetails, err := t.client.DecodeRawTransaction(prevTx)
			if err != nil {
				log.Printf("Failed to decode previous transaction %s: %v", vin.TxID, err)
				continue
			}

			if vin.Vout < uint32(len(prevTxDetails.Vout)) {
				prevOut := prevTxDetails.Vout[vin.Vout]
				if len(prevOut.ScriptPubKey.Addresses) > 0 {
					fromAddr := prevOut.ScriptPubKey.Addresses[0]
					if t.IsAddressTracked(fromAddr) {
						// Get or create address
						addr, err := db.GetOrCreateAddress(t.db, fromAddr)
						if err != nil {
							log.Printf("Failed to get or create address: %v", err)
							continue
						}

						// Create outgoing transaction
						tx := &db.Transaction{
							AddressID:     addr.ID,
							TxID:          txID,
							Amount:        -prevOut.Value, // Negative for outgoing
							BlockHash:     blockHash,
							BlockHeight:   height,
							IsIncoming:    false,
							Confirmations: 1,
							FromAddress:   fromAddr,
							ToAddress:     "", // Will be set when processing outputs
						}
						if err := db.AddTransaction(t.db, tx); err != nil {
							log.Printf("Failed to create outgoing transaction: %v", err)
						}
					}
				}
			}
		}
	}

	// Process outputs to get to_address
	for _, vout := range txDetails.Vout {
		if len(vout.ScriptPubKey.Addresses) > 0 {
			toAddr := vout.ScriptPubKey.Addresses[0]
			if t.IsAddressTracked(toAddr) {
				// Get or create address
				addr, err := db.GetOrCreateAddress(t.db, toAddr)
				if err != nil {
					log.Printf("Failed to get or create address: %v", err)
					continue
				}

				// Create incoming transaction
				tx := &db.Transaction{
					AddressID:     addr.ID,
					TxID:          txID,
					Amount:        vout.Value, // Positive for incoming
					BlockHash:     blockHash,
					BlockHeight:   height,
					IsIncoming:    true,
					Confirmations: 1,
					FromAddress:   "", // Will be set when processing inputs
					ToAddress:     toAddr,
				}
				if err := db.AddTransaction(t.db, tx); err != nil {
					log.Printf("Failed to create incoming transaction: %v", err)
				}
			}
		}
	}

	return nil
}

// Start starts the mempool tracker
func (t *MempoolTracker) Start(startBlock string) error {
	log.Printf("Starting mempool tracker from block %s", startBlock)

	// Load tracked addresses from database
	rows, err := t.db.Query("SELECT address FROM tracked_addresses")
	if err != nil {
		return fmt.Errorf("failed to load tracked addresses: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var address string
		if err := rows.Scan(&address); err != nil {
			log.Printf("Failed to scan tracked address: %v", err)
			continue
		}
		t.AddTrackedAddress(address)
		log.Printf("Loaded tracked address: %s", address)
	}

	// Parse start block height
	startHeight, err := strconv.ParseInt(startBlock, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid start block height: %v", err)
	}

	// Get current block height
	currentHeight, err := t.client.GetBlockCount()
	if err != nil {
		return fmt.Errorf("failed to get current block height: %v", err)
	}
	log.Printf("Current block height: %d", currentHeight)

	// Process blocks from start height to current height
	for height := startHeight; height <= currentHeight; height++ {
		log.Printf("Processing block %d", height)

		// Get block hash
		blockHash, err := t.client.GetBlockHash(height)
		if err != nil {
			log.Printf("Failed to get block hash for height %d: %v", height, err)
			continue
		}

		// Get block details
		block, err := t.client.GetBlockVerbose(blockHash)
		if err != nil {
			log.Printf("Failed to get block %s: %v", blockHash, err)
			continue
		}

		// Process each transaction in the block
		for _, tx := range block.Tx {
			log.Printf("Processing transaction %s", tx.TxID)

			// Process transaction
			if err := t.processTransaction(tx.TxID, blockHash, height); err != nil {
				log.Printf("Failed to process transaction %s: %v", tx.TxID, err)
				continue
			}
		}

		// Store the last processed block
		_, err = t.db.Exec(`
			INSERT INTO last_processed_block (block_height, block_hash)
			VALUES ($1, $2)
			ON CONFLICT (id) DO UPDATE
			SET block_height = $1, block_hash = $2, processed_at = CURRENT_TIMESTAMP
		`, height, blockHash)
		if err != nil {
			log.Printf("Failed to store last processed block: %v", err)
		}
	}

	// Start monitoring mempool in a separate goroutine
	go t.monitorMempool()

	return nil
}

// monitorMempool monitors the mempool for new transactions
func (t *MempoolTracker) monitorMempool() {
	for {
		// Get mempool transactions
		txIDs, err := t.client.GetRawMempool()
		if err != nil {
			log.Printf("Failed to get mempool transactions: %v", err)
			time.Sleep(10 * time.Second)
			continue
		}

		// Process each transaction in the mempool
		for _, txID := range txIDs {
			// Get transaction details
			txHex, err := t.client.GetRawTransaction(txID)
			if err != nil {
				log.Printf("Failed to get transaction %s: %v", txID, err)
				continue
			}

			// Decode transaction
			tx, err := t.client.DecodeRawTransaction(txHex)
			if err != nil {
				log.Printf("Failed to decode transaction %s: %v", txID, err)
				continue
			}

			// Process transaction inputs (outgoing transactions)
			for _, input := range tx.Vin {
				if input.TxID == "" { // Skip coinbase inputs
					continue
				}

				// Get the previous transaction output
				prevTxHex, err := t.client.GetRawTransaction(input.TxID)
				if err != nil {
					log.Printf("Failed to get previous transaction %s: %v", input.TxID, err)
					continue
				}

				prevTx, err := t.client.DecodeRawTransaction(prevTxHex)
				if err != nil {
					log.Printf("Failed to decode previous transaction %s: %v", input.TxID, err)
					continue
				}

				// Get the address from the previous output
				prevOutput := prevTx.Vout[input.Vout]
				if len(prevOutput.ScriptPubKey.Addresses) == 0 {
					continue
				}
				address := prevOutput.ScriptPubKey.Addresses[0]

				// Check if address is tracked
				var trackedAddress TrackedAddress
				err = t.db.QueryRow("SELECT id, address, balance, required_confirmations FROM tracked_addresses WHERE address = $1", address).Scan(
					&trackedAddress.ID, &trackedAddress.Address, &trackedAddress.Balance, &trackedAddress.RequiredConfirmations)
				if err != nil {
					if err == sql.ErrNoRows {
						continue
					}
					log.Printf("Failed to query tracked address %s: %v", address, err)
					continue
				}

				// Create outgoing transaction with 0 confirmations
				amount := -prevOutput.Value
				_, err = t.db.Exec(`
					INSERT INTO transactions (tx_id, address, amount, block_height, confirmations, status)
					VALUES ($1, $2, $3, NULL, 0, 'confirmed')
					ON CONFLICT (tx_id, address) DO NOTHING
				`, txID, address, amount)
				if err != nil {
					log.Printf("Failed to insert transaction %s: %v", txID, err)
					continue
				}

				// Update address balance
				_, err = t.db.Exec(`
					UPDATE tracked_addresses
					SET balance = balance + $1,
						last_updated = CURRENT_TIMESTAMP
					WHERE address = $2
				`, amount, address)
				if err != nil {
					log.Printf("Failed to update address balance %s: %v", address, err)
					continue
				}
			}

			// Process transaction outputs (incoming transactions)
			for _, output := range tx.Vout {
				if len(output.ScriptPubKey.Addresses) == 0 {
					continue
				}
				address := output.ScriptPubKey.Addresses[0]

				// Check if address is tracked
				var trackedAddress TrackedAddress
				err = t.db.QueryRow("SELECT id, address, balance, required_confirmations FROM tracked_addresses WHERE address = $1", address).Scan(
					&trackedAddress.ID, &trackedAddress.Address, &trackedAddress.Balance, &trackedAddress.RequiredConfirmations)
				if err != nil {
					if err == sql.ErrNoRows {
						continue
					}
					log.Printf("Failed to query tracked address %s: %v", address, err)
					continue
				}

				// Create incoming transaction with 0 confirmations
				amount := output.Value
				_, err = t.db.Exec(`
					INSERT INTO transactions (tx_id, address, amount, block_height, confirmations, status)
					VALUES ($1, $2, $3, NULL, 0, 'confirmed')
					ON CONFLICT (tx_id, address) DO NOTHING
				`, txID, address, amount)
				if err != nil {
					log.Printf("Failed to insert transaction %s: %v", txID, err)
					continue
				}

				// Update address balance
				_, err = t.db.Exec(`
					UPDATE tracked_addresses
					SET balance = balance + $1,
						last_updated = CURRENT_TIMESTAMP
					WHERE address = $2
				`, amount, address)
				if err != nil {
					log.Printf("Failed to update address balance %s: %v", address, err)
					continue
				}
			}
		}

		time.Sleep(10 * time.Second)
	}
}

func (t *MempoolTracker) isTransactionProcessed(txID string) bool {
	// Check if transaction is already in the database
	var count int
	err := t.db.QueryRow("SELECT COUNT(*) FROM transactions WHERE tx_id = $1", txID).Scan(&count)
	if err != nil {
		log.Printf("Failed to check if transaction is processed: %v", err)
		return false
	}
	return count > 0
}

func (t *MempoolTracker) updateProcessedTransactions(currentTxMap map[string]bool) {
	// Remove transactions that are no longer in the mempool
	rows, err := t.db.Query("SELECT tx_id FROM transactions WHERE block_hash = ''")
	if err != nil {
		log.Printf("Failed to get mempool transactions: %v", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var txID string
		if err := rows.Scan(&txID); err != nil {
			log.Printf("Failed to scan transaction ID: %v", err)
			continue
		}
		if !currentTxMap[txID] {
			// Transaction is no longer in mempool, update its status
			_, err := t.db.Exec("UPDATE transactions SET block_hash = NULL, block_height = NULL WHERE tx_id = $1", txID)
			if err != nil {
				log.Printf("Failed to update transaction status: %v", err)
			}
		}
	}
}
