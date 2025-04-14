package mempool

import (
	"context"
	"database/sql"
	"log"
	"sync"
	"time"

	"github.com/dogeorg/dogetracker/pkg/spec"
	"github.com/dogeorg/dogetracker/server/db"
)

// Transaction represents a transaction in the mempool
type Transaction struct {
	TxID        string
	BlockHash   string
	BlockHeight int64
	Amount      float64
	IsIncoming  bool
	Address     string
}

// Block represents a mined block
type Block struct {
	Hash   string
	Height int64
}

// MempoolTracker tracks transactions in the mempool
type MempoolTracker struct {
	mu           sync.RWMutex
	transactions map[string]*Transaction
	addresses    map[string]bool
	db           *sql.DB
	client       spec.Blockchain
}

// NewMempoolTracker creates a new mempool tracker
func NewMempoolTracker(db *sql.DB, client spec.Blockchain) *MempoolTracker {
	return &MempoolTracker{
		transactions: make(map[string]*Transaction),
		addresses:    make(map[string]bool),
		db:           db,
		client:       client,
	}
}

// Start begins tracking the mempool
func (mt *MempoolTracker) Start(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Get mempool transactions
			txIDs, err := mt.client.GetMempoolTransactions()
			if err != nil {
				log.Printf("Error getting mempool transactions: %v", err)
				continue
			}

			// Process each transaction
			for _, txID := range txIDs {
				tx, err := mt.client.GetMempoolTransaction(txID)
				if err != nil {
					log.Printf("Error getting transaction %s: %v", txID, err)
					continue
				}

				// Convert to our transaction type
				mempoolTx := &Transaction{
					TxID:        txID,
					BlockHash:   "",
					BlockHeight: 0,
					Amount:      tx["amount"].(float64),
					IsIncoming:  tx["is_incoming"].(bool),
					Address:     tx["address"].(string),
				}

				// Add to mempool if address is being tracked
				if mt.IsAddressTracked(mempoolTx.Address) {
					mt.AddTransaction(mempoolTx)
				}
			}
		}
	}
}

// AddAddress adds an address to track
func (mt *MempoolTracker) AddAddress(address string) {
	mt.mu.Lock()
	defer mt.mu.Unlock()
	mt.addresses[address] = true
}

// RemoveAddress removes an address from tracking
func (mt *MempoolTracker) RemoveAddress(address string) {
	mt.mu.Lock()
	defer mt.mu.Unlock()
	delete(mt.addresses, address)
}

// IsAddressTracked checks if an address is being tracked
func (mt *MempoolTracker) IsAddressTracked(address string) bool {
	mt.mu.RLock()
	defer mt.mu.RUnlock()
	return mt.addresses[address]
}

// AddTransaction adds a transaction to the mempool and updates the address balance
func (mt *MempoolTracker) AddTransaction(tx *Transaction) {
	mt.mu.Lock()
	defer mt.mu.Unlock()

	// Only track if address is being monitored
	if !mt.addresses[tx.Address] {
		return
	}

	mt.transactions[tx.TxID] = tx

	// Convert to DB transaction and update balance
	dbTx := mt.convertToDBTransaction(tx)
	if dbTx == nil {
		return
	}

	err := db.UpdateAddressBalanceWithTransaction(mt.db, tx.Address, dbTx)
	if err != nil {
		log.Printf("Error updating address balance: %v", err)
	}
}

// RemoveTransaction removes a transaction from the mempool
func (mt *MempoolTracker) RemoveTransaction(txID string) {
	mt.mu.Lock()
	defer mt.mu.Unlock()
	delete(mt.transactions, txID)
}

// GetTransaction returns a transaction by ID
func (mt *MempoolTracker) GetTransaction(txID string) *Transaction {
	mt.mu.RLock()
	defer mt.mu.RUnlock()
	return mt.transactions[txID]
}

// GetMempoolTransactions returns all transactions in the mempool
func (mt *MempoolTracker) GetMempoolTransactions() []*Transaction {
	mt.mu.RLock()
	defer mt.mu.RUnlock()
	txs := make([]*Transaction, 0, len(mt.transactions))
	for _, tx := range mt.transactions {
		txs = append(txs, tx)
	}
	return txs
}

// HandleBlock handles a new block being mined
func (mt *MempoolTracker) HandleBlock(block *Block) {
	mt.mu.Lock()
	defer mt.mu.Unlock()

	// Remove transactions that were included in this block
	for txID, tx := range mt.transactions {
		if tx.BlockHash == block.Hash {
			delete(mt.transactions, txID)
		}
	}
}

// convertToDBTransaction converts a mempool transaction to a database transaction
func (mt *MempoolTracker) convertToDBTransaction(tx *Transaction) *db.Transaction {
	// Get or create the address
	addr, err := db.GetOrCreateAddress(mt.db, tx.Address)
	if err != nil {
		log.Printf("Error getting address: %v", err)
		return nil
	}

	return &db.Transaction{
		AddressID:     addr.ID,
		TxID:          tx.TxID,
		BlockHash:     tx.BlockHash,
		BlockHeight:   tx.BlockHeight,
		Amount:        tx.Amount,
		IsIncoming:    tx.IsIncoming,
		Confirmations: 0,
		Status:        "pending",
		CreatedAt:     time.Now(),
	}
}
