package mempool

import (
	"context"
	"database/sql"
	"log"
	"time"

	"github.com/dogeorg/doge"
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
		// Get raw transaction hex
		txData, err := t.client.GetRawTransaction(txid)
		if err != nil {
			continue
		}

		// Convert hex to bytes
		txBytes, err := doge.HexDecode(txData["hex"].(string))
		if err != nil {
			log.Printf("Error decoding transaction hex: %v", err)
			continue
		}

		// Decode transaction using doge package
		tx := doge.DecodeTransaction(txBytes)

		// Process outputs (incoming transactions)
		for i, vout := range tx.Vout {
			// Extract addresses from output script
			addresses, err := doge.ExtractAddresses(vout.ScriptPubKey, &doge.DogeMainNetChain)
			if err != nil {
				continue
			}

			for _, addr := range addresses {
				if t.trackedAddrs[addr] {
					// Found a tracked address in the output
					amount := float64(vout.Value) / 1e8 // Convert from satoshis to DOGE
					transaction := &db.Transaction{
						TxID:       txid,
						Amount:     amount,
						IsIncoming: true,
						Status:     "pending",
					}
					if err := db.AddTransaction(t.db, transaction); err != nil {
						log.Printf("Error adding pending transaction: %v", err)
					}
				}
			}
		}

		// Process inputs (outgoing transactions)
		for _, vin := range tx.Vin {
			if vin.Coinbase != "" {
				continue // Skip coinbase transactions
			}

			// Get the previous transaction
			prevTxData, err := t.client.GetRawTransaction(vin.TxID)
			if err != nil {
				continue
			}

			// Decode previous transaction
			prevTxBytes, err := doge.HexDecode(prevTxData["hex"].(string))
			if err != nil {
				continue
			}
			prevTx := doge.DecodeTransaction(prevTxBytes)

			// Check if the spent output belonged to a tracked address
			if vin.VoutIndex < uint32(len(prevTx.Vout)) {
				prevOut := prevTx.Vout[vin.VoutIndex]
				addresses, err := doge.ExtractAddresses(prevOut.ScriptPubKey, &doge.DogeMainNetChain)
				if err != nil {
					continue
				}

				for _, addr := range addresses {
					if t.trackedAddrs[addr] {
						// Found a tracked address in the input
						amount := -float64(prevOut.Value) / 1e8 // Negative for outgoing, convert from satoshis
						transaction := &db.Transaction{
							TxID:       txid,
							Amount:     amount,
							IsIncoming: false,
							Status:     "pending",
						}
						if err := db.AddTransaction(t.db, transaction); err != nil {
							log.Printf("Error adding pending transaction: %v", err)
						}
					}
				}
			}
		}
	}

	return nil
}
