// Package config loads server and ClickHouse settings from CLICKHOUSE_* and MCP_*
// env vars.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Transport string

const (
	// Auth does not apply to stdio — credentials come from the environment (MCP
	// spec); it serves a single client. HTTP serves many and auth applies.
	TransportStdio Transport = "stdio"
	TransportHTTP  Transport = "http"
)

// AuthMode selects how HTTP requests are authenticated (ADR-0007). off and bearer
// are wired; broker (the interactive metadata/DCR layer) is not yet.
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

// ServerConfig is the MCP transport/auth config (the ClickHouse connection is
// the top-level Config fields).
type ServerConfig struct {
	Transport Transport
	// HTTPAddr is the listen address for TransportHTTP (host:port), e.g. ":8080".
	// Not validated here — a malformed value fails at net.Listen in main.
	HTTPAddr string
	AuthMode AuthMode
	// OIDC is populated (and required) only when AuthMode is bearer or broker.
	OIDC OIDCConfig
}

// OIDCConfig holds bearer-token validation settings (ADR-0007/0003). Names match
// the CONTEXT.md glossary.
type OIDCConfig struct {
	// Issuer is the OIDC provider's issuer URL; its endpoints (incl. JWKS) are
	// resolved by discovery. Required for bearer/broker.
	Issuer string
	// ResourceURI is this server's canonical identifier; a token's aud must equal
	// it (RFC 8707), so a token minted for another service cannot be replayed here.
	ResourceURI string
	// IdentityClaim names the token claim used as the user's identity (default
	// email, then preferred_username).
	IdentityClaim string
	// RequiredClaim/RequiredValue gate access: the token's RequiredClaim must
	// contain RequiredValue. Empty RequiredClaim means authenticate-only (no
	// access gate) — every valid token from the issuer is allowed.
	RequiredClaim string
	RequiredValue string
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

	// broker is a valid mode but its interactive layer is not wired yet; fail
	// loudly rather than serve without it. Drop this once broker lands.
	if authMode == AuthBroker {
		return ServerConfig{}, fmt.Errorf("MCP_AUTH_MODE=broker is not implemented yet; use off or bearer")
	}

	var oidc OIDCConfig
	if authMode == AuthBearer {
		o, err := loadOIDC()
		if err != nil {
			return ServerConfig{}, err
		}
		oidc = o
	}

	return ServerConfig{
		Transport: transport,
		HTTPAddr:  envString("MCP_HTTP_ADDR", ":8080"),
		AuthMode:  authMode,
		OIDC:      oidc,
	}, nil
}

// loadOIDC reads the bearer-token settings. Issuer and resource URI are required
// (no safe default — an empty audience would validate any token); the identity
// claim defaults to email and the access gate is optional. Values are trimmed so
// a whitespace-only env var is treated as unset rather than a bad URL.
func loadOIDC() (OIDCConfig, error) {
	issuer := strings.TrimSpace(envString("OIDC_ISSUER", ""))
	if issuer == "" {
		return OIDCConfig{}, fmt.Errorf("OIDC_ISSUER is required when MCP_AUTH_MODE=bearer")
	}
	resourceURI := strings.TrimSpace(envString("MCP_RESOURCE_URI", ""))
	if resourceURI == "" {
		return OIDCConfig{}, fmt.Errorf("MCP_RESOURCE_URI is required when MCP_AUTH_MODE=bearer (the audience a token must carry)")
	}
	requiredClaim := strings.TrimSpace(envString("OIDC_REQUIRED_CLAIM", ""))
	requiredValue := envString("OIDC_REQUIRED_VALUE", "")
	// A gate claim with no value is incoherent — it would deny every legitimate
	// token — so require both or neither rather than silently locking everyone out.
	if requiredClaim != "" && requiredValue == "" {
		return OIDCConfig{}, fmt.Errorf("OIDC_REQUIRED_VALUE is required when OIDC_REQUIRED_CLAIM is set")
	}
	return OIDCConfig{
		Issuer:        issuer,
		ResourceURI:   resourceURI,
		IdentityClaim: envString("OIDC_IDENTITY_CLAIM", "email"),
		RequiredClaim: requiredClaim,
		RequiredValue: requiredValue,
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
