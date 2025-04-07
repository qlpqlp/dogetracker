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
	trackedAddrs map[string]struct {
		id                    int64
		requiredConfirmations int
	}
	stop chan struct{}
}

func NewMempoolTracker(client spec.Blockchain, db *sql.DB, trackedAddrs []string) *MempoolTracker {
	// Initialize the tracker with address IDs and required confirmations
	addrMap := make(map[string]struct {
		id                    int64
		requiredConfirmations int
	})

	// Get address IDs and required confirmations from database
	for _, addr := range trackedAddrs {
		var id int64
		var requiredConfirmations int
		err := db.QueryRow(`
			SELECT id, required_confirmations 
			FROM tracked_addresses 
			WHERE address = $1
		`, addr).Scan(&id, &requiredConfirmations)
		if err != nil {
			log.Printf("Error getting address ID for %s: %v", addr, err)
			continue
		}
		addrMap[addr] = struct {
			id                    int64
			requiredConfirmations int
		}{id, requiredConfirmations}
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
			if addrInfo, exists := t.trackedAddrs[addrStr]; exists {
				// Found a tracked address in the output
				amount := float64(vout.Value) / 1e8 // Convert from satoshis to DOGE

				// Check if this transaction already exists
				var existingTxID int64
				err := t.db.QueryRow(`
					SELECT id FROM transactions 
					WHERE address_id = $1 AND tx_id = $2
				`, addrInfo.id, txid).Scan(&existingTxID)

				if err == sql.ErrNoRows {
					// Transaction doesn't exist, create a new one
					transaction := &db.Transaction{
						AddressID:     addrInfo.id,
						TxID:          txid,
						Amount:        amount,
						IsIncoming:    true,
						Status:        "pending",
						Confirmations: 0,
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
				if addrInfo, exists := t.trackedAddrs[addrStr]; exists {
					// Found a tracked address in the input
					amount := -float64(prevOut.Value) / 1e8 // Negative for outgoing, convert from satoshis

					// Check if this transaction already exists
					var existingTxID int64
					err := t.db.QueryRow(`
						SELECT id FROM transactions 
						WHERE address_id = $1 AND tx_id = $2
					`, addrInfo.id, txid).Scan(&existingTxID)

					if err == sql.ErrNoRows {
						// Transaction doesn't exist, create a new one
						transaction := &db.Transaction{
							AddressID:     addrInfo.id,
							TxID:          txid,
							Amount:        amount,
							IsIncoming:    false,
							Status:        "pending",
							Confirmations: 0,
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
	}

	return nil
}

// AddAddress adds a new address to track in the mempool
func (t *MempoolTracker) AddAddress(address string) {
	// Get address ID and required confirmations from database
	var id int64
	var requiredConfirmations int
	err := t.db.QueryRow(`
		SELECT id, required_confirmations 
		FROM tracked_addresses 
		WHERE address = $1
	`, address).Scan(&id, &requiredConfirmations)
	if err != nil {
		log.Printf("Error getting address ID for %s: %v", address, err)
		return
	}

	t.trackedAddrs[address] = struct {
		id                    int64
		requiredConfirmations int
	}{id, requiredConfirmations}
	log.Printf("Added address to mempool tracker: %s", address)
}
