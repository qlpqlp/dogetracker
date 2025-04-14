package api

import (
	"database/sql"
	"fmt"
	"net/http"

	"github.com/dogeorg/dogetracker/pkg/tracker"
)

// API represents the API server
type API struct {
	db      *sql.DB
	tracker *tracker.Tracker
	port    int
	token   string
}

// NewAPI creates a new API server
func NewAPI(db *sql.DB, tracker *tracker.Tracker, port int, token string) *API {
	return &API{
		db:      db,
		tracker: tracker,
		port:    port,
		token:   token,
	}
}

// ServeHTTP implements http.Handler
func (a *API) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Verify API token
	if token := r.Header.Get("X-API-Token"); token != a.token {
		http.Error(w, "Invalid API token", http.StatusUnauthorized)
		return
	}

	// TODO: Implement API endpoints
	http.Error(w, "Not implemented", http.StatusNotImplemented)
}

// Start starts the API server
func (a *API) Start() error {
	return http.ListenAndServe(fmt.Sprintf(":%d", a.port), a)
}
