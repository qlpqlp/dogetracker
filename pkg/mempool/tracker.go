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

	log.Printf("Found %d transactions in mempool", len(txids))

	// Process each transaction
	for _, txid := range txids {
		// Get raw transaction hex
		txData, err := t.client.GetRawTransaction(txid)
		if err != nil {
			log.Printf("Error getting raw transaction %s: %v", txid, err)
			continue
		}

		// Convert hex to bytes
		txBytes, err := doge.HexDecode(txData["hex"].(string))
		if err != nil {
			log.Printf("Error decoding transaction hex: %v", err)
			continue
		}

		// Decode transaction using doge package
		tx := doge.DecodeTx(txBytes)

		// Process outputs (incoming transactions)
		for _, vout := range tx.VOut {
			// Extract addresses from output script
			scriptType, addr := doge.ClassifyScript(vout.Script, &doge.DogeMainNetChain)
			if scriptType == "" {
				continue
			}

			addrStr := string(addr)
			//log.Printf("Checking output address: %s", addrStr)
			if t.trackedAddrs[addrStr] {
				//log.Printf("Found tracked address in output: %s", addrStr)
				// Found a tracked address in the output
				amount := float64(vout.Value) / 1e8 // Convert from satoshis to DOGE

				// Get or create the address in the database to get its ID
				addr, err := db.GetOrCreateAddress(t.db, addrStr)
				if err != nil {
					log.Printf("Error getting address ID for %s: %v", addrStr, err)
					continue
				}

				transaction := &db.Transaction{
					AddressID:  addr.ID,
					TxID:       txid,
					Amount:     amount,
					IsIncoming: true,
					Status:     "pending",
				}

				// Add transaction to database
				if err := db.AddTransaction(t.db, transaction); err != nil {
					log.Printf("Error adding pending transaction: %v", err)
				} else {
					log.Printf("Added pending transaction for address %s: %s", addrStr, txid)
				}
			}
		}

		// Process inputs (outgoing transactions)
		for _, vin := range tx.VIn {
			// Skip coinbase transactions (they have empty TxID)
			if len(vin.TxID) == 0 {
				continue
			}

			// Get the previous transaction
			txIDHex := doge.HexEncodeReversed(vin.TxID)
			prevTxData, err := t.client.GetRawTransaction(txIDHex)
			if err != nil {
				continue
			}

			prevTxBytes, err := doge.HexDecode(prevTxData["hex"].(string))
			if err != nil {
				continue
			}
			prevTx := doge.DecodeTx(prevTxBytes)

			// Check if the spent output belonged to a tracked address
			if vin.VOut < uint32(len(prevTx.VOut)) {
				prevOut := prevTx.VOut[vin.VOut]
				scriptType, addr := doge.ClassifyScript(prevOut.Script, &doge.DogeMainNetChain)
				if scriptType == "" {
					continue
				}

				addrStr := string(addr)
				//log.Printf("Checking input address: %s", addrStr)
				if t.trackedAddrs[addrStr] {
					//log.Printf("Found tracked address in input: %s", addrStr)
					// Found a tracked address in the input
					amount := -float64(prevOut.Value) / 1e8 // Negative for outgoing, convert from satoshis

					// Get or create the address in the database to get its ID
					addr, err := db.GetOrCreateAddress(t.db, addrStr)
					if err != nil {
						log.Printf("Error getting address ID for %s: %v", addrStr, err)
						continue
					}

					transaction := &db.Transaction{
						AddressID:  addr.ID,
						TxID:       txid,
						Amount:     amount,
						IsIncoming: false,
						Status:     "pending",
					}

					// Add transaction to database
					if err := db.AddTransaction(t.db, transaction); err != nil {
						log.Printf("Error adding pending transaction: %v", err)
					} else {
						log.Printf("Added pending transaction for address %s: %s", addrStr, txid)
					}
				}
			}
		}
	}

	return nil
}

// AddAddress adds a new address to track in the mempool
func (t *MempoolTracker) AddAddress(address string) {
	t.trackedAddrs[address] = true
	log.Printf("Added address to mempool tracker: %s", address)
}
