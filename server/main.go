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
	log.Printf("Processing block: Height=%d, Hash=%s, Time=%d, Confirmations=%d",
		header.Height, header.Hash, header.Time, header.Confirmations)

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

	// Process transactions for each address
	for _, addr := range addresses {
		// Get address balance
		var balance float64
		err := db.QueryRow(`
			SELECT COALESCE(SUM(amount), 0)
			FROM unspent_transactions
			WHERE address_id = $1
		`, addr.ID).Scan(&balance)
		if err != nil {
			return fmt.Errorf("error getting balance for address %s: %v", addr.Address, err)
		}

		log.Printf("Address %s balance: %.8f DOGE", addr.Address, balance)
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
