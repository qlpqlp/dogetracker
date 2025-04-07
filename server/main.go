package main

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
	"github.com/qlpqlp/dogetracker/pkg/walker"
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
func ProcessBlockTransactions(db *sql.DB, block *walker.ChainBlock, blockchain spec.Blockchain) error {
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

	// Process each transaction in the block
	for _, tx := range block.Block.Tx {
		// Process outputs (incoming transactions)
		for i, vout := range tx.VOut {
			// Extract addresses from output script
			scriptType, addr := doge.ClassifyScript(vout.Script, &doge.DogeMainNetChain)
			if scriptType == "" {
				continue
			}

			if addrID, exists := trackedAddrs[string(addr)]; exists {
				// Found a tracked address in the output
				amount := float64(vout.Value) / 1e8 // Convert from satoshis to DOGE

				// Create unspent output record
				unspentOutput := &serverdb.UnspentOutput{
					AddressID: addrID,
					TxID:      tx.TxID,
					Vout:      i,
					Amount:    amount,
					Script:    doge.HexEncode(vout.Script),
				}

				// Add unspent output to database
				if err := serverdb.AddUnspentOutput(db, unspentOutput); err != nil {
					log.Printf("Error adding unspent output: %v", err)
				}

				// Create transaction record
				transaction := &serverdb.Transaction{
					AddressID:     addrID,
					TxID:          tx.TxID,
					BlockHash:     block.Hash,
					BlockHeight:   block.Height,
					Amount:        amount,
					IsIncoming:    true,
					Confirmations: 1, // First confirmation
					Status:        "confirmed",
				}

				// Add transaction to database
				if err := serverdb.AddTransaction(db, transaction); err != nil {
					log.Printf("Error adding transaction: %v", err)
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
				continue
			}

			// Decode previous transaction
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

				if addrID, exists := trackedAddrs[string(addr)]; exists {
					// Found a tracked address in the input
					amount := -float64(prevOut.Value) / 1e8 // Negative for outgoing, convert from satoshis

					// Remove the unspent output as it's now spent
					if err := serverdb.RemoveUnspentOutput(db, addrID, txIDHex, int(vin.VOut)); err != nil {
						log.Printf("Error removing unspent output: %v", err)
					}

					// Create transaction record
					transaction := &serverdb.Transaction{
						AddressID:     addrID,
						TxID:          tx.TxID,
						BlockHash:     block.Hash,
						BlockHeight:   block.Height,
						Amount:        amount,
						IsIncoming:    false,
						Confirmations: 1, // First confirmation
						Status:        "confirmed",
					}

					// Add transaction to database
					if err := serverdb.AddTransaction(db, transaction); err != nil {
						log.Printf("Error adding transaction: %v", err)
					}
				}
			}
		}
	}

	// Update balances for all tracked addresses
	for addr, addrID := range trackedAddrs {
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
		if err := serverdb.UpdateAddressBalance(db, addrID, balance); err != nil {
			log.Printf("Error updating address balance: %v", err)
		}
	}

	return nil
}

// HandleChainReorganization handles a chain reorganization by undoing transactions from invalid blocks
func HandleChainReorganization(db *sql.DB, undo *walker.UndoForkBlocks) error {
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
	dbName := flag.String("db-name", getEnvOrDefault("DB_NAME", "dogewalker"), "PostgreSQL database name")

	// API flags
	apiPort := flag.Int("api-port", getEnvIntOrDefault("API_PORT", 8080), "API server port")
	apiToken := flag.String("api-token", getEnvOrDefault("API_TOKEN", ""), "API bearer token for authentication")
	startBlock := flag.String("start-block", getEnvOrDefault("START_BLOCK", "0e0bd6be24f5f426a505694bf46f60301a3a08dfdfda13854fdfe0ce7d455d6f"), "Starting block hash or height to begin processing from")

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

	// Start API server
	apiServer := api.NewServer(db, config.apiToken)
	go func() {
		log.Printf("Starting API server on port %d", config.apiPort)
		if err := apiServer.Start(config.apiPort); err != nil {
			log.Printf("API server error: %v", err)
		}
	}()

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

	// Watch for new blocks.
	zmqTip, err := core.CoreZMQListener(ctx, config.zmqHost, config.zmqPort)
	if err != nil {
		log.Printf("CoreZMQListener: %v", err)
		os.Exit(1)
	}
	tipChanged := chaser.NewTipChaser(ctx, zmqTip, blockchain).Listen(1, true)

	// Walk the blockchain.
	blocks, err := walker.WalkTheDoge(ctx, walker.WalkerOptions{
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

	// Initialize mempool tracker
	mempoolTracker := mempool.NewMempoolTracker(blockchain, db, []string{*dbName})
	go mempoolTracker.Start(ctx)

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
