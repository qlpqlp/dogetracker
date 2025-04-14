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

type MempoolTracker struct {
	client       spec.Blockchain
	db           *sql.DB
	trackedAddrs map[string]bool
	stop         chan struct{}
	lock         sync.RWMutex
	lastTxids    map[string]bool
}

func NewMempoolTracker(client spec.Blockchain, db *sql.DB, trackedAddrs []string) *MempoolTracker {
	addrMap := make(map[string]bool)
	for _, addr := range trackedAddrs {
		addrMap[addr] = true
	}

	return &MempoolTracker{
		client:       client,
		db:           db,
		trackedAddrs: addrMap,
		stop:         make(chan struct{}),
		lastTxids:    make(map[string]bool),
	}
}

func (t *MempoolTracker) Start(ctx context.Context) {
	// Check mempool every 2 seconds
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	// Initial mempool check
	if err := t.checkMempool(); err != nil {
		log.Printf("Error in initial mempool check: %v", err)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.stop:
			return
		case <-ticker.C:
			if err := t.checkMempool(); err != nil {
				log.Printf("Error checking mempool: %v", err)
			}
		}
	}
}

func (t *MempoolTracker) Stop() {
	close(t.stop)
}

func (t *MempoolTracker) checkMempool() error {
	// Get all transactions in mempool
	txids, err := t.client.GetMempoolTransactions()
	if err != nil {
		return err
	}

	t.lock.Lock()
	defer t.lock.Unlock()

	// Create a map of current transactions
	currentTxids := make(map[string]bool)
	for _, txid := range txids {
		currentTxids[txid] = true
	}

	// Process new transactions
	for _, txid := range txids {
		// Skip if we've already processed this transaction
		if t.lastTxids[txid] {
			continue
		}

		tx, err := t.client.GetRawTransaction(txid)
		if err != nil {
			continue
		}

		// Check inputs and outputs for tracked addresses
		vins := tx["vin"].([]interface{})
		vouts := tx["vout"].([]interface{})

		// Process outputs (incoming transactions)
		for _, vout := range vouts {
			voutMap := vout.(map[string]interface{})
			if scriptPubKey, ok := voutMap["scriptPubKey"].(map[string]interface{}); ok {
				if addresses, ok := scriptPubKey["addresses"].([]interface{}); ok {
					for _, addr := range addresses {
						addrStr := addr.(string)
						if t.trackedAddrs[addrStr] {
							// Found a tracked address in the output
							amount := voutMap["value"].(float64)
							tx := &db.Transaction{
								TxID:       txid,
								Amount:     amount,
								IsIncoming: true,
								Status:     "pending",
							}
							if err := db.AddTransaction(t.db, tx); err != nil {
								log.Printf("Error adding pending transaction: %v", err)
							} else {
								log.Printf("Added pending incoming transaction %s for address %s: %f DOGE", txid, addrStr, amount)
							}
						}
					}
				}
			}
		}

		// Process inputs (outgoing transactions)
		for _, vin := range vins {
			vinMap := vin.(map[string]interface{})
			if txid, ok := vinMap["txid"].(string); ok {
				// Get the previous transaction to check its output address
				prevTx, err := t.client.GetRawTransaction(txid)
				if err != nil {
					continue
				}

				voutIndex := int(vinMap["vout"].(float64))
				if vouts, ok := prevTx["vout"].([]interface{}); ok && voutIndex < len(vouts) {
					vout := vouts[voutIndex].(map[string]interface{})
					if scriptPubKey, ok := vout["scriptPubKey"].(map[string]interface{}); ok {
						if addresses, ok := scriptPubKey["addresses"].([]interface{}); ok {
							for _, addr := range addresses {
								addrStr := addr.(string)
								if t.trackedAddrs[addrStr] {
									// Found a tracked address in the input
									amount := -vout["value"].(float64) // Negative for outgoing
									tx := &db.Transaction{
										TxID:       txid,
										Amount:     amount,
										IsIncoming: false,
										Status:     "pending",
									}
									if err := db.AddTransaction(t.db, tx); err != nil {
										log.Printf("Error adding pending transaction: %v", err)
									} else {
										log.Printf("Added pending outgoing transaction %s for address %s: %f DOGE", txid, addrStr, amount)
									}
								}
							}
						}
					}
				}
			}
		}
	}

	// Update lastTxids with current transactions
	t.lastTxids = currentTxids

	return nil
}
