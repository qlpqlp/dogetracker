package tracker

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/dogeorg/dogetracker/pkg/database"
	"github.com/pebbe/zmq4"
)

type MempoolTracker struct {
	socket    *zmq4.Socket
	db        *database.DB
	addresses map[string]bool
}

func NewMempoolTracker(db *database.DB) (*MempoolTracker, error) {
	socket, err := zmq4.NewSocket(zmq4.SUB)
	if err != nil {
		return nil, fmt.Errorf("error creating ZMQ socket: %v", err)
	}

	err = socket.Connect("tcp://127.0.0.1:28332")
	if err != nil {
		return nil, fmt.Errorf("error connecting to ZMQ: %v", err)
	}

	err = socket.SetSubscribe("hashtx")
	if err != nil {
		return nil, fmt.Errorf("error setting ZMQ subscription: %v", err)
	}

	return &MempoolTracker{
		socket:    socket,
		db:        db,
		addresses: make(map[string]bool),
	}, nil
}

func (mt *MempoolTracker) AddAddress(address string) {
	mt.addresses[address] = true
	log.Printf("Added address for mempool tracking: %s", address)
}

type MempoolTransaction struct {
	TxHash string `json:"txid"`
}

func (mt *MempoolTracker) Start(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			msg, err := mt.socket.Recv(0)
			if err != nil {
				log.Printf("Error receiving ZMQ message: %v", err)
				continue
			}

			var tx MempoolTransaction
			if err := json.Unmarshal([]byte(msg), &tx); err != nil {
				log.Printf("Error unmarshaling transaction: %v", err)
				continue
			}

			log.Printf("Transaction detected in mempool: %s", tx.TxHash)
		}
	}
}
