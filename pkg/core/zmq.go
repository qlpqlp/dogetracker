package core

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"syscall"
	"time"

	"github.com/pebbe/zmq4"
)

// Transaction represents a raw transaction from ZMQ
type RawTransaction struct {
	TxID string
	Vin  []struct {
		TxID string `json:"txid"`
		Vout int    `json:"vout"`
	} `json:"vin"`
	Vout []struct {
		Value        float64 `json:"value"`
		ScriptPubKey struct {
			Addresses []string `json:"addresses"`
		} `json:"scriptPubKey"`
	} `json:"vout"`
	BlockHash string `json:"blockhash,omitempty"`
	Time      int64  `json:"time,omitempty"`
}

/*
 * CoreZMQListener listens to Core Node ZMQ Interface.
 *
 * newTip channel announces whenever Core finds a new Best Block Hash (Tip change)
 * newTx channel announces whenever Core finds a new transaction
 */
func CoreZMQListener(ctx context.Context, host string, port int) (<-chan string, <-chan RawTransaction, error) {
	newTip := make(chan string, 100)
	newTx := make(chan RawTransaction, 100)
	nodeAddress := fmt.Sprintf("tcp://%s:%d", host, port)

	// Connect to Core
	sock, err := zmq4.NewSocket(zmq4.Type(zmq4.SUB))
	if err != nil {
		return nil, nil, err
	}
	sock.SetRcvtimeo(2 * time.Second) // for shutdown
	err = sock.Connect(nodeAddress)
	if err != nil {
		return nil, nil, err
	}

	// Subscribe to both block and transaction events
	err = sock.SetSubscribe("hashblock")
	if err != nil {
		return nil, nil, err
	}
	err = sock.SetSubscribe("hashtx")
	if err != nil {
		return nil, nil, err
	}
	err = sock.SetSubscribe("rawtx")
	if err != nil {
		return nil, nil, err
	}

	go func() {
		for {
			// Check for shutdown
			select {
			case <-ctx.Done():
				sock.Close()
				return
			default:
			}

			msg, err := sock.RecvMessageBytes(0)
			if err != nil {
				switch err := err.(type) {
				case zmq4.Errno:
					if err == zmq4.Errno(syscall.ETIMEDOUT) {
						// handle timeouts by looping again
						continue
					} else if err == zmq4.Errno(syscall.EAGAIN) {
						continue
					} else {
						// handle other ZeroMQ error codes
						log.Printf("ZMQ err: %s", err)
						continue
					}
				default:
					// handle other Go errors
					log.Printf("ZMQ err: %s", err)
					continue
				}
			}
			tag := string(msg[0])
			switch tag {
			case "hashblock":
				id := hex.EncodeToString(msg[1])
				newTip <- id
			case "hashtx":
				txid := hex.EncodeToString(msg[1])
				log.Printf("New transaction detected: %s", txid)
			case "rawtx":
				// Parse the raw transaction
				var rawTx RawTransaction
				if err := json.Unmarshal(msg[1], &rawTx); err != nil {
					log.Printf("Error parsing raw transaction: %v", err)
					continue
				}
				rawTx.TxID = hex.EncodeToString(msg[1])
				newTx <- rawTx
			default:
				log.Printf("Unknown ZMQ message type: %s", tag)
			}
		}
	}()
	return newTip, newTx, nil
}
