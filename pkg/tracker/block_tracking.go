package tracker

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/dogeorg/doge"
	"github.com/dogeorg/dogetracker/pkg/database"
)

type BlockTracker struct {
	client    *doge.Client
	db        *database.DB
	minConfs  int
	addresses map[string]bool
}

func NewBlockTracker(client *doge.Client, db *database.DB, minConfs int) *BlockTracker {
	return &BlockTracker{
		client:    client,
		db:        db,
		minConfs:  minConfs,
		addresses: make(map[string]bool),
	}
}

func (bt *BlockTracker) AddAddress(address string) error {
	// Add address to database
	_, err := bt.db.Exec("INSERT INTO addresses (address) VALUES ($1) ON CONFLICT (address) DO NOTHING", address)
	if err != nil {
		return fmt.Errorf("error adding address to database: %v", err)
	}

	bt.addresses[address] = true
	log.Printf("Added address for tracking: %s", address)
	return nil
}

func (bt *BlockTracker) ProcessBlock(blockHash string) error {
	block, err := bt.client.GetBlock(blockHash)
	if err != nil {
		return fmt.Errorf("error getting block: %v", err)
	}

	log.Printf("Processing block %d", block.Height)

	for _, tx := range block.Tx {
		// Process each transaction in the block
		if err := bt.processTransaction(tx, block.Height); err != nil {
			log.Printf("Error processing transaction %s: %v", tx.Txid, err)
			continue
		}
	}

	return nil
}

func (bt *BlockTracker) processTransaction(tx *doge.Transaction, blockHeight int64) error {
	// Check if any of our tracked addresses are involved in this transaction
	for _, vout := range tx.Vout {
		if vout.ScriptPubKey.Addresses != nil {
			for _, addr := range vout.ScriptPubKey.Addresses {
				if bt.addresses[addr] {
					// This is a transaction to one of our tracked addresses
					amount := float64(vout.Value)

					// Insert into transactions table
					_, err := bt.db.Exec(`
						INSERT INTO transactions (tx_hash, address_id, amount, block_height, confirmations)
						SELECT $1, id, $2, $3, 1
						FROM addresses WHERE address = $4
					`, tx.Txid, amount, blockHeight, addr)
					if err != nil {
						return fmt.Errorf("error inserting transaction: %v", err)
					}

					// Insert into unspent_transactions table
					_, err = bt.db.Exec(`
						INSERT INTO unspent_transactions (tx_hash, address_id, amount, block_height, confirmations)
						SELECT $1, id, $2, $3, 1
						FROM addresses WHERE address = $4
					`, tx.Txid, amount, blockHeight, addr)
					if err != nil {
						return fmt.Errorf("error inserting unspent transaction: %v", err)
					}

					log.Printf("Transaction received: %s, amount: %f DOGE, address: %s", tx.Txid, amount, addr)
				}
			}
		}
	}

	// Check for spent transactions
	for _, vin := range tx.Vin {
		if vin.Txid != "" {
			// Remove from unspent_transactions
			_, err := bt.db.Exec(`
				DELETE FROM unspent_transactions
				WHERE tx_hash = $1
			`, vin.Txid)
			if err != nil {
				return fmt.Errorf("error removing spent transaction: %v", err)
			}

			log.Printf("Transaction spent: %s", vin.Txid)
		}
	}

	return nil
}

func (bt *BlockTracker) UpdateConfirmations() error {
	// Get current block height
	info, err := bt.client.GetBlockChainInfo()
	if err != nil {
		return fmt.Errorf("error getting blockchain info: %v", err)
	}

	// Update confirmations for all transactions
	_, err = bt.db.Exec(`
		UPDATE transactions 
		SET confirmations = $1 - block_height + 1,
			updated_at = CURRENT_TIMESTAMP
		WHERE block_height IS NOT NULL
	`, info.Blocks)
	if err != nil {
		return fmt.Errorf("error updating transaction confirmations: %v", err)
	}

	// Update confirmations for unspent transactions
	_, err = bt.db.Exec(`
		UPDATE unspent_transactions 
		SET confirmations = $1 - block_height + 1,
			updated_at = CURRENT_TIMESTAMP
		WHERE block_height IS NOT NULL
	`, info.Blocks)
	if err != nil {
		return fmt.Errorf("error updating unspent transaction confirmations: %v", err)
	}

	return nil
}

func (bt *BlockTracker) Start(ctx context.Context) error {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := bt.UpdateConfirmations(); err != nil {
				log.Printf("Error updating confirmations: %v", err)
			}
		}
	}
}
