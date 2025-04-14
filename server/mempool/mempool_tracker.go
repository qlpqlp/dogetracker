package mempool

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	"github.com/dogeorg/dogetracker/pkg/core"
	"github.com/dogeorg/dogetracker/server/db"
)

type MempoolTracker struct {
	client           *core.CoreRPCClient
	db               *sql.DB
	trackedAddresses map[string]bool
}

func NewMempoolTracker(client *core.CoreRPCClient, db *sql.DB) *MempoolTracker {
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
		if vin.Txid != "" { // Skip coinbase
			prevTx, err := t.client.GetRawTransaction(vin.Txid)
			if err != nil {
				log.Printf("Failed to get previous transaction %s: %v", vin.Txid, err)
				continue
			}

			prevTxDetails, err := t.client.DecodeRawTransaction(prevTx)
			if err != nil {
				log.Printf("Failed to decode previous transaction %s: %v", vin.Txid, err)
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

func (t *MempoolTracker) Start() error {
	// Get initial mempool transactions
	txIDs, err := t.client.GetRawMempool()
	if err != nil {
		return fmt.Errorf("failed to get initial mempool: %v", err)
	}

	// Process initial mempool transactions
	for _, txID := range txIDs {
		if err := t.processTransaction(txID, "", 0); err != nil {
			log.Printf("Failed to process mempool transaction %s: %v", txID, err)
		}
	}

	// Start monitoring for new transactions
	go func() {
		for {
			// Wait for a bit before checking again
			time.Sleep(10 * time.Second)

			// Get current mempool transactions
			currentTxIDs, err := t.client.GetRawMempool()
			if err != nil {
				log.Printf("Failed to get mempool: %v", err)
				continue
			}

			// Create a map of current transactions for quick lookup
			currentTxMap := make(map[string]bool)
			for _, txID := range currentTxIDs {
				currentTxMap[txID] = true
			}

			// Process new transactions
			for _, txID := range currentTxIDs {
				if !t.isTransactionProcessed(txID) {
					if err := t.processTransaction(txID, "", 0); err != nil {
						log.Printf("Failed to process mempool transaction %s: %v", txID, err)
					}
				}
			}

			// Update processed transactions map
			t.updateProcessedTransactions(currentTxMap)
		}
	}()

	return nil
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
