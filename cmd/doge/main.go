package main

import (
	"flag"

	"github.com/dogeorg/dogetracker/pkg/core"
)

func main() {
	// Parse command line flags
	rpcHost := flag.String("rpc-host", "127.0.0.1", "Dogecoin RPC host")
	rpcPort := flag.Int("rpc-port", 22555, "Dogecoin RPC port")
	rpcUser := flag.String("rpc-user", "dogecoin", "Dogecoin RPC username")
	rpcPass := flag.String("rpc-pass", "dogecoin", "Dogecoin RPC password")

	flag.Parse()

	// Create RPC client
	client := core.NewCoreRPCClient(*rpcHost, *rpcPort, *rpcUser, *rpcPass)

	// TODO: Initialize database and tracker
	// TODO: Start processing blocks and transactions
}
