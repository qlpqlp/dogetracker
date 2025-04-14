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
	IsSpent       bool      `json:"is_spent"`
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
