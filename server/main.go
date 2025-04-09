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
	"time"

	"github.com/dogeorg/doge"
	_ "github.com/lib/pq"
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

	// First, update confirmations for all existing transactions
	for _, addrInfo := range trackedAddrs {
		// Update all transactions for this address
		_, err = db.Exec(`
			UPDATE transactions 
			SET confirmations = CASE 
					WHEN block_height IS NOT NULL THEN 
						CASE 
							WHEN CAST($1 - block_height + 1 AS INTEGER) > 50 THEN 50
							ELSE CAST($1 - block_height + 1 AS INTEGER)
						END
					ELSE 0
				END,
				status = CASE 
					WHEN block_height IS NOT NULL AND 
						CASE 
							WHEN CAST($1 - block_height + 1 AS INTEGER) > 50 THEN 50
							ELSE CAST($1 - block_height + 1 AS INTEGER)
						END >= $3 THEN 'confirmed' 
					ELSE 'pending' 
				END
			WHERE address_id = $2
		`, block.Height, addrInfo.id, addrInfo.requiredConfirmations)
		if err != nil {
			log.Printf("Error updating transaction confirmations: %v", err)
		}
	}

	// Process each transaction in the block
	for _, tx := range block.Block.Tx {
		// Calculate transaction fee
		var fee float64
		var totalInput float64
		var totalOutput float64

		// Sum up all inputs
		for _, input := range tx.VIn {
			if len(input.TxID) == 0 {
				continue // Skip coinbase transactions
			}
			// Get the previous transaction output value
			prevTx, err := blockchain.GetRawTransaction(doge.HexEncodeReversed(input.TxID))
			if err != nil {
				log.Printf("Error getting previous transaction %s: %v", doge.HexEncodeReversed(input.TxID), err)
				continue
			}
			prevTxBytes, err := doge.HexDecode(prevTx["hex"].(string))
			if err != nil {
				log.Printf("Error decoding previous transaction: %v", err)
				continue
			}
			decodedPrevTx := doge.DecodeTx(prevTxBytes)
			if int(input.VOut) < len(decodedPrevTx.VOut) {
				totalInput += float64(decodedPrevTx.VOut[input.VOut].Value) / 1e8
			}
		}

		// Sum up all outputs
		for _, output := range tx.VOut {
			totalOutput += float64(output.Value) / 1e8
		}

		// Fee is the difference between inputs and outputs
		fee = totalInput - totalOutput

		// Get block header to get the block time
		blockHeader, err := blockchain.GetBlockHeader(block.Hash)
		if err != nil {
			log.Printf("Error getting block header: %v", err)
			continue
		}
		txTimestamp := time.Unix(int64(blockHeader.Time), 0)

		// Check if transaction already exists
		exists, err := serverdb.TransactionExists(db, tx.TxID)
		if err != nil {
			log.Printf("Error checking if transaction exists: %v", err)
			continue
		}

		if exists {
			// Update existing transaction
			_, err = db.Exec(`
				UPDATE transactions 
				SET block_hash = $1,
					block_height = $2,
					confirmations = CASE 
						WHEN block_height IS NOT NULL THEN 
							CASE 
								WHEN CAST($2 - block_height + 1 AS INTEGER) > 50 THEN 50 
								ELSE CAST($2 - block_height + 1 AS INTEGER) 
							END 
						ELSE 0 
					END,
					status = CASE 
						WHEN block_height IS NOT NULL AND 
							CASE 
								WHEN CAST($2 - block_height + 1 AS INTEGER) > 50 THEN 50 
								ELSE CAST($2 - block_height + 1 AS INTEGER) 
							END >= required_confirmations 
						THEN 'confirmed' 
						ELSE 'pending' 
					END,
					fee = $3,
					timestamp = $4
				WHERE tx_id = $5`,
				block.Hash, block.Height, fee, txTimestamp, tx.TxID)
			if err != nil {
				log.Printf("Error updating transaction: %v", err)
				continue
			}
		} else {
			// Extract address from the first output
			var address string
			var senderAddress string
			if len(tx.VOut) > 0 {
				scriptType, addr := doge.ClassifyScript(tx.VOut[0].Script, &doge.DogeMainNetChain)
				if scriptType != "" {
					address = string(addr)
				}
			}

			// Try to get sender address from inputs
			if len(tx.VIn) > 0 && len(tx.VIn[0].TxID) > 0 {
				// Get the first input's previous transaction
				txIDHex := doge.HexEncodeReversed(tx.VIn[0].TxID)
				prevTx, err := blockchain.GetRawTransaction(txIDHex)
				if err == nil {
					prevTxBytes, err := doge.HexDecode(prevTx["hex"].(string))
					if err == nil {
						decodedPrevTx := doge.DecodeTx(prevTxBytes)
						if int(tx.VIn[0].VOut) < len(decodedPrevTx.VOut) {
							prevOut := decodedPrevTx.VOut[tx.VIn[0].VOut]
							scriptType, senderAddr := doge.ClassifyScript(prevOut.Script, &doge.DogeMainNetChain)
							if scriptType != "" {
								senderAddress = string(senderAddr)
							}
						}
					}
				}
			}

			// Insert new transaction
			_, err = db.Exec(`
				WITH addr AS (
					SELECT id, required_confirmations 
					FROM tracked_addresses 
					WHERE address = $7
				)
				INSERT INTO transactions (
					tx_id, address_id, amount, block_hash, block_height, 
					confirmations, status, fee, timestamp, is_incoming,
					sender_address, receiver_address
				) 
				SELECT $1, id, $2, $3, CAST($4 AS BIGINT), 
					CASE WHEN $4 IS NOT NULL THEN 1 ELSE 0 END,
					CASE WHEN $4 IS NOT NULL AND 1 >= required_confirmations THEN 'confirmed' ELSE 'pending' END,
					$5, $6, $8, $9, $10
				FROM addr`,
				tx.TxID, totalOutput, block.Hash, block.Height, fee, txTimestamp, address, true, senderAddress, address)
			if err != nil {
				log.Printf("Error adding transaction: %v", err)
				continue
			}
		}
	}

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
	startBlock := flag.String("start-block", getEnvOrDefault("START_BLOCK", ""), "Starting block hash or height to begin processing from")

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

	log.Printf("Connecting to Dogecoin node at %s:%d", config.rpcHost, config.rpcPort)

	// Core Node blockchain access.
	blockchain := core.NewCoreRPCClient(config.rpcHost, config.rpcPort, config.rpcUser, config.rpcPass)

	// Get last processed block from database
	lastBlockHash, lastBlockHeight, err := serverdb.GetLastProcessedBlock(db)
	if err != nil {
		log.Printf("Failed to get last processed block: %v", err)
		// If no database block exists, use the start-block flag if provided
		if *startBlock != "" {
			// Check if startBlock is a block height (numeric) or block hash
			if blockHeight, err := strconv.ParseInt(*startBlock, 10, 64); err == nil {
				// It's a block height, get the corresponding block hash
				lastBlockHash, err = blockchain.GetBlockHash(blockHeight)
				if err != nil {
					log.Printf("Failed to get block hash for height %d: %v", blockHeight, err)
					// Fall back to current best block
					lastBlockHash, err = blockchain.GetBestBlockHash()
					if err != nil {
						log.Printf("Failed to get best block hash: %v", err)
						lastBlockHash = "1a91e3dace36e2be3bf030a65679fe821aa1d6ef92e7c9902eb318182c355691" // Fall back to default block
					}
				} else {
					log.Printf("Starting from block height %d (hash: %s)", blockHeight, lastBlockHash)
				}
			} else {
				// It's already a block hash
				lastBlockHash = *startBlock
				log.Printf("Starting from block hash: %s", lastBlockHash)
			}
		} else {
			// No start block specified, get the current best block
			lastBlockHash, err = blockchain.GetBestBlockHash()
			if err != nil {
				log.Printf("Failed to get best block hash: %v", err)
				lastBlockHash = "1a91e3dace36e2be3bf030a65679fe821aa1d6ef92e7c9902eb318182c355691" // Fall back to default block
			} else {
				log.Printf("No database block found, starting from current best block: %s", lastBlockHash)
			}
		}
	} else {
		log.Printf("Found last processed block in database: %s (height: %d)", lastBlockHash, lastBlockHeight)
	}

	ctx, shutdown := context.WithCancel(context.Background())

	// Always use the last processed block from database if it exists
	startBlockHash := lastBlockHash
	log.Printf("Starting from block hash: %s", startBlockHash)

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

	// Create a custom tip changed channel that only signals when we have the block in our database
	tipChanged := make(chan string, 100)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case blockHash := <-zmqTip:
				// Check if we have this block in our database
				var exists bool
				err := db.QueryRow(`
					SELECT EXISTS (
						SELECT 1 FROM last_processed_block 
						WHERE block_hash = $1
					)
				`, blockHash).Scan(&exists)
				if err != nil {
					log.Printf("Error checking block existence: %v", err)
					continue
				}
				if exists {
					tipChanged <- blockHash
				}
			}
		}
	}()

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

					// Process block transactions and update database
					if err := ProcessBlockTransactions(db, b.Block, blockchain); err != nil {
						log.Printf("Failed to process block transactions: %v", err)
					}

					// Update last processed block in database
					if err := serverdb.UpdateLastProcessedBlock(db, b.Block.Hash, b.Block.Height); err != nil {
						log.Printf("Failed to update last processed block: %v", err)
					}

					// Get the next block hash from the block header
					blockHeader, err := blockchain.GetBlockHeader(b.Block.Hash)
					if err != nil {
						log.Printf("Error getting block header: %v", err)
						continue
					}

					if blockHeader.NextBlockHash != "" {
						log.Printf("Moving to next block: %s", blockHeader.NextBlockHash)
						// Update the start block hash for the next iteration
						startBlockHash = blockHeader.NextBlockHash
					} else {
						// We've reached the current block, wait for new blocks
						log.Printf("Reached current block, waiting for new blocks...")
						// Get the current best block hash
						bestBlockHash, err := blockchain.GetBestBlockHash()
						if err != nil {
							log.Printf("Error getting best block hash: %v", err)
							continue
						}
						// If we're not at the best block yet, continue processing
						if bestBlockHash != b.Block.Hash {
							startBlockHash = bestBlockHash
							log.Printf("New block found, continuing to process: %s", bestBlockHash)
						} else {
							// We're at the best block, wait for the next block notification
							select {
							case <-ctx.Done():
								return
							case newBlockHash := <-tipChanged:
								startBlockHash = newBlockHash
								log.Printf("New block notification received, continuing to process: %s", newBlockHash)
							}
						}
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
