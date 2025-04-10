package api

import (
	"database/sql"
	"fmt"
	"net/http"

	"github.com/qlpqlp/dogetracker/pkg/tracker"
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

// Start starts the API server
func (a *API) Start() error {
	// TODO: Implement API server
	return http.ListenAndServe(fmt.Sprintf(":%d", a.port), nil)
}
