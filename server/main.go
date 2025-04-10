package main

/*
 * DogeTracker
 *
 * This code is based on the Dogecoin Foundation's DogeWalker project
 * (github.com/dogeorg/dogewalker) and has been modified to create
 * a transaction tracking system for Dogecoin addresses.
 */

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/dogeorg/doge"
	_ "github.com/lib/pq"
	"github.com/qlpqlp/dogetracker/pkg/chaser"
	"github.com/qlpqlp/dogetracker/pkg/core"
	"github.com/qlpqlp/dogetracker/pkg/mempool"
	"github.com/qlpqlp/dogetracker/pkg/spec"
	"github.com/qlpqlp/dogetracker/pkg/tracker"
	"github.com/qlpqlp/dogetracker/server/api"
	serverdb "github.com/qlpqlp/dogetracker/server/db"
)

type Config struct {
	rpcHost   string
	rpcPort   int
	rpcUser   string
	rpcPass   string
	zmqHost   string
	zmqPort   int
	batchSize int

	// PostgreSQL configuration
	dbHost string
	dbPort int
	dbUser string
	dbPass string
	dbName string

	// API configuration
	apiPort    int
	apiToken   string
	startBlock string
}

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvIntOrDefault(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
	}
	return defaultValue
}

// ProcessBlockTransactions processes all transactions in a block and updates the database
func ProcessBlockTransactions(db *sql.DB, block *tracker.ChainBlock, blockchain spec.Blockchain) error {
	// Get all tracked addresses
	rows, err := db.Query(`SELECT id, address, required_confirmations FROM tracked_addresses`)
	if err != nil {
		return fmt.Errorf("failed to get tracked addresses: %v", err)
	}
	defer rows.Close()

	// Create a map of tracked addresses for quick lookup
	trackedAddrs := make(map[string]struct {
		id                    int64
		requiredConfirmations int
	})
	for rows.Next() {
		var id int64
		var addr string
		var requiredConfirmations int
		if err := rows.Scan(&id, &addr, &requiredConfirmations); err != nil {
			return fmt.Errorf("failed to scan tracked address: %v", err)
		}
		trackedAddrs[addr] = struct {
			id                    int64
			requiredConfirmations int
		}{id, requiredConfirmations}
	}

	// Process each transaction in the block
	log.Printf("Starting to process block %d with %d transactions", block.Height, len(block.Block.Tx))
	for txIndex, tx := range block.Block.Tx {
		log.Printf("Processing %d/%d: %s", txIndex+1, len(block.Block.Tx), tx.TxID)

		// Print raw transaction data
		log.Printf("Raw transaction data for %s:", tx.TxID)
		log.Printf("Version: %d", tx.Version)
		log.Printf("LockTime: %d", tx.LockTime)
		log.Printf("Number of inputs: %d", len(tx.VIn))
		log.Printf("Number of outputs: %d", len(tx.VOut))

		// Print input details
		for i, vin := range tx.VIn {
			log.Printf("Input %d:", i)
			log.Printf("  TxID: %x", vin.TxID)
			log.Printf("  VOut: %d", vin.VOut)
			log.Printf("  Script: %x", vin.Script)
			log.Printf("  Sequence: %d", vin.Sequence)
		}

		// Print output details
		for i, vout := range tx.VOut {
			log.Printf("Output %d:", i)
			log.Printf("  Value: %d", vout.Value)
			log.Printf("  Script: %x", vout.Script)
		}

		// Check if this is a coinbase transaction
		isCoinbase := len(tx.VIn) > 0 && len(tx.VIn[0].TxID) == 0
		if isCoinbase {
			log.Printf("Transaction %s is a coinbase transaction, skipping input processing", tx.TxID)
			// Skip processing inputs for coinbase transactions
			continue
		}

		// Calculate transaction fee only if it involves a tracked address
		var fee float64
		var hasTrackedAddress bool

		// First check outputs for tracked addresses
		for _, vout := range tx.VOut {
			// Extract address from script
			scriptType, addr := doge.ClassifyScript(vout.Script, &doge.DogeMainNetChain)
			if scriptType == "" {
				continue
			}
			if _, exists := trackedAddrs[string(addr)]; exists {
				hasTrackedAddress = true
				log.Printf("Found tracked address in output: %s", string(addr))
				break
			}
		}

		// If no tracked addresses in outputs, check inputs
		if !hasTrackedAddress {
			for _, vin := range tx.VIn {
				// Skip coinbase transactions (they have empty TxID)
				if len(vin.TxID) == 0 {
					// This is a coinbase transaction, we can skip it
					log.Printf("Skipping coinbase transaction %s", tx.TxID)
					continue
				}

				prevTxData, err := blockchain.GetRawTransaction(doge.HexEncodeReversed(vin.TxID))
				if err != nil {
					// Skip coinbase transactions silently
					continue
				}
				// Log the raw transaction data structure
				log.Printf("Raw transaction data for %s: %+v", doge.HexEncodeReversed(vin.TxID), prevTxData)

				hexData, ok := prevTxData["hex"].(string)
				if !ok {
					log.Printf("Error: hex field not found or not a string in transaction data for %s", doge.HexEncodeReversed(vin.TxID))
					continue
				}

				prevTxBytes, err := doge.HexDecode(hexData)
				if err != nil {
					log.Printf("Error decoding previous transaction hex: %v", err)
					continue
				}
				// Add logging for raw transaction data
				log.Printf("Decoding transaction %s with %d bytes", doge.HexEncodeReversed(vin.TxID), len(prevTxBytes))
				if len(prevTxBytes) == 0 {
					log.Printf("Warning: Empty transaction data for %s", doge.HexEncodeReversed(vin.TxID))
					continue
				}
				prevTx := doge.DecodeTx(prevTxBytes)
				// Check for invalid transaction structure
				if len(prevTx.VIn) == 0 && len(prevTx.VOut) == 0 {
					log.Printf("Warning: Failed to decode previous transaction %s - empty VIn and VOut", doge.HexEncodeReversed(vin.TxID))
					continue
				}
				// Check if VOut index is valid
				if vin.VOut >= uint32(len(prevTx.VOut)) {
					log.Printf("Warning: VOut index %d out of range for transaction %s (len: %d)", vin.VOut, doge.HexEncodeReversed(vin.TxID), len(prevTx.VOut))
					continue
				}
				// Additional safety check for script length
				if len(prevTx.VOut[vin.VOut].Script) == 0 {
					log.Printf("Warning: Empty script in previous transaction %s output %d", doge.HexEncodeReversed(vin.TxID), vin.VOut)
					continue
				}
				// Extract address from script
				scriptType, addr := doge.ClassifyScript(prevTx.VOut[vin.VOut].Script, &doge.DogeMainNetChain)
				if scriptType == "" {
					continue
				}
				if _, exists := trackedAddrs[string(addr)]; exists {
					hasTrackedAddress = true
					log.Printf("Found tracked address in input: %s", string(addr))
					break
				}
			}
		}

		if hasTrackedAddress {
			log.Printf("Transaction %s involves tracked address, calculating fee", tx.TxID)
			// Calculate fee only for transactions involving tracked addresses
			if len(tx.VIn) > 0 {
				// Sum all inputs
				var totalInput float64
				var inputCount int
				for _, vin := range tx.VIn {
					// Skip coinbase transactions (they have empty TxID)
					if len(vin.TxID) == 0 {
						continue
					}

					prevTxData, err := blockchain.GetRawTransaction(doge.HexEncodeReversed(vin.TxID))
					if err != nil {
						// Skip coinbase transactions silently
						continue
					}
					prevTxBytes, err := doge.HexDecode(prevTxData["hex"].(string))
					if err != nil {
						log.Printf("Error decoding previous transaction hex: %v", err)
						continue
					}
					prevTx := doge.DecodeTx(prevTxBytes)
					if len(prevTx.VIn) == 0 && len(prevTx.VOut) == 0 {
						log.Printf("Warning: Failed to decode previous transaction %s", doge.HexEncodeReversed(vin.TxID))
						continue
					}
					// Check if VOut index is valid
					if vin.VOut >= uint32(len(prevTx.VOut)) {
						log.Printf("Warning: VOut index %d out of range for transaction %s (len: %d)", vin.VOut, doge.HexEncodeReversed(vin.TxID), len(prevTx.VOut))
						continue
					}
					totalInput += float64(prevTx.VOut[vin.VOut].Value) / 1e8
					inputCount++
				}

				// Sum all outputs
				var totalOutput float64
				for _, vout := range tx.VOut {
					totalOutput += float64(vout.Value) / 1e8
				}

				// Only calculate fee if we successfully processed all inputs
				if inputCount == len(tx.VIn) {
					fee = totalInput - totalOutput
					log.Printf("Calculated fee for transaction %s: %f DOGE", tx.TxID, fee)
				} else {
					// If we couldn't get all inputs, use a default fee
					fee = 0.01 // Default fee of 0.01 DOGE
					log.Printf("Using default fee for transaction %s as some inputs could not be processed", tx.TxID)
				}
			}

			// Process outputs (incoming transactions)
			for i, vout := range tx.VOut {
				// Extract address from script
				scriptType, addr := doge.ClassifyScript(vout.Script, &doge.DogeMainNetChain)
				if scriptType == "" {
					continue
				}

				if addrInfo, exists := trackedAddrs[string(addr)]; exists {
					log.Printf("Found tracked address in output %d: %s", i, string(addr))
					// Found a tracked address in the output
					amount := float64(vout.Value) / 1e8 // Convert from satoshis to DOGE

					// Get sender address from inputs
					var senderAddress string
					if len(tx.VIn) > 0 && len(tx.VIn[0].TxID) > 0 {
						// Get the first input's previous transaction
						txIDHex := doge.HexEncodeReversed(tx.VIn[0].TxID)
						prevTxData, err := blockchain.GetRawTransaction(txIDHex)
						if err == nil {
							prevTxBytes, err := doge.HexDecode(prevTxData["hex"].(string))
							if err == nil {
								prevTx := doge.DecodeTx(prevTxBytes)
								if len(prevTx.VIn) == 0 && len(prevTx.VOut) == 0 {
									log.Printf("Warning: Failed to decode previous transaction %s", doge.HexEncodeReversed(tx.VIn[0].TxID))
									continue
								}
								// Check if VOut index is valid
								if tx.VIn[0].VOut >= uint32(len(prevTx.VOut)) {
									log.Printf("Warning: VOut index %d out of range for transaction %s (len: %d)", tx.VIn[0].VOut, doge.HexEncodeReversed(tx.VIn[0].TxID), len(prevTx.VOut))
									continue
								}
								prevOut := prevTx.VOut[tx.VIn[0].VOut]
								_, senderAddr := doge.ClassifyScript(prevOut.Script, &doge.DogeMainNetChain)
								senderAddress = string(senderAddr)
							}
						}
					}

					// Create unspent output record
					unspentOutput := &serverdb.UnspentOutput{
						AddressID: addrInfo.id,
						TxID:      tx.TxID,
						Vout:      i,
						Amount:    amount,
						Script:    doge.HexEncode(vout.Script),
					}

					// Add unspent output to database
					log.Printf("Adding unspent output for address %s in transaction %s", string(addr), tx.TxID)
					if err := serverdb.AddUnspentOutput(db, unspentOutput); err != nil {
						log.Printf("Error adding unspent output: %v", err)
					}

					// Check if this transaction already exists
					var existingTxID int64
					err := db.QueryRow(`
						SELECT id FROM transactions 
						WHERE address_id = $1 AND tx_id = $2
					`, addrInfo.id, tx.TxID).Scan(&existingTxID)

					if err == sql.ErrNoRows {
						// Calculate initial confirmations
						confirmations := 1 // First confirmation

						// Determine initial status
						status := "pending"
						if confirmations >= addrInfo.requiredConfirmations {
							status = "confirmed"
						}

						// Transaction doesn't exist, create a new one
						transaction := &serverdb.Transaction{
							AddressID:       addrInfo.id,
							TxID:            tx.TxID,
							BlockHash:       block.Hash,
							BlockHeight:     block.Height,
							Amount:          amount,
							Fee:             fee,
							Timestamp:       int64(block.Block.Header.Timestamp),
							IsIncoming:      true,
							Confirmations:   confirmations,
							Status:          status,
							SenderAddress:   senderAddress,
							ReceiverAddress: string(addr),
						}

						// Add transaction to database
						log.Printf("Adding new transaction for address %s: %s", string(addr), tx.TxID)
						if err := serverdb.AddTransaction(db, transaction); err != nil {
							log.Printf("Error adding transaction: %v", err)
						}
					} else if err != nil {
						log.Printf("Error checking for existing transaction: %v", err)
					} else {
						// Transaction exists, update it with the new block information
						log.Printf("Updating existing transaction for address %s: %s", string(addr), tx.TxID)
						_, err = db.Exec(`
							UPDATE transactions 
							SET block_hash = $1, 
								block_height = $2, 
								fee = $3,
								timestamp = $4,
								confirmations = CASE 
									WHEN block_height IS NOT NULL THEN 
										CASE 
											WHEN CAST($2 - block_height + 1 AS INTEGER) > 50 THEN 50
											ELSE CAST($2 - block_height + 1 AS INTEGER)
										END
									ELSE 1
								END,
								status = CASE 
									WHEN block_height IS NOT NULL AND 
										CASE 
											WHEN CAST($2 - block_height + 1 AS INTEGER) > 50 THEN 50
											ELSE CAST($2 - block_height + 1 AS INTEGER)
										END >= $5 THEN 'confirmed' 
									ELSE 'pending' 
								END,
								sender_address = $6,
								receiver_address = $7
							WHERE id = $8
						`, block.Hash, block.Height, fee, int64(block.Block.Header.Timestamp), addrInfo.requiredConfirmations, senderAddress, string(addr), existingTxID)
						if err != nil {
							log.Printf("Error updating transaction: %v", err)
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
				prevTxData, err := blockchain.GetRawTransaction(txIDHex)
				if err != nil {
					// Skip coinbase transactions silently
					continue
				}

				prevTxBytes, err := doge.HexDecode(prevTxData["hex"].(string))
				if err != nil {
					log.Printf("Error decoding previous transaction hex: %v", err)
					continue
				}
				prevTx := doge.DecodeTx(prevTxBytes)
				if len(prevTx.VIn) == 0 && len(prevTx.VOut) == 0 {
					log.Printf("Warning: Failed to decode previous transaction %s", txIDHex)
					continue
				}
				// Check if VOut index is valid
				if vin.VOut >= uint32(len(prevTx.VOut)) {
					log.Printf("Warning: VOut index %d out of range for transaction %s (len: %d)", vin.VOut, txIDHex, len(prevTx.VOut))
					continue
				}
				// Check if the spent output belonged to a tracked address
				if vin.VOut < uint32(len(prevTx.VOut)) {
					// Extract address from script
					scriptType, addr := doge.ClassifyScript(prevTx.VOut[vin.VOut].Script, &doge.DogeMainNetChain)
					if scriptType == "" {
						continue
					}

					if addrInfo, exists := trackedAddrs[string(addr)]; exists {
						log.Printf("Found tracked address in input: %s", string(addr))
						// Found a tracked address in the input
						amount := -float64(prevTx.VOut[vin.VOut].Value) / 1e8 // Negative for outgoing, convert from satoshis

						// Get receiver address from outputs
						var receiverAddress string
						if len(tx.VOut) > 0 {
							_, receiverAddr := doge.ClassifyScript(tx.VOut[0].Script, &doge.DogeMainNetChain)
							receiverAddress = string(receiverAddr)
						}

						// Remove the unspent output as it's now spent
						log.Printf("Removing spent output for address %s in transaction %s", string(addr), txIDHex)
						if err := serverdb.RemoveUnspentOutput(db, addrInfo.id, txIDHex, int(vin.VOut)); err != nil {
							log.Printf("Error removing unspent output: %v", err)
						}

						// Check if this transaction already exists
						var existingTxID int64
						err := db.QueryRow(`
							SELECT id FROM transactions 
							WHERE address_id = $1 AND tx_id = $2
						`, addrInfo.id, tx.TxID).Scan(&existingTxID)

						if err == sql.ErrNoRows {
							// Calculate initial confirmations
							confirmations := 1 // First confirmation

							// Determine initial status
							status := "pending"
							if confirmations >= addrInfo.requiredConfirmations {
								status = "confirmed"
							}

							// Transaction doesn't exist, create a new one
							transaction := &serverdb.Transaction{
								AddressID:       addrInfo.id,
								TxID:            tx.TxID,
								BlockHash:       block.Hash,
								BlockHeight:     block.Height,
								Amount:          amount,
								Fee:             fee,
								Timestamp:       int64(block.Block.Header.Timestamp),
								IsIncoming:      false,
								Confirmations:   confirmations,
								Status:          status,
								SenderAddress:   string(addr),
								ReceiverAddress: receiverAddress,
							}

							// Add transaction to database
							log.Printf("Adding new outgoing transaction for address %s: %s", string(addr), tx.TxID)
							if err := serverdb.AddTransaction(db, transaction); err != nil {
								log.Printf("Error adding transaction: %v", err)
							}
						} else if err != nil {
							log.Printf("Error checking for existing transaction: %v", err)
						} else {
							// Transaction exists, update it with the new block information
							log.Printf("Updating existing outgoing transaction for address %s: %s", string(addr), tx.TxID)
							_, err = db.Exec(`
								UPDATE transactions 
								SET block_hash = $1, 
									block_height = $2, 
									fee = $3,
									timestamp = $4,
									confirmations = CASE 
										WHEN block_height IS NOT NULL THEN 
											CASE 
												WHEN CAST($2 - block_height + 1 AS INTEGER) > 50 THEN 50
												ELSE CAST($2 - block_height + 1 AS INTEGER)
											END
										ELSE 1
									END,
									status = CASE 
										WHEN block_height IS NOT NULL AND 
											CASE 
												WHEN CAST($2 - block_height + 1 AS INTEGER) > 50 THEN 50
												ELSE CAST($2 - block_height + 1 AS INTEGER)
											END >= $5 THEN 'confirmed' 
										ELSE 'pending' 
									END,
									sender_address = $6,
									receiver_address = $7
								WHERE id = $8
							`, block.Hash, block.Height, fee, int64(block.Block.Header.Timestamp), addrInfo.requiredConfirmations, string(addr), receiverAddress, existingTxID)
							if err != nil {
								log.Printf("Error updating transaction: %v", err)
							}
						}
					}
				}
			}
		}
	}

	// Update confirmations for all transactions in the block
	for _, addrInfo := range trackedAddrs {
		_, err := db.Exec(`
			UPDATE transactions 
			SET confirmations = CASE 
				WHEN block_height IS NOT NULL THEN 
					CASE 
						WHEN CAST($1 - block_height + 1 AS INTEGER) > 50 THEN 50
						ELSE CAST($1 - block_height + 1 AS INTEGER)
					END
				ELSE 1
			END,
			status = CASE 
				WHEN block_height IS NOT NULL AND 
					CASE 
						WHEN CAST($1 - block_height + 1 AS INTEGER) > 50 THEN 50
						ELSE CAST($1 - block_height + 1 AS INTEGER)
					END >= $2 THEN 'confirmed' 
				ELSE 'pending' 
			END
			WHERE address_id = $3
		`, block.Height, addrInfo.requiredConfirmations, addrInfo.id)
		if err != nil {
			log.Printf("Error updating confirmations for address %d: %v", addrInfo.id, err)
		}
	}

	log.Printf("Finished processing block %d", block.Height)

	// Update balances for all tracked addresses
	for addr, addrInfo := range trackedAddrs {
		// Get address details including unspent outputs
		_, _, unspentOutputs, err := serverdb.GetAddressDetails(db, addr)
		if err != nil {
			log.Printf("Error getting address details: %v", err)
			continue
		}

		// Calculate balance from unspent outputs
		var balance float64
		for _, output := range unspentOutputs {
			balance += output.Amount
		}

		// Update address balance
		if err := serverdb.UpdateAddressBalance(db, addrInfo.id, balance); err != nil {
			log.Printf("Error updating address balance: %v", err)
		}
	}

	return nil
}

// HandleChainReorganization handles a chain reorganization by undoing transactions from invalid blocks
func HandleChainReorganization(db *sql.DB, undo *tracker.UndoForkBlocks) error {
	// Get all tracked addresses
	rows, err := db.Query(`SELECT id, address FROM tracked_addresses`)
	if err != nil {
		return fmt.Errorf("failed to get tracked addresses: %v", err)
	}
	defer rows.Close()

	// Create a map of tracked addresses for quick lookup
	trackedAddrs := make(map[string]int64)
	for rows.Next() {
		var id int64
		var addr string
		if err := rows.Scan(&id, &addr); err != nil {
			return fmt.Errorf("failed to scan tracked address: %v", err)
		}
		trackedAddrs[addr] = id
	}

	// Remove transactions from invalid blocks
	for _, blockHash := range undo.BlockHashes {
		// Get all transactions from this block for tracked addresses
		rows, err := db.Query(`
			SELECT t.id, t.address_id, t.tx_id, t.amount, t.is_incoming, u.id, u.tx_id, u.vout
			FROM transactions t
			LEFT JOIN unspent_outputs u ON t.address_id = u.address_id AND t.tx_id = u.tx_id
			WHERE t.block_hash = $1
		`, blockHash)
		if err != nil {
			log.Printf("Error querying transactions for block %s: %v", blockHash, err)
			continue
		}
		defer rows.Close()

		// Process each transaction
		for rows.Next() {
			var txID int64
			var addrID int64
			var txHash string
			var amount float64
			var isIncoming bool
			var unspentID sql.NullInt64
			var unspentTxID sql.NullString
			var unspentVout sql.NullInt64

			err := rows.Scan(&txID, &addrID, &txHash, &amount, &isIncoming, &unspentID, &unspentTxID, &unspentVout)
			if err != nil {
				log.Printf("Error scanning transaction: %v", err)
				continue
			}

			// If this is an incoming transaction, we need to remove the unspent output
			if isIncoming && unspentID.Valid {
				// Remove the unspent output
				if err := serverdb.RemoveUnspentOutput(db, addrID, unspentTxID.String, int(unspentVout.Int64)); err != nil {
					log.Printf("Error removing unspent output: %v", err)
				}
			}

			// Delete the transaction
			_, err = db.Exec(`DELETE FROM transactions WHERE id = $1`, txID)
			if err != nil {
				log.Printf("Error deleting transaction: %v", err)
			}
		}
	}

	// Update balances for all tracked addresses
	for addr, addrID := range trackedAddrs {
		// Get address details including transactions and unspent outputs
		_, _, unspentOutputs, err := serverdb.GetAddressDetails(db, addr)
		if err != nil {
			log.Printf("Error getting address details: %v", err)
			continue
		}

		// Calculate balance from unspent outputs
		var balance float64
		for _, output := range unspentOutputs {
			balance += output.Amount
		}

		// Update address balance
		if err := serverdb.UpdateAddressBalance(db, addrID, balance); err != nil {
			log.Printf("Error updating address balance: %v", err)
		}
	}

	return nil
}

func main() {
	// Define command line flags
	rpcHost := flag.String("rpc-host", getEnvOrDefault("DOGE_RPC_HOST", "127.0.0.1"), "Dogecoin RPC host")
	rpcPort := flag.Int("rpc-port", getEnvIntOrDefault("DOGE_RPC_PORT", 22555), "Dogecoin RPC port")
	rpcUser := flag.String("rpc-user", getEnvOrDefault("DOGE_RPC_USER", "dogecoin"), "Dogecoin RPC username")
	rpcPass := flag.String("rpc-pass", getEnvOrDefault("DOGE_RPC_PASS", "dogecoin"), "Dogecoin RPC password")
	zmqHost := flag.String("zmq-host", getEnvOrDefault("DOGE_ZMQ_HOST", "127.0.0.1"), "Dogecoin ZMQ host")
	zmqPort := flag.Int("zmq-port", getEnvIntOrDefault("DOGE_ZMQ_PORT", 28332), "Dogecoin ZMQ port")

	// PostgreSQL flags
	dbHost := flag.String("db-host", getEnvOrDefault("DB_HOST", "localhost"), "PostgreSQL host")
	dbPort := flag.Int("db-port", getEnvIntOrDefault("DB_PORT", 5432), "PostgreSQL port")
	dbUser := flag.String("db-user", getEnvOrDefault("DB_USER", "postgres"), "PostgreSQL username")
	dbPass := flag.String("db-pass", getEnvOrDefault("DB_PASS", "postgres"), "PostgreSQL password")
	dbName := flag.String("db-name", getEnvOrDefault("DB_NAME", "dogetracker"), "PostgreSQL database name")

	// API flags
	apiPort := flag.Int("api-port", getEnvIntOrDefault("API_PORT", 420), "API server port")
	apiToken := flag.String("api-token", getEnvOrDefault("API_TOKEN", ""), "API bearer token for authentication")
	startBlock := flag.String("start-block", getEnvOrDefault("START_BLOCK", "DTqAFgNNUgiPEfGmc4HZUkqJ4sz5vADd1n"), "Starting block hash or height to begin processing from")

	// Parse command line flags
	flag.Parse()

	config := Config{
		rpcHost:    *rpcHost,
		rpcPort:    *rpcPort,
		rpcUser:    *rpcUser,
		rpcPass:    *rpcPass,
		zmqHost:    *zmqHost,
		zmqPort:    *zmqPort,
		dbHost:     *dbHost,
		dbPort:     *dbPort,
		dbUser:     *dbUser,
		dbPass:     *dbPass,
		dbName:     *dbName,
		apiPort:    *apiPort,
		apiToken:   *apiToken,
		startBlock: *startBlock,
	}

	// Connect to PostgreSQL
	dbConnStr := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		config.dbHost, config.dbPort, config.dbUser, config.dbPass, config.dbName)
	db, err := sql.Open("postgres", dbConnStr)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	// Initialize database schema
	if err := serverdb.InitDB(db); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}

	// Get last processed block from database
	lastBlockHash, _, err := serverdb.GetLastProcessedBlock(db)
	if err != nil {
		log.Printf("Failed to get last processed block: %v", err)
		// Use default block if database query fails
		lastBlockHash = *startBlock
	}

	log.Printf("Connecting to Dogecoin node at %s:%d", config.rpcHost, config.rpcPort)

	ctx, shutdown := context.WithCancel(context.Background())

	// Core Node blockchain access.
	blockchain := core.NewCoreRPCClient(config.rpcHost, config.rpcPort, config.rpcUser, config.rpcPass)

	// Check if startBlock is a block height (numeric) or block hash
	var startBlockHash string
	if blockHeight, err := strconv.ParseInt(*startBlock, 10, 64); err == nil {
		// It's a block height, get the corresponding block hash
		startBlockHash, err = blockchain.GetBlockHash(blockHeight)
		if err != nil {
			log.Printf("Failed to get block hash for height %d: %v", blockHeight, err)
			startBlockHash = lastBlockHash // Fall back to last processed block
		} else {
			log.Printf("Starting from block height %d (hash: %s)", blockHeight, startBlockHash)
		}
	} else {
		// It's already a block hash
		startBlockHash = *startBlock
		log.Printf("Starting from block hash: %s", startBlockHash)
	}

	// Get tracked addresses from database
	rows, err := db.Query(`SELECT address FROM tracked_addresses`)
	if err != nil {
		log.Printf("Failed to get tracked addresses: %v", err)
	} else {
		defer rows.Close()

		var trackedAddresses []string
		for rows.Next() {
			var addr string
			if err := rows.Scan(&addr); err != nil {
				log.Printf("Error scanning tracked address: %v", err)
				continue
			}
			trackedAddresses = append(trackedAddresses, addr)
		}

		log.Printf("Found %d tracked addresses", len(trackedAddresses))

		// Initialize mempool tracker with actual tracked addresses
		mempoolTracker := mempool.NewMempoolTracker(blockchain, db, trackedAddresses)
		go mempoolTracker.Start(ctx)

		// Start API server with mempool tracker
		apiServer := api.NewServer(db, config.apiToken, mempoolTracker)
		go func() {
			log.Printf("Starting API server on port %d", config.apiPort)
			if err := apiServer.Start(config.apiPort); err != nil {
				log.Printf("API server error: %v", err)
			}
		}()
	}

	// Watch for new blocks.
	zmqTip, err := core.CoreZMQListener(ctx, config.zmqHost, config.zmqPort)
	if err != nil {
		log.Printf("CoreZMQListener: %v", err)
		os.Exit(1)
	}
	tipChanged := chaser.NewTipChaser(ctx, zmqTip, blockchain).Listen(1, true)

	// Walk the blockchain.
	blocks, err := tracker.WalkTheDoge(ctx, tracker.TrackerOptions{
		Chain:           &doge.DogeMainNetChain,
		ResumeFromBlock: startBlockHash,
		Client:          blockchain,
		TipChanged:      tipChanged,
	})
	if err != nil {
		log.Printf("WalkTheDoge: %v", err)
		os.Exit(1)
	}

	// Process blocks and update database
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case b := <-blocks:
				if b.Block != nil {
					log.Printf("Processing block: %v (%v)", b.Block.Hash, b.Block.Height)
					// Update last processed block in database
					if err := serverdb.UpdateLastProcessedBlock(db, b.Block.Hash, b.Block.Height); err != nil {
						log.Printf("Failed to update last processed block: %v", err)
					}

					// Process block transactions and update database
					if err := ProcessBlockTransactions(db, b.Block, blockchain); err != nil {
						log.Printf("Failed to process block transactions: %v", err)
					}
				} else {
					log.Printf("Undoing to: %v (%v)", b.Undo.ResumeFromBlock, b.Undo.LastValidHeight)

					// Handle chain reorganization
					if err := HandleChainReorganization(db, b.Undo); err != nil {
						log.Printf("Failed to handle chain reorganization: %v", err)
					}
				}
			}
		}
	}()

	// Hook ^C signal.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		for {
			select {
			case sig := <-sigCh: // sigterm/sigint caught
				log.Printf("Caught %v signal, shutting down", sig)
				shutdown()
				continue
			case <-ctx.Done():
				return
			}
		}
	}()

	// Wait for shutdown.
	<-ctx.Done()
}
