package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/dogeorg/dogetracker/pkg/core"
	"github.com/dogeorg/dogetracker/server/api"
	"github.com/dogeorg/dogetracker/server/db"
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

	// Create database connection
	dbConn, err := sql.Open("postgres", fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		config.dbHost, config.dbPort, config.dbUser, config.dbPass, config.dbName,
	))
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer dbConn.Close()

	// Initialize database schema
	if err := db.InitDB(dbConn); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}

	// Create RPC client
	rpcClient := core.NewCoreRPCClient(config.rpcHost, config.rpcPort, config.rpcUser, config.rpcPass)

	// Create mempool tracker
	tracker := mempool.NewMempoolTracker(rpcClient, dbConn)

	// Check for last processed block
	var lastBlockHeight int64
	var lastBlockHash string
	err = dbConn.QueryRow(`
		SELECT block_height, block_hash 
		FROM last_processed_block 
		ORDER BY id DESC 
		LIMIT 1
	`).Scan(&lastBlockHeight, &lastBlockHash)
	if err != nil && err != sql.ErrNoRows {
		log.Fatalf("Failed to get last processed block: %v", err)
	}

	// Determine start block
	startBlock := config.startBlock
	if startBlock == "0" && err == nil {
		// If no start block specified and we have a last processed block, start from the next block
		startBlock = strconv.FormatInt(lastBlockHeight+1, 10)
		log.Printf("Resuming from last processed block in database: height %d, hash %s", lastBlockHeight, lastBlockHash)
	} else if startBlock == "0" {
		// If no start block specified and no last processed block, start from current height
		currentHeight, err := rpcClient.GetBlockCount()
		if err != nil {
			log.Fatalf("Failed to get current block height: %v", err)
		}
		startBlock = strconv.FormatInt(currentHeight, 10)
		log.Printf("Starting from current block height: %d", currentHeight)
	}

	// Start mempool tracker
	if err := tracker.Start(startBlock); err != nil {
		log.Fatalf("Failed to start mempool tracker: %v", err)
	}

	// Set up API server
	api.SetDB(dbConn)
	http.HandleFunc("/api/track", api.TrackAddressHandler)
	http.HandleFunc("/api/balance", api.GetBalanceHandler)
	http.HandleFunc("/api/transactions", api.GetTransactionsHandler)

	// Start API server
	log.Printf("Starting API server on port %d", config.apiPort)
	if err := http.ListenAndServe(fmt.Sprintf(":%d", config.apiPort), nil); err != nil {
		log.Fatalf("API server error: %v", err)
	}
}
