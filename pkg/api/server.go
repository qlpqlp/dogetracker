package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/dogeorg/dogetracker/pkg/database"
)

type Server struct {
	db    *database.DB
	port  int
	token string
}

func NewServer(db *database.DB, port int, token string) *Server {
	return &Server{
		db:    db,
		port:  port,
		token: token,
	}
}

func (s *Server) authenticate(r *http.Request) bool {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return false
	}

	parts := strings.Split(auth, " ")
	if len(parts) != 2 || parts[0] != "Bearer" {
		return false
	}

	return parts[1] == s.token
}

type AddressResponse struct {
	Address        string                  `json:"address"`
	Balance        float64                 `json:"balance"`
	Transactions   []TransactionResponse   `json:"transactions"`
	UnspentOutputs []UnspentOutputResponse `json:"unspent_outputs"`
}

type TransactionResponse struct {
	TxHash        string  `json:"tx_hash"`
	Amount        float64 `json:"amount"`
	BlockHeight   int64   `json:"block_height"`
	Confirmations int     `json:"confirmations"`
	IsSpent       bool    `json:"is_spent"`
	CreatedAt     string  `json:"created_at"`
}

type UnspentOutputResponse struct {
	TxHash        string  `json:"tx_hash"`
	Amount        float64 `json:"amount"`
	BlockHeight   int64   `json:"block_height"`
	Confirmations int     `json:"confirmations"`
	CreatedAt     string  `json:"created_at"`
}

func (s *Server) handleAddress(w http.ResponseWriter, r *http.Request) {
	if !s.authenticate(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Extract address from URL path
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) != 4 {
		http.Error(w, "Invalid address", http.StatusBadRequest)
		return
	}
	address := parts[3]

	// Get address details
	var response AddressResponse
	response.Address = address

	// Get balance
	err := s.db.QueryRow(`
		SELECT COALESCE(SUM(amount), 0)
		FROM unspent_transactions ut
		JOIN addresses a ON ut.address_id = a.id
		WHERE a.address = $1
	`, address).Scan(&response.Balance)
	if err != nil {
		http.Error(w, fmt.Sprintf("Error getting balance: %v", err), http.StatusInternalServerError)
		return
	}

	// Get transactions
	rows, err := s.db.Query(`
		SELECT t.tx_hash, t.amount, t.block_height, t.confirmations, t.is_spent, t.created_at
		FROM transactions t
		JOIN addresses a ON t.address_id = a.id
		WHERE a.address = $1
		ORDER BY t.created_at DESC
	`, address)
	if err != nil {
		http.Error(w, fmt.Sprintf("Error getting transactions: %v", err), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var tx TransactionResponse
		err := rows.Scan(&tx.TxHash, &tx.Amount, &tx.BlockHeight, &tx.Confirmations, &tx.IsSpent, &tx.CreatedAt)
		if err != nil {
			http.Error(w, fmt.Sprintf("Error scanning transaction: %v", err), http.StatusInternalServerError)
			return
		}
		response.Transactions = append(response.Transactions, tx)
	}

	// Get unspent outputs
	rows, err = s.db.Query(`
		SELECT ut.tx_hash, ut.amount, ut.block_height, ut.confirmations, ut.created_at
		FROM unspent_transactions ut
		JOIN addresses a ON ut.address_id = a.id
		WHERE a.address = $1
		ORDER BY ut.created_at DESC
	`, address)
	if err != nil {
		http.Error(w, fmt.Sprintf("Error getting unspent outputs: %v", err), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var utxo UnspentOutputResponse
		err := rows.Scan(&utxo.TxHash, &utxo.Amount, &utxo.BlockHeight, &utxo.Confirmations, &utxo.CreatedAt)
		if err != nil {
			http.Error(w, fmt.Sprintf("Error scanning unspent output: %v", err), http.StatusInternalServerError)
			return
		}
		response.UnspentOutputs = append(response.UnspentOutputs, utxo)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (s *Server) Start() error {
	http.HandleFunc("/api/address/", s.handleAddress)

	addr := fmt.Sprintf(":%d", s.port)
	log.Printf("Starting API server on %s", addr)
	return http.ListenAndServe(addr, nil)
}
