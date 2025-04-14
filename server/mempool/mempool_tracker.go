package mempool

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	"github.com/dogeorg/dogetracker/pkg/spec"
	"github.com/dogeorg/dogetracker/server/db"
)

// MempoolTracker tracks transactions in the mempool
type MempoolTracker struct {
	client           spec.Blockchain
	db               *sql.DB
	trackedAddresses map[string]bool
	processedTxs     map[string]bool
}

// TrackedAddress represents a tracked address in the database
type TrackedAddress struct {
	ID                    int64
	Address               string
	Balance               float64
	LastUpdated           time.Time
	RequiredConfirmations int
	CreatedAt             time.Time
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
		processedTxs:     make(map[string]bool),
	}
}

// AddTrackedAddress adds an address to the list of tracked addresses
func (t *MempoolTracker) AddTrackedAddress(address string) {
	t.trackedAddresses[address] = true
}

// IsAddressTracked checks if an address is being tracked
func (t *MempoolTracker) IsAddressTracked(address string) bool {
	return t.trackedAddresses[address]
}

// isTransactionProcessed checks if a transaction has already been processed
func (t *MempoolTracker) isTransactionProcessed(txID string) bool {
	return t.processedTxs[txID]
}

// Start starts the mempool tracker
func (t *MempoolTracker) Start() error {
	log.Printf("Starting mempool tracker")

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

	// Start mempool monitoring
	go t.monitorMempool()

	return nil
}

// monitorMempool monitors the mempool for new transactions
func (t *MempoolTracker) monitorMempool() {
	for {
		// Get mempool transactions
		txIDs, err := t.client.GetRawMempool()
		if err != nil {
			time.Sleep(10 * time.Second)
			continue
		}

		// Process each transaction in the mempool
		for _, txID := range txIDs {
			// Skip if transaction is already processed
			if t.isTransactionProcessed(txID) {
				continue
			}

			// Get transaction details
			txHex, err := t.client.GetRawTransaction(txID)
			if err != nil {
				continue
			}

			// Decode transaction
			tx, err := t.client.DecodeRawTransaction(txHex)
			if err != nil {
				continue
			}

			// Track all addresses involved in the transaction
			spentOutputs := make(map[string]map[int]bool) // Map of txID to vout indices that are spent

			// Process inputs first to get from_addresses and track spent outputs
			for _, input := range tx.Vin {
				if input.TxID == "" { // Skip coinbase inputs
					continue
				}

				// Track this output as spent
				if spentOutputs[input.TxID] == nil {
					spentOutputs[input.TxID] = make(map[int]bool)
				}
				spentOutputs[input.TxID][int(input.Vout)] = true

				// Get the previous transaction output
				prevTxHex, err := t.client.GetRawTransaction(input.TxID)
				if err != nil {
					continue
				}

				prevTx, err := t.client.DecodeRawTransaction(prevTxHex)
				if err != nil {
					continue
				}

				// Get the address from the previous output
				prevOutput := prevTx.Vout[input.Vout]
				if len(prevOutput.ScriptPubKey.Addresses) == 0 {
					continue
				}
				address := prevOutput.ScriptPubKey.Addresses[0]

				// Check if address is tracked
				if t.IsAddressTracked(address) {
					// Get or create address
					addr, err := db.GetOrCreateAddress(t.db, address, 6)
					if err != nil {
						continue
					}

					// Create outgoing transaction with 0 confirmations
					tx := &db.Transaction{
						AddressID:     addr.ID,
						TxID:          txID,
						Amount:        -prevOutput.Value, // Negative for outgoing
						BlockHash:     "",                // Empty for mempool transactions
						BlockHeight:   0,                 // 0 for mempool transactions
						IsIncoming:    false,
						Confirmations: 0,
						FromAddress:   address,
						ToAddress:     "", // Will be set when processing outputs
						Status:        "pending",
					}
					if err := db.AddTransaction(t.db, tx); err != nil {
						continue
					}

					// Remove the spent output from unspent_outputs
					if err := db.RemoveUnspentOutput(t.db, addr.ID, input.TxID, int(input.Vout)); err != nil {
						continue
					}
				}
			}

			// Process outputs to get to_addresses and add unspent outputs
			for i, output := range tx.Vout {
				if len(output.ScriptPubKey.Addresses) == 0 {
					continue
				}
				address := output.ScriptPubKey.Addresses[0]

				// Check if address is tracked
				if t.IsAddressTracked(address) {
					// Get or create address
					addr, err := db.GetOrCreateAddress(t.db, address, 6)
					if err != nil {
						continue
					}

					// Find the from_address for this output
					var fromAddr string
					for _, input := range tx.Vin {
						if input.TxID != "" {
							prevTxHex, err := t.client.GetRawTransaction(input.TxID)
							if err != nil {
								continue
							}
							prevTx, err := t.client.DecodeRawTransaction(prevTxHex)
							if err != nil {
								continue
							}
							if input.Vout < uint32(len(prevTx.Vout)) {
								prevOut := prevTx.Vout[input.Vout]
								if len(prevOut.ScriptPubKey.Addresses) > 0 {
									fromAddr = prevOut.ScriptPubKey.Addresses[0]
									break
								}
							}
						}
					}

					// Create incoming transaction with 0 confirmations
					tx := &db.Transaction{
						AddressID:     addr.ID,
						TxID:          txID,
						Amount:        output.Value, // Positive for incoming
						BlockHash:     "",           // Empty for mempool transactions
						BlockHeight:   0,            // 0 for mempool transactions
						IsIncoming:    true,
						Confirmations: 0,
						FromAddress:   fromAddr,
						ToAddress:     address,
						Status:        "pending",
					}
					if err := db.AddTransaction(t.db, tx); err != nil {
						continue
					}

					// Check if this output is being spent in the same transaction
					if spentOutputs[txID] != nil && spentOutputs[txID][i] {
						continue // Skip if output is being spent
					}

					// Add unspent output
					output := &db.UnspentOutput{
						AddressID: addr.ID,
						TxID:      txID,
						Vout:      i,
						Amount:    output.Value,
						Script:    output.ScriptPubKey.Hex,
					}
					if err := db.AddUnspentOutput(t.db, output); err != nil {
						continue
					}
				}
			}

			// Mark transaction as processed
			t.processedTxs[txID] = true
		}

		time.Sleep(10 * time.Second)
	}
}
