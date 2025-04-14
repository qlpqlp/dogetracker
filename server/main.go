package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/dogeorg/doge"
	"github.com/dogeorg/dogetracker/pkg/chaser"
	"github.com/dogeorg/dogetracker/pkg/core"
	"github.com/dogeorg/dogetracker/pkg/walker"
)

type Config struct {
	rpcHost   string
	rpcPort   int
	rpcUser   string
	rpcPass   string
	zmqHost   string
	zmqPort   int
	batchSize int
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

	// Parse command line flags
	flag.Parse()

	config := Config{
		rpcHost: *rpcHost,
		rpcPort: *rpcPort,
		rpcUser: *rpcUser,
		rpcPass: *rpcPass,
		zmqHost: *zmqHost,
		zmqPort: *zmqPort,
	}

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

	// Get the starting block hash if specified
	var startBlockHash string
	if *startBlock >= 0 {
		hash, err := blockchain.GetBlockHash(int64(*startBlock))
		if err != nil {
			log.Printf("Error getting block hash for height %d: %v", *startBlock, err)
			os.Exit(1)
		}
		startBlockHash = hash
		log.Printf("Starting from block height %d (hash: %s)", *startBlock, startBlockHash)
	}

	// Walk the blockchain.
	blocks, err := walker.WalkTheDoge(ctx, walker.WalkerOptions{
		Chain:           &doge.DogeMainNetChain,
		ResumeFromBlock: startBlockHash, // Empty string means start from genesis block
		Client:          blockchain,
		TipChanged:      tipChanged,
	})
	if err != nil {
		log.Printf("WalkTheDoge: %v", err)
		os.Exit(1)
	}

	// Log new blocks.
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case b := <-blocks:
				if b.Block != nil {
					log.Printf("block: %v (%v)", b.Block.Hash, b.Block.Height)
				} else {
					log.Printf("undo to: %v (%v)", b.Undo.ResumeFromBlock, b.Undo.LastValidHeight)
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
