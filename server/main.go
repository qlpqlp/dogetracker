package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dogeorg/dogetracker/pkg/api"
	"github.com/dogeorg/dogetracker/pkg/chaser"
	"github.com/dogeorg/dogetracker/pkg/core"
	"github.com/dogeorg/dogetracker/pkg/database"
	"github.com/dogeorg/dogetracker/pkg/spec"
)

type Config struct {
	rpcHost   string
	rpcPort   int
	rpcUser   string
	rpcPass   string
	zmqHost   string
	zmqPort   int
	batchSize int
	dbHost    string
	dbPort    int
	dbUser    string
	dbPass    string
	dbName    string
	apiPort   int
	apiToken  string
}

func processBlock(ctx context.Context, db *database.DB, blockchain spec.Blockchain, height int64) error {
	// Get block hash
	hash, err := blockchain.GetBlockHash(height)
	if err != nil {
		return fmt.Errorf("error getting block hash: %v", err)
	}

	// Get block header for basic info
	header, err := blockchain.GetBlockHeader(hash)
	if err != nil {
		return fmt.Errorf("error getting block header: %v", err)
	}

	// Log block information
	log.Printf("Processing block: Height=%d, Hash=%s", header.Height, header.Hash)

	// Get all tracked addresses
	rows, err := db.Query("SELECT id, address FROM addresses")
	if err != nil {
		return fmt.Errorf("error getting tracked addresses: %v", err)
	}
	defer rows.Close()

	var addresses []struct {
		ID      int64
		Address string
	}
	for rows.Next() {
		var addr struct {
			ID      int64
			Address string
		}
		if err := rows.Scan(&addr.ID, &addr.Address); err != nil {
			return fmt.Errorf("error scanning address: %v", err)
		}
		addresses = append(addresses, addr)
	}

	if len(addresses) == 0 {
		log.Printf("No addresses to track in block %d", height)
	} else {
		log.Printf("Processing transactions for %d tracked addresses in block %d", len(addresses), height)
	}

	// Process transactions for each address
	for _, addr := range addresses {
		// Get raw transactions for this address in this block
		txs, err := blockchain.GetAddressTransactions(addr.Address, height)
		if err != nil {
			log.Printf("Error getting transactions for address %s: %v", addr.Address, err)
			continue
		}

		if len(txs) > 0 {
			log.Printf("Found %d transactions for address %s in block %d", len(txs), addr.Address, height)
		}

		// Process each transaction
		for _, tx := range txs {
			// Store transaction
			_, err := db.Exec(`
				INSERT INTO transactions (address_id, tx_hash, amount, block_height, confirmations, is_spent, created_at)
				VALUES ($1, $2, $3, $4, $5, $6, NOW())
				ON CONFLICT (address_id, tx_hash) DO UPDATE
				SET confirmations = $5, is_spent = $6, updated_at = NOW()
			`, addr.ID, tx.Hash, tx.Amount, height, header.Confirmations, tx.IsSpent)
			if err != nil {
				log.Printf("Error storing transaction %s for address %s: %v", tx.Hash, addr.Address, err)
				continue
			}

			// If transaction is unspent, store in unspent_transactions
			if !tx.IsSpent {
				_, err := db.Exec(`
					INSERT INTO unspent_transactions (address_id, tx_hash, amount, block_height, confirmations, created_at)
					VALUES ($1, $2, $3, $4, $5, NOW())
					ON CONFLICT (address_id, tx_hash) DO UPDATE
					SET confirmations = $5, updated_at = NOW()
				`, addr.ID, tx.Hash, tx.Amount, height, header.Confirmations)
				if err != nil {
					log.Printf("Error storing unspent transaction %s for address %s: %v", tx.Hash, addr.Address, err)
					continue
				}
				log.Printf("Stored unspent transaction %s for address %s: amount=%f", tx.Hash, addr.Address, tx.Amount)
			} else {
				// Remove from unspent if spent
				_, err := db.Exec(`
					DELETE FROM unspent_transactions
					WHERE address_id = $1 AND tx_hash = $2
				`, addr.ID, tx.Hash)
				if err != nil {
					log.Printf("Error removing spent transaction %s for address %s: %v", tx.Hash, addr.Address, err)
					continue
				}
				log.Printf("Removed spent transaction %s for address %s", tx.Hash, addr.Address)
			}
		}

		// Update address balance
		var balance float64
		err = db.QueryRow(`
			SELECT COALESCE(SUM(amount), 0)
			FROM unspent_transactions
			WHERE address_id = $1
		`, addr.ID).Scan(&balance)
		if err != nil {
			log.Printf("Error calculating balance for address %s: %v", addr.Address, err)
			continue
		}

		// Update address balance in database
		_, err = db.Exec(`
			UPDATE addresses
			SET balance = $1, updated_at = NOW()
			WHERE id = $2
		`, balance, addr.ID)
		if err != nil {
			log.Printf("Error updating balance for address %s: %v", addr.Address, err)
			continue
		}
	}

	// Save the processed block
	if err := db.SaveProcessedBlock(height, hash); err != nil {
		return fmt.Errorf("error saving processed block: %v", err)
	}

	return nil
}

func main() {
	// Define command line flags
	rpcHost := flag.String("rpc-host", "127.0.0.1", "RPC host address")
	rpcPort := flag.Int("rpc-port", 22555, "RPC port number")
	rpcUser := flag.String("rpc-user", "dogecoin", "RPC username")
	rpcPass := flag.String("rpc-pass", "dogecoin", "RPC password")
	zmqHost := flag.String("zmq-host", "127.0.0.1", "ZMQ host address")
	zmqPort := flag.Int("zmq-port", 28332, "ZMQ port number")
	startBlock := flag.Int("start-block", -1, "Block height to start from (default: genesis block)")

	// Database flags
	dbHost := flag.String("db-host", "localhost", "Database host address")
	dbPort := flag.Int("db-port", 5432, "Database port number")
	dbUser := flag.String("db-user", "postgres", "Database username")
	dbPass := flag.String("db-pass", "", "Database password")
	dbName := flag.String("db-name", "dogetracker", "Database name")

	// API flags
	apiPort := flag.Int("api-port", 8080, "API server port")
	apiToken := flag.String("api-token", "", "API authentication token")

	// Parse command line flags
	flag.Parse()

	config := Config{
		rpcHost:  *rpcHost,
		rpcPort:  *rpcPort,
		rpcUser:  *rpcUser,
		rpcPass:  *rpcPass,
		zmqHost:  *zmqHost,
		zmqPort:  *zmqPort,
		dbHost:   *dbHost,
		dbPort:   *dbPort,
		dbUser:   *dbUser,
		dbPass:   *dbPass,
		dbName:   *dbName,
		apiPort:  *apiPort,
		apiToken: *apiToken,
	}

	ctx, shutdown := context.WithCancel(context.Background())

	// Initialize database
	db, err := database.NewDB(config.dbHost, config.dbPort, config.dbUser, config.dbPass, config.dbName)
	if err != nil {
		log.Printf("Error connecting to database: %v", err)
		os.Exit(1)
	}
	defer db.Close()

	// Initialize database schema
	if err := db.InitSchema(); err != nil {
		log.Printf("Error initializing database schema: %v", err)
		os.Exit(1)
	}

	// Start API server
	apiServer := api.NewServer(db, config.apiPort, config.apiToken)
	go func() {
		if err := apiServer.Start(); err != nil {
			log.Printf("Error starting API server: %v", err)
			os.Exit(1)
		}
	}()

	// Core Node blockchain access.
	blockchain := core.NewCoreRPCClient(config.rpcHost, config.rpcPort, config.rpcUser, config.rpcPass)

	// Check for last processed block if start-block is not specified
	if *startBlock < 0 {
		lastBlock, err := db.GetLastProcessedBlock()
		if err != nil {
			log.Printf("Error getting last processed block: %v", err)
			os.Exit(1)
		}
		if lastBlock != nil {
			*startBlock = int(lastBlock.Height) + 1
			log.Printf("Resuming from last processed block height: %d", *startBlock)
		}
	}

	// Set up ZMQ listener for new blocks (but don't wait for it)
	zmqTip, err := core.CoreZMQListener(ctx, config.zmqHost, config.zmqPort)
	if err != nil {
		log.Printf("CoreZMQListener: %v", err)
		os.Exit(1)
	}
	_ = chaser.NewTipChaser(ctx, zmqTip, blockchain).Listen(1, true)

	// Process blocks in a separate goroutine
	go func() {
		currentHeight := int64(*startBlock)
		ticker := time.NewTicker(5 * time.Second) // Check for new blocks every 5 seconds
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// Get current block height
				blockCount, err := blockchain.GetBlockCount()
				if err != nil {
					log.Printf("Error getting block count: %v", err)
					continue
				}

				// Process all blocks up to the current height
				for height := currentHeight; height <= blockCount; height++ {
					if err := processBlock(ctx, db, blockchain, height); err != nil {
						log.Printf("Error processing block %d: %v", height, err)
						continue
					}
					currentHeight = height + 1
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
