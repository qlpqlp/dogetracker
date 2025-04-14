package config

import (
	"flag"
	"fmt"
)

type Config struct {
	APIPort  int
	APIToken string
	DBHost   string
	DBPort   int
	DBUser   string
	DBPass   string
	DBName   string
	MinConfs int
}

func LoadConfig() (*Config, error) {
	cfg := &Config{}

	flag.IntVar(&cfg.APIPort, "api-port", 470, "API server port")
	flag.StringVar(&cfg.APIToken, "api-token", "", "API authentication token")
	flag.StringVar(&cfg.DBHost, "db-host", "localhost", "Database host")
	flag.IntVar(&cfg.DBPort, "db-port", 5432, "Database port")
	flag.StringVar(&cfg.DBUser, "db-user", "postgres", "Database user")
	flag.StringVar(&cfg.DBPass, "db-pass", "postgres", "Database password")
	flag.StringVar(&cfg.DBName, "db-name", "dogetracker", "Database name")
	flag.IntVar(&cfg.MinConfs, "min-confs", 6, "Minimum confirmations required")

	flag.Parse()

	if cfg.APIToken == "" {
		return nil, fmt.Errorf("api-token is required")
	}

	return cfg, nil
}
