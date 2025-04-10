package tracker

import (
	"database/sql"
	"fmt"

	_ "github.com/lib/pq"
)

// NewDB creates a new database connection
func NewDB(host string, port int, user string, password string, dbname string) (*sql.DB, error) {
	connStr := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		host, port, user, password, dbname)

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("error opening database: %v", err)
	}

	err = db.Ping()
	if err != nil {
		return nil, fmt.Errorf("error connecting to database: %v", err)
	}

	return db, nil
}
