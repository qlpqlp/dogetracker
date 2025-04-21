package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dogeorg/dogetracker/pkg/api"
	"github.com/dogeorg/dogetracker/pkg/chaser"
	"github.com/dogeorg/dogetracker/pkg/core"
	"github.com/dogeorg/dogetracker/pkg/database"
	"github.com/dogeorg/dogetracker/pkg/spec"
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

func processBlock(ctx context.Context, db *database.DB, blockchain spec.Blockchain, height int64) error {
	// Get block hash
	hash, err := blockchain.GetBlockHash(height)
	if err != nil {
		return fmt.Errorf("error getting block hash: %v", err)
	}

	log.Printf("Processing block %d (%s)", height, hash)

	// Get tracked addresses
	addresses, err := db.GetTrackedAddresses()
	if err != nil {
		return fmt.Errorf("error getting tracked addresses: %v", err)
	}

	// Process each address
	for _, addr := range addresses {
		// Get raw transactions for this address
		txs, err := blockchain.GetAddressTransactions(addr, height)
		if err != nil {
			log.Printf("Error getting transactions for address %s: %v", addr, err)
			continue
		}

		// Process each transaction
		for _, tx := range txs {
			// If transaction is spent, mark it as spent and insert the spent transaction
			if tx.IsSpent {
				err = db.MarkTransactionSpent(tx.Hash, tx.ToAddress)
				if err != nil {
					log.Printf("Error marking transaction %s as spent: %v", tx.Hash, err)
					continue
				}
			} else {
				// For non-spent transactions, insert into transactions table
				err = db.InsertTransaction(tx.Hash, addr, tx.Amount, height, tx.FromAddress, tx.ToAddress)
				if err != nil {
					log.Printf("Error inserting transaction %s: %v", tx.Hash, err)
					continue
				}

				// Only add to unspent transactions if it's a received transaction (positive amount)
				if tx.Amount > 0 {
					err = db.InsertUnspentTransaction(tx.Hash, addr, tx.Amount, height)
					if err != nil {
						log.Printf("Error inserting unspent transaction %s: %v", tx.Hash, err)
						continue
					}
				}
			}

			// Update address balance
			balance, err := db.GetAddressBalance(addr)
			if err != nil {
				log.Printf("Error getting balance for address %s: %v", addr, err)
				continue
			}
			err = db.UpdateAddressBalance(addr, balance)
			if err != nil {
				log.Printf("Error updating balance for address %s: %v", addr, err)
				continue
			}
		}
	}

	// Save processed block
	err = db.SaveProcessedBlock(height, hash)
	if err != nil {
		return fmt.Errorf("error saving processed block: %v", err)
	}

	return nil
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

	// Start API server
	apiServer := api.NewServer(db, config.apiPort, config.apiToken)
	go func() {
		if err := apiServer.Start(); err != nil {
			log.Printf("Error starting API server: %v", err)
			os.Exit(1)
		}
	}()

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

	// Set up ZMQ listener for new blocks and transactions
	zmqTip, zmqTx, err := core.CoreZMQListener(ctx, config.zmqHost, config.zmqPort)
	if err != nil {
		log.Printf("CoreZMQListener: %v", err)
		os.Exit(1)
	}
	_ = chaser.NewTipChaser(ctx, zmqTip, blockchain).Listen(1, true)

	// Process transactions in real-time
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case tx := <-zmqTx:
				// Get tracked addresses
				addresses, err := db.GetTrackedAddresses()
				if err != nil {
					log.Printf("Error getting tracked addresses: %v", err)
					continue
				}

				// Check if any of our tracked addresses are involved in this transaction
				for _, addr := range addresses {
					// Check inputs (spent transactions)
					for _, vin := range tx.Vin {
						if vin.TxID != "" {
							// Get the previous transaction to check if it was to our address
							var prevTx struct {
								Vout []struct {
									ScriptPubKey struct {
										Addresses []string `json:"addresses"`
									} `json:"scriptPubKey"`
								} `json:"vout"`
							}
							err := blockchain.Request("getrawtransaction", []any{vin.TxID, 1}, &prevTx)
							if err != nil {
								continue
							}

							// Check if the spent output was to our address
							if vin.Vout < len(prevTx.Vout) {
								for _, prevAddr := range prevTx.Vout[vin.Vout].ScriptPubKey.Addresses {
									if prevAddr == addr {
										// This transaction is spending our output
										err = db.MarkTransactionSpent(vin.TxID, tx.Vout[0].ScriptPubKey.Addresses[0])
										if err != nil {
											log.Printf("Error marking transaction %s as spent: %v", vin.TxID, err)
										}
									}
								}
							}
						}
					}

					// Check outputs (received transactions)
					for _, vout := range tx.Vout {
						if vout.ScriptPubKey.Addresses != nil {
							for _, toAddr := range vout.ScriptPubKey.Addresses {
								if toAddr == addr {
									// This is a transaction to our address
									// Get the from address from the inputs
									var fromAddress string
									if len(tx.Vin) > 0 && tx.Vin[0].TxID != "" {
										var prevTx struct {
											Vout []struct {
												ScriptPubKey struct {
													Addresses []string `json:"addresses"`
												} `json:"scriptPubKey"`
											} `json:"vout"`
										}
										err := blockchain.Request("getrawtransaction", []any{tx.Vin[0].TxID, 1}, &prevTx)
										if err == nil && len(prevTx.Vout) > tx.Vin[0].Vout {
											if len(prevTx.Vout[tx.Vin[0].Vout].ScriptPubKey.Addresses) > 0 {
												fromAddress = prevTx.Vout[tx.Vin[0].Vout].ScriptPubKey.Addresses[0]
											}
										}
									}

									// Insert the transaction
									err = db.InsertTransaction(tx.TxID, addr, vout.Value, 0, fromAddress, toAddr)
									if err != nil {
										log.Printf("Error inserting transaction %s: %v", tx.TxID, err)
										continue
									}

									// Add to unspent transactions
									err = db.InsertUnspentTransaction(tx.TxID, addr, vout.Value, 0)
									if err != nil {
										log.Printf("Error inserting unspent transaction %s: %v", tx.TxID, err)
										continue
									}

									// Update address balance
									balance, err := db.GetAddressBalance(addr)
									if err != nil {
										log.Printf("Error getting balance for address %s: %v", addr, err)
										continue
									}
									err = db.UpdateAddressBalance(addr, balance)
									if err != nil {
										log.Printf("Error updating balance for address %s: %v", addr, err)
										continue
									}
								}
							}
						}
					}
				}
			}
		}
	}()

	// Process blocks in a separate goroutine
	go func() {
		currentHeight := int64(*startBlock)
		ticker := time.NewTicker(5 * time.Second) // Check for new blocks every 5 seconds
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// Get current block height
				blockCount, err := blockchain.GetBlockCount()
				if err != nil {
					log.Printf("Error getting block count: %v", err)
					continue
				}

				// Process all blocks up to the current height
				for height := currentHeight; height <= blockCount; height++ {
					if err := processBlock(ctx, db, blockchain, height); err != nil {
						log.Printf("Error processing block %d: %v", height, err)
						continue
					}
					currentHeight = height + 1
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
