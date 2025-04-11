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
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	_ "github.com/lib/pq"
	"github.com/qlpqlp/dogetracker/pkg/api"
	"github.com/qlpqlp/dogetracker/pkg/doge"
	"github.com/qlpqlp/dogetracker/pkg/tracker"
	serverdb "github.com/qlpqlp/dogetracker/server/db"
)

var (
	dbHost     string
	dbPort     int
	dbUser     string
	dbPass     string
	dbName     string
	apiPort    int
	apiToken   string
	nodeAddr   string
	startBlock int64
)

func init() {
	flag.StringVar(&dbHost, "db-host", "localhost", "Database host")
	flag.IntVar(&dbPort, "db-port", 5432, "Database port")
	flag.StringVar(&dbUser, "db-user", "postgres", "Database username")
	flag.StringVar(&dbPass, "db-pass", "", "Database password")
	flag.StringVar(&dbName, "db-name", "dogetracker", "Database name")
	flag.IntVar(&apiPort, "api-port", 8080, "API port")
	flag.StringVar(&apiToken, "api-token", "", "API token")
	flag.StringVar(&nodeAddr, "node", "qlplock.ddns.net:22556", "Dogecoin node address to connect to")
	flag.Int64Var(&startBlock, "start-block", 0, "Block height to start syncing from")
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

// ProcessBlockTransactions processes all transactions in a block
func ProcessBlockTransactions(block *doge.BlockchainBlock, db *sql.DB, trackedAddresses map[string]bool) error {
	// Get block hash
	blockHash := block.Hash

	// Create SPV node with default peers
	peers := []string{
		"seed.dogecoin.net:22556",  // Mainnet seed node
		"seed.dogecoin.com:22556",  // Mainnet seed node
		"seed.multidoge.org:22556", // Mainnet seed node
	}
	spvNode := doge.NewSPVNode(peers, 0, doge.NewSQLDatabase(db))

	// Add tracked addresses to SPV node
	for addr := range trackedAddresses {
		spvNode.AddWatchAddress(addr)
	}

	// Connect to a peer
	for _, peer := range peers {
		if err := spvNode.ConnectToPeer(peer); err != nil {
			log.Printf("Failed to connect to %s: %v", peer, err)
			continue
		}
		log.Printf("Connected to %s", peer)
		break
	}

	// Get block transactions using SPV
	txs, err := spvNode.GetBlockTransactions(blockHash)
	if err != nil {
		return fmt.Errorf("error getting block transactions: %v", err)
	}

	log.Printf("Processing block %s with %d transactions", blockHash, len(txs))

	// Process each transaction
	for i, tx := range txs {
		log.Printf("Processing transaction %d/%d: %s", i+1, len(txs), tx.TxID)

		// Check if any of the tracked addresses are involved in this transaction
		if spvNode.ProcessTransaction(tx) {
			// Found a transaction involving tracked address
			log.Printf("Found transaction involving tracked address")

			// Store transaction in database
			_, err := db.Exec(`
				INSERT INTO transactions (txid, block_hash, block_height, address, amount, timestamp)
				VALUES ($1, $2, $3, $4, $5, $6)
				ON CONFLICT (txid, address) DO NOTHING
			`, tx.TxID, blockHash, block.Height, "", tx.Outputs[0].Value, block.Time)

			if err != nil {
				return fmt.Errorf("error storing transaction: %v", err)
			}
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
	flag.Parse()

	// Create context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create database connection
	db, err := tracker.NewDB(dbHost, dbPort, dbUser, dbPass, dbName)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	// Initialize database schema
	if err := serverdb.InitDB(db); err != nil {
		log.Fatalf("Failed to initialize database schema: %v", err)
	}

	// Create SPV node with specified peer and start block
	peers := []string{nodeAddr}
	spvNode := doge.NewSPVNode(peers, uint32(startBlock), doge.NewSQLDatabase(db))

	log.Printf("Attempting to connect to Dogecoin node at %s...", nodeAddr)
	log.Printf("Starting from block height %d", startBlock)

	// Connect to peer
	if err := spvNode.ConnectToPeer(nodeAddr); err != nil {
		log.Fatalf("Failed to connect to peer: %v", err)
	}
	log.Printf("Successfully connected to peer")

	// Create tracker
	t := tracker.NewTracker(db, spvNode)

	// Create API server
	api := api.NewAPI(db, t, apiPort, apiToken)
	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", apiPort),
		Handler: api,
	}

	// Start block processing
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
				// Get current block height
				height, err := spvNode.GetBlockCount()
				if err != nil {
					log.Printf("Error getting block height: %v", err)
					time.Sleep(10 * time.Second)
					continue
				}
				log.Printf("Current block height: %d", height)

				// Process new blocks
				if err := t.ProcessBlocks(ctx, height); err != nil {
					log.Printf("Error processing blocks: %v", err)
				}

				time.Sleep(10 * time.Second)
			}
		}
	}()

	// Start API server
	go func() {
		log.Printf("Starting API server on port %d", apiPort)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Error starting API server: %v", err)
		}
	}()

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Println("Shutting down server...")

	// Shutdown server
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	// Cancel the processing context
	cancel()

	// Shutdown server
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("Error shutting down server: %v", err)
	}

	log.Println("Server shutdown complete")
}
