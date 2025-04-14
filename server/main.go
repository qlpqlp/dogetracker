package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/dogeorg/dogetracker/pkg/chaser"
	"github.com/dogeorg/dogetracker/pkg/core"
	"github.com/dogeorg/dogetracker/pkg/database"
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

	// Watch for new blocks.
	zmqTip, err := core.CoreZMQListener(ctx, config.zmqHost, config.zmqPort)
	if err != nil {
		log.Printf("CoreZMQListener: %v", err)
		os.Exit(1)
	}
	_ = chaser.NewTipChaser(ctx, zmqTip, blockchain).Listen(1, true)

	// Get the starting block hash if specified
	if *startBlock >= 0 {
		hash, err := blockchain.GetBlockHash(int64(*startBlock))
		if err != nil {
			log.Printf("Error getting block hash for height %d: %v", *startBlock, err)
			os.Exit(1)
		}
		log.Printf("Starting from block height %d (hash: %s)", *startBlock, hash)

		// Save the starting block as processed
		if err := db.SaveProcessedBlock(int64(*startBlock), hash); err != nil {
			log.Printf("Error saving processed block: %v", err)
			os.Exit(1)
		}
	}

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
