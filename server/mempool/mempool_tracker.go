package mempool

import (
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"

	"crypto/sha256"

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
	db     *sql.DB
	client spec.Blockchain
	mu     sync.RWMutex
	// Map of address to balance
	balances map[string]float64
	// Map of transaction ID to transaction
	transactions map[string]*Transaction
	// Map of address to list of transaction IDs
	addressTransactions map[string][]string
	// Map of address to whether it's being tracked
	trackedAddresses map[string]bool
	// Channel to stop the tracker
	stopChan chan struct{}
}

// NewMempoolTracker creates a new MempoolTracker
func NewMempoolTracker(db *sql.DB, client spec.Blockchain) *MempoolTracker {
	return &MempoolTracker{
		db:                  db,
		client:              client,
		balances:            make(map[string]float64),
		transactions:        make(map[string]*Transaction),
		addressTransactions: make(map[string][]string),
		trackedAddresses:    make(map[string]bool),
		stopChan:            make(chan struct{}),
	}
}

// Start starts tracking transactions from the specified block height
func (mt *MempoolTracker) Start(startBlock string) error {
	log.Printf("Starting mempool tracker from block %s", startBlock)

	// Convert start block to int
	startHeight, err := strconv.ParseInt(startBlock, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid start block height: %v", err)
	}

	// Get current block height
	currentHeight, err := mt.client.GetBlockCount()
	if err != nil {
		return fmt.Errorf("failed to get current block height: %v", err)
	}
	log.Printf("Current block height: %d", currentHeight)

	// Process blocks from start height to current height
	for height := startHeight; height <= currentHeight; height++ {
		log.Printf("Processing block %d", height)
		blockHash, err := mt.client.GetBlockHash(height)
		if err != nil {
			log.Printf("Failed to get block hash for height %d: %v", height, err)
			continue
		}

		// Get block header to check confirmations
		blockHeader, err := mt.client.GetBlockHeader(blockHash)
		if err != nil {
			log.Printf("Failed to get block header for %s: %v", blockHash, err)
			continue
		}

		// Skip orphaned blocks
		if blockHeader.Confirmations == -1 {
			log.Printf("Block %s is orphaned, skipping", blockHash)
			continue
		}

		// Get block transactions
		blockHex, err := mt.client.GetBlock(blockHash)
		if err != nil {
			log.Printf("Failed to get block %s: %v", blockHash, err)
			continue
		}

		// Parse block hex to get transactions
		txIDs, err := parseBlockTransactions(blockHex)
		if err != nil {
			log.Printf("Failed to parse block transactions for %s: %v", blockHash, err)
			continue
		}
		log.Printf("Found %d transactions in block %d", len(txIDs), height)

		// Process transactions in the block
		for _, txID := range txIDs {
			// Get transaction details
			tx, err := mt.client.GetRawTransaction(txID)
			if err != nil {
				log.Printf("Failed to get transaction %s: %v", txID, err)
				continue
			}

			// Process inputs and outputs
			vins, ok := tx["vin"].([]interface{})
			if !ok {
				log.Printf("Invalid vin format in transaction %s", txID)
				continue
			}

			vouts, ok := tx["vout"].([]interface{})
			if !ok {
				log.Printf("Invalid vout format in transaction %s", txID)
				continue
			}

			// Process outputs (incoming transactions)
			for _, vout := range vouts {
				voutMap, ok := vout.(map[string]interface{})
				if !ok {
					log.Printf("Invalid vout format in transaction %s", txID)
					continue
				}

				scriptPubKey, ok := voutMap["scriptPubKey"].(map[string]interface{})
				if !ok {
					continue
				}

				addresses, ok := scriptPubKey["addresses"].([]interface{})
				if !ok {
					continue
				}

				for _, addr := range addresses {
					addrStr, ok := addr.(string)
					if !ok {
						continue
					}

					amount, ok := voutMap["value"].(float64)
					if !ok {
						continue
					}

					// Create a new transaction
					mempoolTx := &Transaction{
						TxID:        txID,
						BlockHash:   blockHash,
						BlockHeight: height,
						Amount:      amount,
						IsIncoming:  true,
						Address:     addrStr,
					}

					// Get or create address
					addr, err := db.GetOrCreateAddress(mt.db, mempoolTx.Address)
					if err != nil {
						log.Printf("Failed to get or create address %s: %v", mempoolTx.Address, err)
						continue
					}

					// Convert to database transaction
					dbTx := &db.Transaction{
						AddressID:     addr.ID,
						TxID:          mempoolTx.TxID,
						BlockHash:     mempoolTx.BlockHash,
						BlockHeight:   mempoolTx.BlockHeight,
						Amount:        mempoolTx.Amount,
						IsIncoming:    mempoolTx.IsIncoming,
						Confirmations: int(blockHeader.Confirmations),
						Status:        "confirmed",
					}

					// Check if the address in the transaction is being tracked
					if mt.IsAddressTracked(mempoolTx.Address) {
						log.Printf("Processing transaction %s for tracked address %s", txID, mempoolTx.Address)
						// Add transaction to database
						if err := db.AddTransaction(mt.db, dbTx); err != nil {
							log.Printf("Failed to add transaction to database: %v", err)
						}

						// Update address balance
						if err := db.UpdateAddressBalanceWithTransaction(mt.db, mempoolTx.Address, dbTx); err != nil {
							log.Printf("Failed to update address balance: %v", err)
						}
					}
				}
			}

			// Process inputs (outgoing transactions)
			for _, vin := range vins {
				vinMap, ok := vin.(map[string]interface{})
				if !ok {
					log.Printf("Invalid vin format in transaction %s", txID)
					continue
				}

				txid, ok := vinMap["txid"].(string)
				if !ok {
					continue
				}

				// Get the previous transaction to check its output address
				prevTx, err := mt.client.GetRawTransaction(txid)
				if err != nil {
					continue
				}

				voutIndex, ok := vinMap["vout"].(float64)
				if !ok {
					continue
				}

				vouts, ok := prevTx["vout"].([]interface{})
				if !ok || int(voutIndex) >= len(vouts) {
					continue
				}

				vout := vouts[int(voutIndex)].(map[string]interface{})
				scriptPubKey, ok := vout["scriptPubKey"].(map[string]interface{})
				if !ok {
					continue
				}

				addresses, ok := scriptPubKey["addresses"].([]interface{})
				if !ok {
					continue
				}

				for _, addr := range addresses {
					addrStr, ok := addr.(string)
					if !ok {
						continue
					}

					amount, ok := vout["value"].(float64)
					if !ok {
						continue
					}

					// Create a new transaction
					mempoolTx := &Transaction{
						TxID:        txID,
						BlockHash:   blockHash,
						BlockHeight: height,
						Amount:      -amount, // Negative for outgoing
						IsIncoming:  false,
						Address:     addrStr,
					}

					// Get or create address
					addr, err := db.GetOrCreateAddress(mt.db, mempoolTx.Address)
					if err != nil {
						log.Printf("Failed to get or create address %s: %v", mempoolTx.Address, err)
						continue
					}

					// Convert to database transaction
					dbTx := &db.Transaction{
						AddressID:     addr.ID,
						TxID:          mempoolTx.TxID,
						BlockHash:     mempoolTx.BlockHash,
						BlockHeight:   mempoolTx.BlockHeight,
						Amount:        mempoolTx.Amount,
						IsIncoming:    mempoolTx.IsIncoming,
						Confirmations: int(blockHeader.Confirmations),
						Status:        "confirmed",
					}

					// Check if the address in the transaction is being tracked
					if mt.IsAddressTracked(mempoolTx.Address) {
						log.Printf("Processing transaction %s for tracked address %s", txID, mempoolTx.Address)
						// Add transaction to database
						if err := db.AddTransaction(mt.db, dbTx); err != nil {
							log.Printf("Failed to add transaction to database: %v", err)
						}

						// Update address balance
						if err := db.UpdateAddressBalanceWithTransaction(mt.db, mempoolTx.Address, dbTx); err != nil {
							log.Printf("Failed to update address balance: %v", err)
						}
					}
				}
			}
		}
	}

	log.Println("Starting mempool monitoring...")
	// Start monitoring mempool
	go mt.monitorMempool()

	return nil
}

// parseBlockTransactions parses a block hex string to extract transaction IDs
func parseBlockTransactions(blockHex string) ([]string, error) {
	// Decode block hex to bytes
	blockBytes, err := hex.DecodeString(blockHex)
	if err != nil {
		return nil, fmt.Errorf("failed to decode block hex: %v", err)
	}

	if len(blockBytes) < 80 {
		return nil, fmt.Errorf("block data too short")
	}

	// Skip block header (80 bytes)
	blockBytes = blockBytes[80:]

	// Read transaction count
	txCount, n := binary.Uvarint(blockBytes)
	if n <= 0 {
		return nil, fmt.Errorf("failed to read transaction count")
	}
	blockBytes = blockBytes[n:]

	var txIDs []string
	for i := uint64(0); i < txCount; i++ {
		// Store the start of the transaction
		txStart := len(blockBytes)

		if len(blockBytes) < 4 {
			return nil, fmt.Errorf("block data too short for transaction version")
		}

		// Skip version (4 bytes)
		blockBytes = blockBytes[4:]

		// Read input count
		inputCount, n := binary.Uvarint(blockBytes)
		if n <= 0 {
			return nil, fmt.Errorf("failed to read input count")
		}
		blockBytes = blockBytes[n:]

		// Skip inputs
		for j := uint64(0); j < inputCount; j++ {
			if len(blockBytes) < 36 {
				return nil, fmt.Errorf("block data too short for input")
			}
			// Skip previous output hash (32 bytes) and index (4 bytes)
			blockBytes = blockBytes[36:]

			// Read script length
			scriptLen, n := binary.Uvarint(blockBytes)
			if n <= 0 {
				return nil, fmt.Errorf("failed to read script length")
			}
			blockBytes = blockBytes[n:]

			if len(blockBytes) < int(scriptLen)+4 {
				return nil, fmt.Errorf("block data too short for script and sequence")
			}
			// Skip script and sequence (4 bytes)
			blockBytes = blockBytes[scriptLen+4:]
		}

		// Read output count
		outputCount, n := binary.Uvarint(blockBytes)
		if n <= 0 {
			return nil, fmt.Errorf("failed to read output count")
		}
		blockBytes = blockBytes[n:]

		// Skip outputs
		for j := uint64(0); j < outputCount; j++ {
			if len(blockBytes) < 8 {
				return nil, fmt.Errorf("block data too short for output value")
			}
			// Skip value (8 bytes)
			blockBytes = blockBytes[8:]

			// Read script length
			scriptLen, n := binary.Uvarint(blockBytes)
			if n <= 0 {
				return nil, fmt.Errorf("failed to read script length")
			}
			blockBytes = blockBytes[n:]

			if len(blockBytes) < int(scriptLen) {
				return nil, fmt.Errorf("block data too short for script")
			}
			// Skip script
			blockBytes = blockBytes[scriptLen:]
		}

		if len(blockBytes) < 4 {
			return nil, fmt.Errorf("block data too short for lock time")
		}
		// Skip lock time (4 bytes)
		blockBytes = blockBytes[4:]

		// Calculate transaction hash from the entire transaction data
		txEnd := len(blockBytes)
		txData := blockBytes[txStart:txEnd]
		hash := sha256.Sum256(txData)
		hash = sha256.Sum256(hash[:])
		txID := hex.EncodeToString(hash[:])
		txIDs = append(txIDs, txID)
	}

	return txIDs, nil
}

// Stop stops the mempool tracker
func (mt *MempoolTracker) Stop() {
	close(mt.stopChan)
}

// monitorMempool continuously monitors the mempool for new transactions
func (mt *MempoolTracker) monitorMempool() {
	log.Println("Starting mempool monitoring loop")
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-mt.stopChan:
			log.Println("Stopping mempool monitoring")
			return
		case <-ticker.C:
			log.Println("Checking mempool for new transactions")
			// Get mempool transactions
			txIDs, err := mt.client.GetMempoolTransactions()
			if err != nil {
				log.Printf("Failed to get mempool transactions: %v", err)
				continue
			}
			log.Printf("Found %d transactions in mempool", len(txIDs))

			// Process new transactions
			for _, txID := range txIDs {
				// Check if we already have this transaction
				if mt.HasTransaction(txID) {
					continue
				}

				// Get transaction details
				tx, err := mt.client.GetMempoolTransaction(txID)
				if err != nil {
					log.Printf("Failed to get transaction %s: %v", txID, err)
					continue
				}

				// Create a new transaction
				mempoolTx := &Transaction{
					TxID:        txID,
					BlockHash:   "",
					BlockHeight: 0,
					Amount:      tx["amount"].(float64),
					IsIncoming:  tx["is_incoming"].(bool),
					Address:     tx["address"].(string),
				}

				// Get or create address
				addr, err := db.GetOrCreateAddress(mt.db, mempoolTx.Address)
				if err != nil {
					log.Printf("Failed to get or create address %s: %v", mempoolTx.Address, err)
					continue
				}

				// Convert to database transaction
				dbTx := &db.Transaction{
					AddressID:     addr.ID,
					TxID:          mempoolTx.TxID,
					BlockHash:     mempoolTx.BlockHash,
					BlockHeight:   mempoolTx.BlockHeight,
					Amount:        mempoolTx.Amount,
					IsIncoming:    mempoolTx.IsIncoming,
					Confirmations: 0,
					Status:        "pending",
				}

				// Check if the address in the transaction is being tracked
				if mt.IsAddressTracked(mempoolTx.Address) {
					log.Printf("Processing mempool transaction %s for tracked address %s", txID, mempoolTx.Address)
					// Add transaction to database
					if err := db.AddTransaction(mt.db, dbTx); err != nil {
						log.Printf("Failed to add transaction to database: %v", err)
					}

					// Update address balance
					if err := db.UpdateAddressBalanceWithTransaction(mt.db, mempoolTx.Address, dbTx); err != nil {
						log.Printf("Failed to update address balance: %v", err)
					}
				}
			}
		}
	}
}

// AddAddress adds an address to track
func (mt *MempoolTracker) AddAddress(address string) {
	mt.mu.Lock()
	defer mt.mu.Unlock()
	mt.trackedAddresses[address] = true
}

// RemoveAddress removes an address from tracking
func (mt *MempoolTracker) RemoveAddress(address string) {
	mt.mu.Lock()
	defer mt.mu.Unlock()
	delete(mt.trackedAddresses, address)
}

// IsAddressTracked checks if an address is being tracked
func (mt *MempoolTracker) IsAddressTracked(address string) bool {
	mt.mu.RLock()
	defer mt.mu.RUnlock()
	return mt.trackedAddresses[address]
}

// AddTransaction adds a transaction to the mempool and updates the address balance
func (mt *MempoolTracker) AddTransaction(tx *Transaction) {
	mt.mu.Lock()
	defer mt.mu.Unlock()

	// Only track if address is being monitored
	if !mt.trackedAddresses[tx.Address] {
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

// HasTransaction checks if a transaction is already being tracked
func (mt *MempoolTracker) HasTransaction(txID string) bool {
	mt.mu.RLock()
	defer mt.mu.RUnlock()
	_, exists := mt.transactions[txID]
	return exists
}
