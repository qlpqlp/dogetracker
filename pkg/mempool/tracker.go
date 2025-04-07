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

	// Log tracked addresses for debugging
	log.Printf("Currently tracking %d addresses: %v", len(t.trackedAddrs), t.trackedAddrs)

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
			log.Printf("Checking output address: %s", addrStr)

			// Check if this address is in our tracked addresses
			found := false
			for trackedAddr, addrInfo := range t.trackedAddrs {
				if addrStr == trackedAddr {
					found = true
					// Found a tracked address in the output
					amount := float64(vout.Value) / 1e8 // Convert from satoshis to DOGE
					log.Printf("Found tracked address in output: %s (ID: %d)", addrStr, addrInfo.id)

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
					} else if err != nil {
						log.Printf("Error checking for existing transaction: %v", err)
					} else {
						log.Printf("Transaction already exists for address %s: %s", addrStr, txid)
					}
					break
				}
			}

			if !found {
				log.Printf("Address not found in tracked addresses: %s", addrStr)
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
				log.Printf("Error getting previous transaction %s: %v", txIDHex, err)
				continue
			}

			prevTxBytes, err := doge.HexDecode(prevTxData["hex"].(string))
			if err != nil {
				log.Printf("Error decoding previous transaction hex: %v", err)
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
				log.Printf("Checking input address: %s", addrStr)

				// Check if this address is in our tracked addresses
				found := false
				for trackedAddr, addrInfo := range t.trackedAddrs {
					if addrStr == trackedAddr {
						found = true
						// Found a tracked address in the input
						amount := -float64(prevOut.Value) / 1e8 // Negative for outgoing, convert from satoshis
						log.Printf("Found tracked address in input: %s (ID: %d)", addrStr, addrInfo.id)

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
						} else if err != nil {
							log.Printf("Error checking for existing transaction: %v", err)
						} else {
							log.Printf("Transaction already exists for address %s: %s", addrStr, txid)
						}
						break
					}
				}

				if !found {
					log.Printf("Address not found in tracked addresses: %s", addrStr)
				}
			}
		}
	}

	return nil
}

// AddAddress adds a new address to track in the mempool
func (t *MempoolTracker) AddAddress(address string) {
	// Get or create address with default confirmations
	addr, err := db.GetOrCreateAddressWithConfirmations(t.db, address, 1)
	if err != nil {
		log.Printf("Error getting or creating address %s: %v", address, err)
		return
	}

	// Add to tracked addresses map
	t.trackedAddrs[address] = struct {
		id                    int64
		requiredConfirmations int
	}{addr.ID, addr.RequiredConfirmations}

	log.Printf("Added address to mempool tracker: %s (ID: %d, Required Confirmations: %d)",
		address, addr.ID, addr.RequiredConfirmations)
}
