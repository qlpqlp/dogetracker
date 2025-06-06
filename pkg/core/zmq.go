package core

import (
	"context"
	"encoding/hex"
	"fmt"
	"log"
	"syscall"
	"time"

	"github.com/pebbe/zmq4"
)

/*
 * CoreZMQListener listens to Core Node ZMQ Interface.
 *
 * newTip channel announces whenever Core finds a new Best Block Hash (Tip change)
 */
func CoreZMQListener(ctx context.Context, host string, port int) (<-chan string, error) {
	newTip := make(chan string, 100)
	nodeAddress := fmt.Sprintf("tcp://%s:%d", host, port)

	// Connect to Core
	sock, err := zmq4.NewSocket(zmq4.SUB)
	if err != nil {
		return nil, err
	}
	sock.SetRcvtimeo(2 * time.Second) // for shutdown
	err = sock.Connect(nodeAddress)
	if err != nil {
		return nil, err
	}

	// Subscribe to both block and transaction events
	err = sock.SetSubscribe("hashblock")
	if err != nil {
		return nil, err
	}
	err = sock.SetSubscribe("hashtx")
	if err != nil {
		return nil, err
	}
	err = sock.SetSubscribe("rawtx")
	if err != nil {
		return nil, err
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
				// We don't need to do anything with the transaction here
				// as it will be processed when the block containing it is processed
			case "rawtx":
				// This is a raw transaction that might be spending our outputs
				txid := hex.EncodeToString(msg[1])
				log.Printf("Raw transaction received: %s", txid)
				// We'll process this transaction to check if it spends any of our outputs
				// The transaction processing will happen in the block processing
			default:
				log.Printf("Unknown ZMQ message type: %s", tag)
			}
		}
	}()
	return newTip, nil
}
