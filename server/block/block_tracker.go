package block

import (
	"database/sql"
	"fmt"
	"log"
	"strconv"

	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/rpcclient"
	"github.com/pvida/dogetracker/server/db"
)

// BlockTracker tracks transactions in blocks
type BlockTracker struct {
	client *rpcclient.Client
	db     *sql.DB
}

// NewBlockTracker creates a new BlockTracker
func NewBlockTracker(client *rpcclient.Client, db *sql.DB) *BlockTracker {
	return &BlockTracker{
		client: client,
		db:     db,
	}
}

// Start starts the block tracker from the given block height
func (t *BlockTracker) Start(startBlock string) error {
	log.Printf("Starting block tracker from block %s", startBlock)

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

	return nil
}

// processTransaction processes a single transaction in a block
func (t *BlockTracker) processTransaction(txID string, blockHash *chainhash.Hash, height int64) error {
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

	// Get current block height for confirmations
	currentHeight, err := t.client.GetBlockCount()
	if err != nil {
		return fmt.Errorf("failed to get current block height: %v", err)
	}
	confirmations := int(currentHeight - height + 1)

	// Track all addresses involved in the transaction
	involvedAddresses := make(map[string]bool)
	spentOutputs := make(map[string]map[int]bool) // Map of txID to vout indices that are spent

	// Process inputs first to get from_addresses and track spent outputs
	for _, vin := range txDetails.Vin {
		if vin.TxID != "" { // Skip coinbase
			// Track this output as spent
			if spentOutputs[vin.TxID] == nil {
				spentOutputs[vin.TxID] = make(map[int]bool)
			}
			spentOutputs[vin.TxID][int(vin.Vout)] = true

			prevTx, err := t.client.GetRawTransaction(vin.TxID)
			if err != nil {
				continue
			}

			prevTxDetails, err := t.client.DecodeRawTransaction(prevTx)
			if err != nil {
				continue
			}

			if vin.Vout < uint32(len(prevTxDetails.Vout)) {
				prevOut := prevTxDetails.Vout[vin.Vout]
				if len(prevOut.ScriptPubKey.Addresses) > 0 {
					fromAddr := prevOut.ScriptPubKey.Addresses[0]
					involvedAddresses[fromAddr] = true

					// Get or create address
					addr, err := db.GetOrCreateAddress(t.db, fromAddr)
					if err != nil {
						continue
					}

					// Create outgoing transaction
					tx := &db.Transaction{
						AddressID:     addr.ID,
						TxID:          txID,
						Amount:        -prevOut.Value, // Negative for outgoing
						BlockHash:     blockHash.String(),
						BlockHeight:   height,
						IsIncoming:    false,
						Confirmations: confirmations,
						FromAddress:   fromAddr,
						ToAddress:     "", // Will be set when processing outputs
						Status:        "pending",
					}
					if err := db.AddTransaction(t.db, tx); err != nil {
						continue
					}

					// Remove the spent output from unspent_outputs
					if err := db.RemoveUnspentOutput(t.db, addr.ID, vin.TxID, int(vin.Vout)); err != nil {
						continue
					}
				}
			}
		}
	}

	// Process outputs to get to_addresses and add unspent outputs
	for i, vout := range txDetails.Vout {
		if len(vout.ScriptPubKey.Addresses) > 0 {
			toAddr := vout.ScriptPubKey.Addresses[0]
			involvedAddresses[toAddr] = true

			// Get or create address
			addr, err := db.GetOrCreateAddress(t.db, toAddr)
			if err != nil {
				continue
			}

			// Find the from_address for this output
			var fromAddr string
			for addr := range involvedAddresses {
				if addr != toAddr {
					fromAddr = addr
					break
				}
			}

			// Create incoming transaction
			tx := &db.Transaction{
				AddressID:     addr.ID,
				TxID:          txID,
				Amount:        vout.Value, // Positive for incoming
				BlockHash:     blockHash.String(),
				BlockHeight:   height,
				IsIncoming:    true,
				Confirmations: confirmations,
				FromAddress:   fromAddr,
				ToAddress:     toAddr,
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
				Amount:    vout.Value,
				Script:    vout.ScriptPubKey.Hex,
			}
			if err := db.AddUnspentOutput(t.db, output); err != nil {
				continue
			}
		}
	}

	return nil
}
