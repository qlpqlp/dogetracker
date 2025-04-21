package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/dogeorg/dogetracker/pkg/database"
)

type Server struct {
	db    *database.DB
	port  int
	token string
}

type TrackRequest struct {
	Address               string `json:"address"`
	RequiredConfirmations int    `json:"required_confirmations"`
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
		SELECT t.tx_hash, t.amount, t.block_height, t.confirmations, t.created_at
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
		err := rows.Scan(&tx.TxHash, &tx.Amount, &tx.BlockHeight, &tx.Confirmations, &tx.CreatedAt)
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

// isValidAddress checks if the given string is a valid Dogecoin address
func isValidAddress(address string) bool {
	// Basic validation - Dogecoin addresses start with 'D' and are 34 characters long
	if len(address) != 34 || !strings.HasPrefix(address, "D") {
		return false
	}
	// TODO: Add more thorough validation if needed
	return true
}

func (s *Server) handleTrack(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Check authorization
	if !s.authenticate(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Parse request body
	var req struct {
		Address               string `json:"address"`
		RequiredConfirmations int64  `json:"required_confirmations"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validate address
	if !isValidAddress(req.Address) {
		http.Error(w, "Invalid address", http.StatusBadRequest)
		return
	}

	// Validate required confirmations
	if req.RequiredConfirmations < 1 {
		req.RequiredConfirmations = 1 // Default to 1 confirmation if not specified
	}

	// Add address to database
	_, err := s.db.Exec(`
		INSERT INTO addresses (address, required_confirmations)
		VALUES ($1, $2)
		ON CONFLICT (address) DO UPDATE
		SET required_confirmations = $2, updated_at = NOW()
	`, req.Address, req.RequiredConfirmations)
	if err != nil {
		http.Error(w, "Error tracking address", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "success",
		"message": "Address tracked successfully",
	})
}

type AddressInfo struct {
	Address        string          `json:"address"`
	Balance        float64         `json:"balance"`
	Transactions   []Transaction   `json:"transactions"`
	UnspentOutputs []UnspentOutput `json:"unspent_outputs"`
}

type Transaction struct {
	TxHash        string    `json:"tx_hash"`
	Amount        float64   `json:"amount"`
	BlockHeight   int64     `json:"block_height"`
	Confirmations int       `json:"confirmations"`
	CreatedAt     time.Time `json:"created_at"`
}

type UnspentOutput struct {
	TxHash        string    `json:"tx_hash"`
	Amount        float64   `json:"amount"`
	BlockHeight   int64     `json:"block_height"`
	Confirmations int       `json:"confirmations"`
	CreatedAt     time.Time `json:"created_at"`
}

func (s *Server) handleGetAddress(w http.ResponseWriter, r *http.Request) {
	// Check authorization
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	token := strings.TrimPrefix(authHeader, "Bearer ")
	if token != s.token {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Get address from URL path
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) != 4 {
		http.Error(w, "Invalid address", http.StatusBadRequest)
		return
	}
	address := parts[3]

	// Get address info
	var info AddressInfo
	info.Address = address

	// Get address ID
	var addressID int64
	err := s.db.QueryRow("SELECT id FROM addresses WHERE address = $1", address).Scan(&addressID)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Address not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Get balance
	err = s.db.QueryRow(`
		SELECT COALESCE(SUM(amount), 0)
		FROM unspent_transactions
		WHERE address_id = $1
	`, addressID).Scan(&info.Balance)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Get transactions
	rows, err := s.db.Query(`
		SELECT tx_hash, amount, block_height, confirmations, created_at
		FROM transactions
		WHERE address_id = $1
		ORDER BY created_at DESC
	`, addressID)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var tx Transaction
		err := rows.Scan(&tx.TxHash, &tx.Amount, &tx.BlockHeight, &tx.Confirmations, &tx.CreatedAt)
		if err != nil {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		info.Transactions = append(info.Transactions, tx)
	}

	// Get unspent outputs
	rows, err = s.db.Query(`
		SELECT tx_hash, amount, block_height, confirmations, created_at
		FROM unspent_transactions
		WHERE address_id = $1
		ORDER BY created_at DESC
	`, addressID)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var utxo UnspentOutput
		err := rows.Scan(&utxo.TxHash, &utxo.Amount, &utxo.BlockHeight, &utxo.Confirmations, &utxo.CreatedAt)
		if err != nil {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		info.UnspentOutputs = append(info.UnspentOutputs, utxo)
	}

	// Return response
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(info)
}

func (s *Server) Start() error {
	http.HandleFunc("/api/track", s.handleTrack)
	http.HandleFunc("/api/address/", s.handleGetAddress)
	log.Printf("Starting API server on port %d", s.port)
	return http.ListenAndServe(fmt.Sprintf(":%d", s.port), nil)
}
