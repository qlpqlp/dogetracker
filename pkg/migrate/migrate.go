package migrate

import (
	"database/sql"
	"fmt"
	"io/ioutil"
	"log"
	"path/filepath"
	"sort"
	"strings"
)

// Migration represents a database migration
type Migration struct {
	Version string
	Up      string
	Down    string
}

// RunMigrations executes all pending migrations
func RunMigrations(db *sql.DB, migrationsDir string) error {
	// Create migrations table if it doesn't exist
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS migrations (
			version VARCHAR(255) PRIMARY KEY,
			applied_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create migrations table: %v", err)
	}

	// Get list of applied migrations
	applied := make(map[string]bool)
	rows, err := db.Query("SELECT version FROM migrations")
	if err != nil {
		return fmt.Errorf("failed to query applied migrations: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			return fmt.Errorf("failed to scan migration version: %v", err)
		}
		applied[version] = true
	}

	// Read migration files
	files, err := ioutil.ReadDir(migrationsDir)
	if err != nil {
		return fmt.Errorf("failed to read migrations directory: %v", err)
	}

	// Sort files by name (which should be the version)
	sort.Slice(files, func(i, j int) bool {
		return files[i].Name() < files[j].Name()
	})

	// Apply pending migrations
	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".sql") {
			continue
		}

		version := strings.TrimSuffix(file.Name(), ".sql")
		if applied[version] {
			continue
		}

		// Read migration file
		content, err := ioutil.ReadFile(filepath.Join(migrationsDir, file.Name()))
		if err != nil {
			return fmt.Errorf("failed to read migration file %s: %v", file.Name(), err)
		}

		// Split into up and down parts
		parts := strings.Split(string(content), "-- +migrate Down")
		if len(parts) != 2 {
			return fmt.Errorf("invalid migration file format in %s", file.Name())
		}

		upSQL := strings.TrimSpace(strings.TrimPrefix(parts[0], "-- +migrate Up"))
		if upSQL == "" {
			return fmt.Errorf("empty up migration in %s", file.Name())
		}

		// Execute migration
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("failed to begin transaction: %v", err)
		}

		_, err = tx.Exec(upSQL)
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("failed to execute migration %s: %v", version, err)
		}

		_, err = tx.Exec("INSERT INTO migrations (version) VALUES ($1)", version)
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("failed to record migration %s: %v", version, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("failed to commit migration %s: %v", version, err)
		}

		log.Printf("Applied migration %s", version)
	}

	return nil
}
