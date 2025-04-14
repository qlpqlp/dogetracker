package api

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"github.com/dogeorg/dogetracker/server/db"
)

var (
	dbConn   *sql.DB
	apiToken string
)

// SetDB sets the database connection
func SetDB(db *sql.DB) {
	dbConn = db
}

// SetToken sets the API token
func SetToken(token string) {
	apiToken = token
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
	// Check authentication
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	token := strings.TrimPrefix(authHeader, "Bearer ")
	if token != apiToken {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Parse request body
	var req struct {
		Address               string `json:"address"`
		RequiredConfirmations int    `json:"required_confirmations"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validate address
	if req.Address == "" {
		http.Error(w, "Address is required", http.StatusBadRequest)
		return
	}

	// Set default confirmations if not provided
	if req.RequiredConfirmations == 0 {
		req.RequiredConfirmations = 6
	}

	// Add address to database
	addr, err := db.GetOrCreateAddress(dbConn, req.Address)
	if err != nil {
		log.Printf("Failed to add address: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Return success response
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":                     addr.ID,
		"address":                addr.Address,
		"balance":                addr.Balance,
		"required_confirmations": addr.RequiredConfirmations,
		"created_at":             addr.CreatedAt,
	})
}
