package main

import (
	"log"
	"os"

	"github.com/dogeorg/dogetracker/pkg/doge"
)

func main() {
	logger := log.New(os.Stdout, "SPV: ", log.LstdFlags)

	// Create a new SPV node
	node, err := doge.NewSPVNode(logger)
	if err != nil {
		logger.Fatalf("Failed to create SPV node: %v", err)
	}

	// Connect to a Dogecoin node
	if err := node.Connect("seed.dogecoin.com:22556"); err != nil {
		logger.Fatalf("Failed to connect: %v", err)
	}

	// Start the node
	if err := node.Start(); err != nil {
		logger.Fatalf("Failed to start node: %v", err)
	}

	// Wait for the node to stop
	<-node.Done()
}
