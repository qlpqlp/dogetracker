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
	"github.com/dogeorg/dogetracker/pkg/chaser"
	"github.com/dogeorg/dogetracker/pkg/core"
	"github.com/dogeorg/dogetracker/pkg/migrate"
	"github.com/dogeorg/dogetracker/pkg/walker"
	"github.com/dogeorg/dogetracker/server/api"
	"github.com/dogeorg/dogetracker/server/mempool"
	_ "github.com/lib/pq"
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
	apiPort  int
	apiToken string

	// Block processing configuration
	startBlock int64
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
	apiPort := flag.Int("api-port", getEnvIntOrDefault("API_PORT", 8080), "API server port")
	apiToken := flag.String("api-token", getEnvOrDefault("API_TOKEN", ""), "API bearer token for authentication")

	// Block processing flags
	startBlock := flag.Int64("start-block", int64(getEnvIntOrDefault("START_BLOCK", 0)), "Block height to start processing from")

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

	// Run migrations
	if err := migrate.RunMigrations(db, "migrations"); err != nil {
		log.Fatalf("Failed to run migrations: %v", err)
	}

	// Start API server
	go func() {
		apiServer := api.NewServer(db, config.apiToken)
		log.Printf("Starting API server on port %d", config.apiPort)
		if err := apiServer.Start(config.apiPort); err != nil {
			log.Printf("API server error: %v", err)
		}
	}()

	log.Printf("Connecting to Dogecoin node at %s:%d", config.rpcHost, config.rpcPort)

	ctx, shutdown := context.WithCancel(context.Background())

	// Core Node blockchain access.
	blockchain := core.NewCoreRPCClient(config.rpcHost, config.rpcPort, config.rpcUser, config.rpcPass)

	// Watch for new blocks.
	zmqTip, err := core.CoreZMQListener(ctx, config.zmqHost, config.zmqPort)
	if err != nil {
		log.Printf("CoreZMQListener: %v", err)
		os.Exit(1)
	}
	tipChanged := chaser.NewTipChaser(ctx, zmqTip, blockchain).Listen(1, true)

	// Get the block hash for the start height
	startBlockHash := "0e0bd6be24f5f426a505694bf46f60301a3a08dfdfda13854fdfe0ce7d455d6f" // Default genesis block
	startHeight := int64(0)

	// First try to get the last processed block from the database
	var lastBlockHeight int64
	var lastBlockHash string
	err = db.QueryRow("SELECT block_height, block_hash FROM last_processed_block ORDER BY id DESC LIMIT 1").Scan(&lastBlockHeight, &lastBlockHash)
	if err == nil {
		// Found a last processed block in the database
		startHeight = lastBlockHeight
		startBlockHash = lastBlockHash
		log.Printf("Resuming from last processed block in database: height %d, hash %s", startHeight, startBlockHash)
	} else if err == sql.ErrNoRows {
		// No record in database, use start-block flag if provided
		if config.startBlock > 0 {
			startHeight = config.startBlock
			startBlockHash, err = blockchain.GetBlockHash(config.startBlock)
			if err != nil {
				log.Printf("Failed to get block hash for height %d: %v", config.startBlock, err)
				os.Exit(1)
			}
			log.Printf("Starting from specified block height: %d", config.startBlock)
		} else {
			// No database record and no start-block flag, get current height
			currentHeight, err := blockchain.GetBlockCount()
			if err != nil {
				log.Printf("Failed to get current block height: %v", err)
				os.Exit(1)
			}
			startHeight = currentHeight
			startBlockHash, err = blockchain.GetBlockHash(currentHeight)
			if err != nil {
				log.Printf("Failed to get block hash for current height %d: %v", currentHeight, err)
				os.Exit(1)
			}
			log.Printf("Starting from current block height: %d", currentHeight)
		}

		// Insert initial record
		_, err = db.Exec("INSERT INTO last_processed_block (block_height, block_hash) VALUES ($1, $2)",
			startHeight, startBlockHash)
		if err != nil {
			log.Printf("Failed to insert initial last processed block in database: %v", err)
			// Continue anyway, this is not critical
		}
	} else {
		// Database error
		log.Printf("Failed to query last processed block: %v", err)
		os.Exit(1)
	}

	// Walk the blockchain.
	blocks, err := walker.WalkTheDoge(ctx, walker.WalkerOptions{
		Chain:           &doge.DogeMainNetChain,
		Client:          blockchain,
		TipChanged:      tipChanged,
		ResumeFromBlock: startBlockHash,
	})
	if err != nil {
		log.Printf("WalkTheDoge: %v", err)
		os.Exit(1)
	}

	// Process blocks and update database
	go func() {
		// Initialize mempool tracker
		mempoolTracker := mempool.NewMempoolTracker(blockchain, db)
		if err := mempoolTracker.Start(fmt.Sprintf("%d", startHeight)); err != nil {
			log.Printf("Failed to start mempool tracker: %v", err)
			return
		}

		for {
			select {
			case <-ctx.Done():
				return
			case b := <-blocks:
				if b.Block != nil {
					log.Printf("Processing block: %v (%v)", b.Block.Hash, b.Block.Height)
					// TODO: Process block transactions and update database
					// This will involve:
					// 1. Checking each transaction for tracked addresses
					// 2. Updating transaction records
					// 3. Updating unspent outputs
					// 4. Updating address balances

					// Update the last processed block in the database
					_, err := db.Exec("UPDATE last_processed_block SET block_height = $1, block_hash = $2, processed_at = CURRENT_TIMESTAMP",
						b.Block.Height, b.Block.Hash)
					if err != nil {
						log.Printf("Failed to update last processed block in database: %v", err)
						// Continue anyway, this is not critical
					}
				} else {
					log.Printf("Undoing to: %v (%v)", b.Undo.ResumeFromBlock, b.Undo.LastValidHeight)
					// TODO: Handle chain reorganization
					// This will involve:
					// 1. Removing transactions from invalid blocks
					// 2. Updating unspent outputs
					// 3. Recalculating balances

					// Update the last processed block in the database after reorganization
					_, err := db.Exec("UPDATE last_processed_block SET block_height = $1, block_hash = $2, processed_at = CURRENT_TIMESTAMP",
						b.Undo.LastValidHeight, b.Undo.ResumeFromBlock)
					if err != nil {
						log.Printf("Failed to update last processed block in database: %v", err)
						// Continue anyway, this is not critical
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
