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
	// Parse command line flags
	rpcHost := flag.String("rpc-host", "localhost", "RPC host")
	rpcPort := flag.Int("rpc-port", 22555, "RPC port")
	rpcUser := flag.String("rpc-user", "", "RPC username")
	rpcPass := flag.String("rpc-pass", "", "RPC password")
	dbHost := flag.String("db-host", "localhost", "Database host")
	dbPort := flag.Int("db-port", 5432, "Database port")
	dbUser := flag.String("db-user", "postgres", "Database username")
	dbPass := flag.String("db-pass", "", "Database password")
	dbName := flag.String("db-name", "dogetracker", "Database name")
	apiPort := flag.Int("api-port", 420, "API port")
	apiToken := flag.String("api-token", "", "API token")

	flag.Parse()

	// Validate required flags
	if *rpcUser == "" || *rpcPass == "" {
		log.Fatal("RPC username and password are required")
	}
	if *apiToken == "" {
		log.Fatal("API token is required")
	}

	// Connect to database
	dbConnStr := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		*dbHost, *dbPort, *dbUser, *dbPass, *dbName)
	dbConn, err := sql.Open("postgres", dbConnStr)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer dbConn.Close()

	// Initialize database schema
	if err := db.InitDB(dbConn); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}

	// Create RPC client
	client := core.NewCoreRPCClient(*rpcHost, *rpcPort, *rpcUser, *rpcPass)

	// Create mempool tracker
	tracker := mempool.NewMempoolTracker(client, dbConn)

	// Start the mempool tracker
	if err := tracker.Start(); err != nil {
		log.Fatalf("Failed to start mempool tracker: %v", err)
	}

	// Set up API server
	api.SetDB(dbConn)
	api.SetToken(*apiToken)
	api.SetTracker(tracker)
	http.HandleFunc("/api/track", api.TrackAddressHandler)
	http.HandleFunc("/api/balance", func(w http.ResponseWriter, r *http.Request) {
		// TODO: Implement balance handler
		http.Error(w, "Not implemented", http.StatusNotImplemented)
	})
	http.HandleFunc("/api/transactions", func(w http.ResponseWriter, r *http.Request) {
		// TODO: Implement transactions handler
		http.Error(w, "Not implemented", http.StatusNotImplemented)
	})

	// Start API server
	log.Printf("Starting API server on port %d", *apiPort)
	if err := http.ListenAndServe(fmt.Sprintf(":%d", *apiPort), nil); err != nil {
		log.Fatalf("Failed to start API server: %v", err)
	}
}
