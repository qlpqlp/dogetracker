package database

import (
	"time"
)

type Address struct {
	ID        int64     `json:"id"`
	Address   string    `json:"address"`
	CreatedAt time.Time `json:"created_at"`
}

type Transaction struct {
	ID            int64     `json:"id"`
	TxHash        string    `json:"tx_hash"`
	AddressID     int64     `json:"address_id"`
	Amount        float64   `json:"amount"`
	BlockHeight   int64     `json:"block_height"`
	Confirmations int       `json:"confirmations"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type UnspentTransaction struct {
	ID            int64     `json:"id"`
	TxHash        string    `json:"tx_hash"`
	AddressID     int64     `json:"address_id"`
	Amount        float64   `json:"amount"`
	BlockHeight   int64     `json:"block_height"`
	Confirmations int       `json:"confirmations"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type ProcessedBlock struct {
	ID          int64     `json:"id"`
	Height      int64     `json:"height"`
	Hash        string    `json:"hash"`
	ProcessedAt time.Time `json:"processed_at"`
}
