// Package config loads ClickHouse connection settings from CLICKHOUSE_* env vars.
package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config holds the ClickHouse connection settings.
type Config struct {
	Host     string
	Port     int
	User     string
	Password string
	Database string
	Secure   bool
}

// Load reads the CLICKHOUSE_* environment variables, applying defaults.
func Load() (*Config, error) {
	port, err := envInt("CLICKHOUSE_PORT", 9000)
	if err != nil {
		return nil, err
	}
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("CLICKHOUSE_PORT: %d out of range (1-65535)", port)
	}
	secure, err := envBool("CLICKHOUSE_SECURE", false)
	if err != nil {
		return nil, err
	}
	return &Config{
		Host:     envString("CLICKHOUSE_HOST", "localhost"),
		Port:     port,
		User:     envString("CLICKHOUSE_USER", "default"),
		Password: envString("CLICKHOUSE_PASSWORD", ""),
		Database: envString("CLICKHOUSE_DATABASE", "default"),
		Secure:   secure,
	}, nil
}

func envString(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

func envInt(key string, def int) (int, error) {
	v, ok := os.LookupEnv(key)
	if !ok {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return n, nil
}

func envBool(key string, def bool) (bool, error) {
	v, ok := os.LookupEnv(key)
	if !ok {
		return def, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%s: %w", key, err)
	}
	return b, nil
}
