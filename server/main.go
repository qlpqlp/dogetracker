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
	"github.com/qlpqlp/dogetracker/pkg/walker"
	"github.com/qlpqlp/dogetracker/server/api"
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
	dbName := flag.String("db-name", getEnvOrDefault("DB_NAME", "dogewalker"), "PostgreSQL database name")

	// API flags
	apiPort := flag.Int("api-port", getEnvIntOrDefault("API_PORT", 8080), "API server port")
	apiToken := flag.String("api-token", getEnvOrDefault("API_TOKEN", ""), "API bearer token for authentication")

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

	// Connect to PostgreSQL
	dbConnStr := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		config.dbHost, config.dbPort, config.dbUser, config.dbPass, config.dbName)
	db, err := sql.Open("postgres", dbConnStr)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

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
		ResumeFromBlock: "0e0bd6be24f5f426a505694bf46f60301a3a08dfdfda13854fdfe0ce7d455d6f",
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
					// TODO: Process block transactions and update database
					// This will involve:
					// 1. Checking each transaction for tracked addresses
					// 2. Updating transaction records
					// 3. Updating unspent outputs
					// 4. Updating address balances
				} else {
					log.Printf("Undoing to: %v (%v)", b.Undo.ResumeFromBlock, b.Undo.LastValidHeight)
					// TODO: Handle chain reorganization
					// This will involve:
					// 1. Removing transactions from invalid blocks
					// 2. Updating unspent outputs
					// 3. Recalculating balances
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
