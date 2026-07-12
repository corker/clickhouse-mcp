// Package config loads server and ClickHouse settings from CLICKHOUSE_* and MCP_*
// env vars.
package config

import (
	"fmt"
	"os"
	"strconv"
)

// Transport selects how the server talks to MCP clients.
type Transport string

const (
	// TransportStdio serves one client over stdin/stdout (the v0.1 default). Auth
	// does not apply — credentials come from the environment (MCP spec).
	TransportStdio Transport = "stdio"
	// TransportHTTP serves many clients over streamable HTTP; auth applies.
	TransportHTTP Transport = "http"
)

// AuthMode selects how HTTP requests are authenticated (ADR-0007). Only stdio
// and AuthOff are functional today; bearer/broker land in later v0.2 layers.
type AuthMode string

const (
	// AuthOff serves without authentication — local dev / MCP Inspector.
	AuthOff AuthMode = "off"
	// AuthBearer validates a bearer token on every request (resource-server core).
	AuthBearer AuthMode = "bearer"
	// AuthBroker is bearer plus the interactive metadata/DCR/proxy layer (Entra).
	AuthBroker AuthMode = "broker"
)

type Config struct {
	Host     string
	Port     int
	User     string
	Password string
	Database string
	Secure   bool

	Server ServerConfig
}

// ServerConfig is the MCP-facing transport and auth configuration, distinct from
// the ClickHouse connection above.
type ServerConfig struct {
	Transport Transport
	// HTTPAddr is the listen address for TransportHTTP (host:port), e.g. ":8080".
	// Not validated here — a malformed value fails at net.Listen in main.
	HTTPAddr string
	AuthMode AuthMode
}

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
	srv, err := loadServer()
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
		Server:   srv,
	}, nil
}

func loadServer() (ServerConfig, error) {
	transport := Transport(envString("MCP_TRANSPORT", string(TransportStdio)))
	switch transport {
	case TransportStdio, TransportHTTP:
	default:
		return ServerConfig{}, fmt.Errorf("MCP_TRANSPORT: unknown transport %q (want stdio or http)", transport)
	}

	authMode := AuthMode(envString("MCP_AUTH_MODE", string(AuthOff)))
	switch authMode {
	case AuthOff, AuthBearer, AuthBroker:
	default:
		return ServerConfig{}, fmt.Errorf("MCP_AUTH_MODE: unknown mode %q (want off, bearer, or broker)", authMode)
	}

	// bearer/broker are valid modes (above) but not wired yet; fail loudly rather
	// than serve unauthenticated while the operator believes auth is on. Narrow
	// this guard as each mode's request-path lands, or a configured mode will be
	// accepted here but silently do nothing.
	if authMode != AuthOff {
		return ServerConfig{}, fmt.Errorf("MCP_AUTH_MODE=%s is not implemented yet; only off is available in this build", authMode)
	}

	return ServerConfig{
		Transport: transport,
		HTTPAddr:  envString("MCP_HTTP_ADDR", ":8080"),
		AuthMode:  authMode,
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
