package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/qlpqlp/dogetracker/server/db"
)

type Server struct {
	db       *sql.DB
	apiToken string
}

func NewServer(db *sql.DB, apiToken string) *Server {
	return &Server{
		db:       db,
		apiToken: apiToken,
	}
}

func (s *Server) Start(port int) error {
	// Initialize database schema
	if err := db.InitDB(s.db); err != nil {
		return err
	}

	// Setup routes
	http.HandleFunc("/api/track", s.authMiddleware(s.handleTrackAddress))
	http.HandleFunc("/api/address/", s.authMiddleware(s.handleGetAddress))

	return http.ListenAndServe(fmt.Sprintf(":%d", port), nil)
}

func (s *Server) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			http.Error(w, "Authorization header required", http.StatusUnauthorized)
			return
		}

		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || parts[0] != "Bearer" || parts[1] != s.apiToken {
			http.Error(w, "Invalid authorization token", http.StatusUnauthorized)
			return
		}

		next(w, r)
	}
}

type TrackRequest struct {
	Address string `json:"address"`
}

type AddressResponse struct {
	Address        *db.TrackedAddress `json:"address"`
	Transactions   []db.Transaction   `json:"transactions"`
	UnspentOutputs []db.UnspentOutput `json:"unspent_outputs"`
}

func (s *Server) handleTrackAddress(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req TrackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Get or create address and return details
	addr, transactions, unspentOutputs, err := db.GetAddressDetails(s.db, req.Address)
	if err != nil {
		http.Error(w, "Error processing request", http.StatusInternalServerError)
		return
	}

	response := AddressResponse{
		Address:        addr,
		Transactions:   transactions,
		UnspentOutputs: unspentOutputs,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (s *Server) handleGetAddress(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract address from URL path
	address := strings.TrimPrefix(r.URL.Path, "/api/address/")
	if address == "" {
		http.Error(w, "Address required", http.StatusBadRequest)
		return
	}

	// Get address details
	addr, transactions, unspentOutputs, err := db.GetAddressDetails(s.db, address)
	if err != nil {
		http.Error(w, "Error processing request", http.StatusInternalServerError)
		return
	}

	response := AddressResponse{
		Address:        addr,
		Transactions:   transactions,
		UnspentOutputs: unspentOutputs,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
