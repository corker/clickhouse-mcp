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

// AuthMode selects how HTTP requests are authenticated (ADR-0007).
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
	// Broker is populated only when AuthMode is broker (the interactive shim).
	Broker BrokerConfig
}

// BrokerConfig holds the interactive-broker settings (ADR-0008), required only for
// MCP_AUTH_MODE=broker. It fronts an upstream IdP (e.g. Entra) that MCP clients
// cannot use directly.
type BrokerConfig struct {
	// PublicURL is this server's externally reachable base URL (no trailing slash);
	// the broker advertises its own OAuth endpoints under it.
	PublicURL string
	// ClientID/ClientSecret are the app pre-registered once with the upstream IdP.
	ClientID     string
	ClientSecret string
	// UpstreamAuthURL/UpstreamTokenURL are the IdP's real authorize/token endpoints.
	UpstreamAuthURL  string
	UpstreamTokenURL string
	// AllowedRedirectHosts are non-loopback host suffixes a client redirect_uri may
	// use. Loopback is always allowed; empty means loopback-only (the safe default).
	AllowedRedirectHosts []string
	// Scopes are advertised in metadata and requested upstream.
	Scopes []string
}

// AccessPolicy maps a token's claims to an identity and an allow/deny decision. It
// is separated from the token-validation settings (issuer, resource URI) because the
// identity + gating step is independent of how the token was validated.
type AccessPolicy struct {
	// IdentityClaim names the claim used as the user's identity (default email,
	// then preferred_username).
	IdentityClaim string
	// RequiredClaim/RequiredValue gate access: the RequiredClaim must contain
	// RequiredValue. Empty RequiredClaim means authenticate-only (no access gate) —
	// every authenticated principal is allowed.
	RequiredClaim string
	RequiredValue string
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
	// AccessPolicy (identity claim + access gate) is applied after token validation.
	AccessPolicy
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

	// A named provider (entra) derives the Entra-specific issuer/endpoints/audience
	// from a tenant id so the operator does not hand-wire them. Only broker mode
	// reads it; bearer mode always uses the explicit generic OIDC vars.
	var derived *derivedProvider
	if authMode == AuthBroker {
		d, err := deriveProvider()
		if err != nil {
			return ServerConfig{}, err
		}
		derived = d
	}

	// Both bearer and broker validate the resulting token, so both load OIDC.
	var oidc OIDCConfig
	var broker BrokerConfig
	if authMode == AuthBearer || authMode == AuthBroker {
		o, err := loadOIDC(derived)
		if err != nil {
			return ServerConfig{}, err
		}
		oidc = o
	}
	if authMode == AuthBroker {
		b, err := loadBroker(derived)
		if err != nil {
			return ServerConfig{}, err
		}
		broker = b
	}

	return ServerConfig{
		Transport: transport,
		HTTPAddr:  envString("MCP_HTTP_ADDR", ":8080"),
		AuthMode:  authMode,
		OIDC:      oidc,
		Broker:    broker,
	}, nil
}

// derivedProvider holds the values a named provider computes so the operator need not
// supply them. nil means the generic provider (all endpoints explicit).
type derivedProvider struct {
	Issuer       string
	ResourceURI  string
	AuthorizeURL string
	TokenURL     string
	ClientID     string
	ClientSecret string
}

// deriveProvider dispatches on MCP_BROKER_PROVIDER. generic returns nil (every
// endpoint explicit); each named provider derives its own endpoints and, crucially,
// its own audience default — the aud rule is per-provider policy (Entra/Google stamp
// aud = the client id), not a shared constant, so a provider whose aud differs
// cannot inherit the wrong default.
func deriveProvider() (*derivedProvider, error) {
	provider := strings.TrimSpace(envString("MCP_BROKER_PROVIDER", "generic"))
	switch provider {
	case "generic":
		return nil, nil
	case "entra":
		return deriveEntra()
	case "google":
		return deriveGoogle()
	default:
		return nil, fmt.Errorf("MCP_BROKER_PROVIDER: unknown provider %q (want entra, google, or generic)", provider)
	}
}

// requireEnv reads and trims the named env vars, failing if any is empty. The
// message names the provider so an operator knows which var set to complete.
func requireEnv(provider string, keys ...string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	for _, k := range keys {
		v := strings.TrimSpace(envString(k, ""))
		if v == "" {
			return nil, fmt.Errorf("%s is required when MCP_BROKER_PROVIDER=%s", k, provider)
		}
		out[k] = v
	}
	return out, nil
}

// deriveEntra derives the Entra endpoints from the tenant id. An Entra v2.0 access
// token's aud is the app (client) id, not the server URL, so the audience defaults
// to the client id (overridable via MCP_RESOURCE_URI for a custom api:// scope).
// Provider endpoint bases. Package-level (not env-configurable) so an operator
// cannot repoint the "trusted Entra/Google" flow at a rogue endpoint — the whole
// point of a named provider is that its endpoints are fixed. They are variables
// rather than constants solely so an in-package validation harness can point them
// at a mock IdP; production always uses the real hosts.
var (
	entraAuthorityBase = "https://login.microsoftonline.com"
	googleAccountsBase = "https://accounts.google.com"
	googleTokenBase    = "https://oauth2.googleapis.com" //nolint:gosec // G101 false positive: public Google token endpoint host, not a credential
)

func deriveEntra() (*derivedProvider, error) {
	env, err := requireEnv("entra", "AZURE_TENANT_ID", "AZURE_CLIENT_ID", "AZURE_CLIENT_SECRET")
	if err != nil {
		return nil, err
	}
	clientID := env["AZURE_CLIENT_ID"]
	base := entraAuthorityBase + "/" + env["AZURE_TENANT_ID"]
	return &derivedProvider{
		Issuer:       base + "/v2.0",
		ResourceURI:  strings.TrimSpace(envString("MCP_RESOURCE_URI", clientID)),
		AuthorizeURL: base + "/oauth2/v2.0/authorize",
		TokenURL:     base + "/oauth2/v2.0/token",
		ClientID:     clientID,
		ClientSecret: env["AZURE_CLIENT_SECRET"],
	}, nil
}

// deriveGoogle derives the Google endpoints (fixed — no tenant in the URL). A Google
// access token's aud is the client id, so the audience defaults to it (overridable
// via MCP_RESOURCE_URI).
func deriveGoogle() (*derivedProvider, error) {
	env, err := requireEnv("google", "GOOGLE_CLIENT_ID", "GOOGLE_CLIENT_SECRET")
	if err != nil {
		return nil, err
	}
	clientID := env["GOOGLE_CLIENT_ID"]
	return &derivedProvider{
		Issuer:       googleAccountsBase,
		ResourceURI:  strings.TrimSpace(envString("MCP_RESOURCE_URI", clientID)),
		AuthorizeURL: googleAccountsBase + "/o/oauth2/v2/auth",
		TokenURL:     googleTokenBase + "/token",
		ClientID:     clientID,
		ClientSecret: env["GOOGLE_CLIENT_SECRET"],
	}, nil
}

// loadOIDC reads the bearer-token settings. Issuer and resource URI are required
// (no safe default — an empty audience would validate any token); the identity
// claim defaults to email and the access gate is optional. Values are trimmed so
// a whitespace-only env var is treated as unset rather than a bad URL.
func loadOIDC(derived *derivedProvider) (OIDCConfig, error) {
	issuer := strings.TrimSpace(envString("OIDC_ISSUER", ""))
	resourceURI := strings.TrimSpace(envString("MCP_RESOURCE_URI", ""))
	if derived != nil {
		issuer = derived.Issuer
		resourceURI = derived.ResourceURI
	}
	if issuer == "" {
		return OIDCConfig{}, fmt.Errorf("OIDC_ISSUER is required when MCP_AUTH_MODE=bearer")
	}
	if resourceURI == "" {
		return OIDCConfig{}, fmt.Errorf("MCP_RESOURCE_URI is required when MCP_AUTH_MODE=bearer (the audience a token must carry)")
	}
	policy, err := loadAccessPolicy()
	if err != nil {
		return OIDCConfig{}, err
	}
	return OIDCConfig{
		Issuer:       issuer,
		ResourceURI:  resourceURI,
		AccessPolicy: policy,
	}, nil
}

// loadAccessPolicy reads the identity claim (default email) and the optional access
// gate. A gate claim with no value is incoherent — it would deny every legitimate
// principal — so require both or neither rather than silently locking everyone out.
func loadAccessPolicy() (AccessPolicy, error) {
	requiredClaim := strings.TrimSpace(envString("OIDC_REQUIRED_CLAIM", ""))
	requiredValue := envString("OIDC_REQUIRED_VALUE", "")
	if requiredClaim != "" && requiredValue == "" {
		return AccessPolicy{}, fmt.Errorf("OIDC_REQUIRED_VALUE is required when OIDC_REQUIRED_CLAIM is set")
	}
	return AccessPolicy{
		IdentityClaim: envString("OIDC_IDENTITY_CLAIM", "email"),
		RequiredClaim: requiredClaim,
		RequiredValue: requiredValue,
	}, nil
}

// loadBroker reads the interactive-broker settings. The public URL, upstream
// endpoints, and pre-registered client id/secret are all required — the broker
// cannot function without any of them, so fail loudly rather than serve a broken
// login flow.
func loadBroker(derived *derivedProvider) (BrokerConfig, error) {
	required := func(key string) (string, error) {
		v := strings.TrimSpace(envString(key, ""))
		if v == "" {
			return "", fmt.Errorf("%s is required when MCP_AUTH_MODE=broker", key)
		}
		return v, nil
	}
	publicURL, err := required("MCP_PUBLIC_URL")
	if err != nil {
		return BrokerConfig{}, err
	}

	// The entra provider supplies the client credentials and endpoints; the generic
	// provider requires them explicitly.
	clientID, clientSecret, authURL, tokenURL := "", "", "", ""
	if derived != nil {
		clientID, clientSecret = derived.ClientID, derived.ClientSecret
		authURL, tokenURL = derived.AuthorizeURL, derived.TokenURL
	} else {
		for _, f := range []struct {
			key string
			dst *string
		}{
			{"OIDC_CLIENT_ID", &clientID},
			{"OIDC_CLIENT_SECRET", &clientSecret},
			{"OIDC_AUTHORIZE_URL", &authURL},
			{"OIDC_TOKEN_URL", &tokenURL},
		} {
			if *f.dst, err = required(f.key); err != nil {
				return BrokerConfig{}, err
			}
		}
	}

	var hosts []string
	for _, h := range strings.Split(envString("MCP_ALLOWED_REDIRECT_HOSTS", ""), ",") {
		if h = strings.TrimSpace(h); h != "" {
			hosts = append(hosts, h)
		}
	}
	scopes := strings.Fields(envString("OIDC_SCOPES", "openid profile email"))
	return BrokerConfig{
		PublicURL:            strings.TrimRight(publicURL, "/"),
		ClientID:             clientID,
		ClientSecret:         clientSecret,
		UpstreamAuthURL:      authURL,
		UpstreamTokenURL:     tokenURL,
		AllowedRedirectHosts: hosts,
		Scopes:               scopes,
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
