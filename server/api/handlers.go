package api

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
)

var dbConn *sql.DB

// SetDB sets the database connection for the API handlers
func SetDB(database *sql.DB) {
	dbConn = database
}

// TrackAddressRequest represents a request to track a new address
type TrackAddressRequest struct {
	Address               string `json:"address"`
	RequiredConfirmations int    `json:"required_confirmations"`
}

// TrackAddressResponse represents the response for tracking a new address
type TrackAddressResponse struct {
	Address               string  `json:"address"`
	Balance               float64 `json:"balance"`
	RequiredConfirmations int     `json:"required_confirmations"`
}

// TrackAddressHandler handles requests to track a new address
func TrackAddressHandler(w http.ResponseWriter, r *http.Request) {
	// Parse request body
	var req TrackAddressRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validate address
	if req.Address == "" {
		http.Error(w, "Address is required", http.StatusBadRequest)
		return
	}

	// Validate required confirmations
	if req.RequiredConfirmations < 0 {
		http.Error(w, "Required confirmations must be non-negative", http.StatusBadRequest)
		return
	}

	// Insert address into database
	var balance float64
	err := dbConn.QueryRow(`
		INSERT INTO tracked_addresses (address, balance, required_confirmations)
		VALUES ($1, 0, $2)
		ON CONFLICT (address) DO UPDATE
		SET required_confirmations = $2
		RETURNING balance
	`, req.Address, req.RequiredConfirmations).Scan(&balance)
	if err != nil {
		log.Printf("Failed to track address %s: %v", req.Address, err)
		http.Error(w, "Failed to track address", http.StatusInternalServerError)
		return
	}

	// Return response
	resp := TrackAddressResponse{
		Address:               req.Address,
		Balance:               balance,
		RequiredConfirmations: req.RequiredConfirmations,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
