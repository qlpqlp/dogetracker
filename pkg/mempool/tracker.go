package mempool

import (
	"context"
	"database/sql"
	"log"
	"time"

	"github.com/qlpqlp/dogetracker/pkg/spec"
	"github.com/qlpqlp/dogetracker/server/db"
)

type MempoolTracker struct {
	client       spec.Blockchain
	db           *sql.DB
	trackedAddrs map[string]bool
	stop         chan struct{}
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
	}
}

func (t *MempoolTracker) Start(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

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

	// Process each transaction
	for _, txid := range txids {
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
									}
								}
							}
						}
					}
				}
			}
		}
	}

	return nil
}
