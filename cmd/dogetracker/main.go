package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/dogeorg/doge"
	"github.com/dogeorg/dogetracker/pkg/api"
	"github.com/dogeorg/dogetracker/pkg/config"
	"github.com/dogeorg/dogetracker/pkg/database"
	"github.com/dogeorg/dogetracker/pkg/tracker"
)

func main() {
	// Load configuration
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Error loading config: %v", err)
	}

	// Initialize database
	db, err := database.NewDB(cfg.DBHost, cfg.DBPort, cfg.DBUser, cfg.DBPass, cfg.DBName)
	if err != nil {
		log.Fatalf("Error connecting to database: %v", err)
	}
	defer db.Close()

	// Initialize database schema
	if err := db.InitSchema(); err != nil {
		log.Fatalf("Error initializing database schema: %v", err)
	}

	// Initialize Dogecoin client
	client, err := doge.NewClient("http://localhost:22555", "rpcuser", "rpcpass")
	if err != nil {
		log.Fatalf("Error creating Dogecoin client: %v", err)
	}

	// Initialize trackers
	blockTracker := tracker.NewBlockTracker(client, db, cfg.MinConfs)
	mempoolTracker, err := tracker.NewMempoolTracker(db)
	if err != nil {
		log.Fatalf("Error creating mempool tracker: %v", err)
	}

	// Initialize API server
	apiServer := api.NewServer(db, cfg.APIPort, cfg.APIToken)

	// Create context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start all components
	go func() {
		if err := blockTracker.Start(ctx); err != nil {
			log.Printf("Error in block tracker: %v", err)
		}
	}()

	go func() {
		if err := mempoolTracker.Start(ctx); err != nil {
			log.Printf("Error in mempool tracker: %v", err)
		}
	}()

	go func() {
		if err := apiServer.Start(); err != nil {
			log.Printf("Error in API server: %v", err)
		}
	}()

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Println("Shutting down...")
	cancel()
}
