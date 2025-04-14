package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/dogeorg/dogetracker/pkg/core"
	"github.com/dogeorg/dogetracker/server/api"
	serverdb "github.com/dogeorg/dogetracker/server/db"
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

func main() {
	log.Println("Starting DogeTracker server...")

	// Parse command line flags
	dbHost := flag.String("db-host", getEnvOrDefault("DB_HOST", "localhost"), "PostgreSQL host")
	dbPort := flag.Int("db-port", getEnvIntOrDefault("DB_PORT", 5432), "PostgreSQL port")
	dbUser := flag.String("db-user", getEnvOrDefault("DB_USER", "postgres"), "PostgreSQL username")
	dbPass := flag.String("db-pass", getEnvOrDefault("DB_PASS", "postgres"), "PostgreSQL password")
	dbName := flag.String("db-name", getEnvOrDefault("DB_NAME", "dogetracker"), "PostgreSQL database name")

	// API flags
	apiPort := flag.Int("api-port", getEnvIntOrDefault("API_PORT", 8080), "API server port")
	apiToken := flag.String("api-token", getEnvOrDefault("API_TOKEN", ""), "API authentication token")

	// Dogecoin RPC flags
	rpcHost := flag.String("rpc-host", getEnvOrDefault("DOGE_RPC_HOST", "127.0.0.1"), "Dogecoin RPC host")
	rpcPort := flag.Int("rpc-port", getEnvIntOrDefault("DOGE_RPC_PORT", 22555), "Dogecoin RPC port")
	rpcUser := flag.String("rpc-user", getEnvOrDefault("DOGE_RPC_USER", "dogecoin"), "Dogecoin RPC username")
	rpcPass := flag.String("rpc-pass", getEnvOrDefault("DOGE_RPC_PASS", "dogecoin"), "Dogecoin RPC password")
	startBlock := flag.String("start-block", getEnvOrDefault("START_BLOCK", "0"), "Starting block height")

	flag.Parse()

	log.Printf("Connecting to database at %s:%d...", *dbHost, *dbPort)
	// Connect to database
	dbConnStr := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		*dbHost, *dbPort, *dbUser, *dbPass, *dbName)

	dbConn, err := sql.Open("postgres", dbConnStr)
	if err != nil {
		log.Fatalf("Error connecting to database: %v", err)
	}
	defer dbConn.Close()

	log.Println("Initializing database schema...")
	// Initialize database schema
	if err := serverdb.InitDB(dbConn); err != nil {
		log.Fatalf("Error initializing database: %v", err)
	}

	log.Printf("Connecting to Dogecoin RPC at %s:%d...", *rpcHost, *rpcPort)
	// Create RPC client
	client := core.NewCoreRPCClient(*rpcHost, *rpcPort, *rpcUser, *rpcPass)

	// Test RPC connection
	blockCount, err := client.GetBlockCount()
	if err != nil {
		log.Fatalf("Error connecting to Dogecoin RPC: %v", err)
	}
	log.Printf("Connected to Dogecoin node. Current block height: %d", blockCount)

	log.Println("Getting tracked addresses...")
	// Get tracked addresses
	trackedAddresses, err := serverdb.GetAllTrackedAddresses(dbConn)
	if err != nil {
		log.Printf("Error getting tracked addresses: %v", err)
		trackedAddresses = []string{} // Empty list if error
	}
	log.Printf("Found %d tracked addresses", len(trackedAddresses))

	log.Println("Creating mempool tracker...")
	// Create mempool tracker
	mempoolTracker := mempool.NewMempoolTracker(dbConn, client)

	// Add tracked addresses to mempool tracker
	for _, addr := range trackedAddresses {
		log.Printf("Adding address to tracker: %s", addr)
		mempoolTracker.AddAddress(addr)
	}

	log.Printf("Starting mempool tracker from block %s...", *startBlock)
	if err := mempoolTracker.Start(*startBlock); err != nil {
		log.Fatalf("Failed to start mempool tracker: %v", err)
	}
	defer mempoolTracker.Stop()

	log.Printf("Starting API server on port %d...", *apiPort)
	// Create API server
	server := api.NewServer(dbConn, *apiToken, mempoolTracker)

	// Start API server
	go func() {
		if err := server.Start(*apiPort); err != nil {
			log.Fatalf("Error starting API server: %v", err)
		}
	}()

	log.Println("Server started successfully. Waiting for interrupt signal...")
	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Println("Shutting down...")
}
