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

	"github.com/dogeorg/dogetracker/pkg/core"
	"github.com/dogeorg/dogetracker/pkg/mempool"
	"github.com/dogeorg/dogetracker/server/api"
	serverdb "github.com/dogeorg/dogetracker/server/db"
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

	flag.Parse()

	// Validate API token
	if *apiToken == "" {
		log.Fatal("API token is required")
	}

	// Connect to database
	dbConnStr := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		*dbHost, *dbPort, *dbUser, *dbPass, *dbName)

	dbConn, err := sql.Open("postgres", dbConnStr)
	if err != nil {
		log.Fatalf("Error connecting to database: %v", err)
	}
	defer dbConn.Close()

	// Initialize database schema
	if err := serverdb.InitDB(dbConn); err != nil {
		log.Fatalf("Error initializing database: %v", err)
	}

	// Create RPC client
	client := core.NewCoreRPCClient(*rpcHost, *rpcPort, *rpcUser, *rpcPass)

	// Get tracked addresses
	trackedAddresses, err := serverdb.GetAllTrackedAddresses(dbConn)
	if err != nil {
		log.Printf("Error getting tracked addresses: %v", err)
		trackedAddresses = []string{} // Empty list if error
	}

	// Create mempool tracker
	mempoolTracker := mempool.NewMempoolTracker(client, dbConn, trackedAddresses)

	// Create API server
	server := api.NewServer(dbConn, *apiToken, mempoolTracker)

	// Start API server
	go func() {
		if err := server.Start(*apiPort); err != nil {
			log.Fatalf("Error starting API server: %v", err)
		}
	}()

	// Start mempool tracker
	go mempoolTracker.Start(context.Background())

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Println("Shutting down...")
}
