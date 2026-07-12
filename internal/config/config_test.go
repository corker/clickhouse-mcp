package config

import (
	"os"
	"testing"
)

// setEnv sets the given vars for one test and clears them afterward, so cases do
// not leak into each other (t.Setenv restores the prior value on cleanup).
func setEnv(t *testing.T, kv map[string]string) {
	for k, v := range kv {
		t.Setenv(k, v)
	}
}

func TestLoad_ServerDefaults(t *testing.T) {
	// Unset the MCP_* vars for this test so Load sees a clean environment and
	// applies the v0.1 defaults: stdio, no auth. (t.Setenv restores them after.)
	t.Setenv("MCP_TRANSPORT", "")
	_ = os.Unsetenv("MCP_TRANSPORT")
	t.Setenv("MCP_AUTH_MODE", "")
	_ = os.Unsetenv("MCP_AUTH_MODE")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Transport != TransportStdio {
		t.Errorf("default transport = %q, want stdio", cfg.Server.Transport)
	}
	if cfg.Server.AuthMode != AuthOff {
		t.Errorf("default auth mode = %q, want off", cfg.Server.AuthMode)
	}
}

func TestLoad_HTTPTransport(t *testing.T) {
	setEnv(t, map[string]string{"MCP_TRANSPORT": "http", "MCP_HTTP_ADDR": ":9999"})
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Transport != TransportHTTP {
		t.Errorf("transport = %q, want http", cfg.Server.Transport)
	}
	if cfg.Server.HTTPAddr != ":9999" {
		t.Errorf("http addr = %q, want :9999", cfg.Server.HTTPAddr)
	}
}

func TestLoad_HTTPAddrDefault(t *testing.T) {
	setEnv(t, map[string]string{"MCP_TRANSPORT": "http"})
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.HTTPAddr != ":8080" {
		t.Errorf("default http addr = %q, want :8080", cfg.Server.HTTPAddr)
	}
}

// Load must reject a bad or incomplete server config rather than start with it:
// an unknown transport/mode, bearer without the issuer/audience it needs, or
// broker missing a required broker setting. Per-row t.Run so each row's t.Setenv
// is restored before the next.
func TestLoad_RejectsInvalidServerConfig(t *testing.T) {
	// A fully-valid bearer base, so broker rows fail only on the broker field under test.
	brokerBase := func(extra map[string]string) map[string]string {
		m := map[string]string{
			"MCP_AUTH_MODE": "broker", "OIDC_ISSUER": "https://idp.example",
			"MCP_RESOURCE_URI": "https://mcp.example",
		}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	cases := []struct {
		name string
		env  map[string]string
	}{
		{"unknown transport", map[string]string{"MCP_TRANSPORT": "grpc"}},
		{"unknown auth mode", map[string]string{"MCP_AUTH_MODE": "magic"}},
		{"broker without public url", brokerBase(map[string]string{
			"OIDC_CLIENT_ID": "id", "OIDC_CLIENT_SECRET": "s", "OIDC_AUTHORIZE_URL": "https://idp/a", "OIDC_TOKEN_URL": "https://idp/t",
		})},
		{"broker without client id", brokerBase(map[string]string{
			"MCP_PUBLIC_URL": "https://mcp.example", "OIDC_CLIENT_SECRET": "s", "OIDC_AUTHORIZE_URL": "https://idp/a", "OIDC_TOKEN_URL": "https://idp/t",
		})},
		{"bearer without issuer", map[string]string{"MCP_AUTH_MODE": "bearer", "MCP_RESOURCE_URI": "https://mcp.example"}},
		{"bearer without resource uri", map[string]string{"MCP_AUTH_MODE": "bearer", "OIDC_ISSUER": "https://idp.example"}},
		{"whitespace issuer is not set", map[string]string{"MCP_AUTH_MODE": "bearer", "OIDC_ISSUER": "  ", "MCP_RESOURCE_URI": "https://mcp.example"}},
		{"required claim without value", map[string]string{"MCP_AUTH_MODE": "bearer", "OIDC_ISSUER": "https://idp.example", "MCP_RESOURCE_URI": "https://mcp.example", "OIDC_REQUIRED_CLAIM": "groups"}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			setEnv(t, tt.env)
			if _, err := Load(); err == nil {
				t.Errorf("%s: Load should error", tt.name)
			}
		})
	}
}

// bearer with issuer + resource URI loads, defaulting the identity claim and
// leaving the access gate empty (authenticate-only) when unset.
func TestLoad_BearerOIDC(t *testing.T) {
	setEnv(t, map[string]string{
		"MCP_AUTH_MODE":    "bearer",
		"MCP_TRANSPORT":    "http",
		"OIDC_ISSUER":      "https://idp.example",
		"MCP_RESOURCE_URI": "https://mcp.example",
	})
	cfg, err := Load()
	if err != nil {
		t.Fatalf("bearer with issuer+resource should load: %v", err)
	}
	o := cfg.Server.OIDC
	if o.Issuer != "https://idp.example" || o.ResourceURI != "https://mcp.example" {
		t.Errorf("issuer/resource not carried: %+v", o)
	}
	if o.IdentityClaim != "email" {
		t.Errorf("identity claim should default to email, got %q", o.IdentityClaim)
	}
	if o.RequiredClaim != "" {
		t.Errorf("access gate should be empty when unset, got %q", o.RequiredClaim)
	}
}

// broker with all required settings loads: the bearer OIDC (it still validates the
// token) plus the broker fields, with the public URL trimmed and hosts split.
func TestLoad_BrokerConfig(t *testing.T) {
	setEnv(t, map[string]string{
		"MCP_AUTH_MODE":              "broker",
		"MCP_TRANSPORT":              "http",
		"OIDC_ISSUER":                "https://idp.example",
		"MCP_RESOURCE_URI":           "https://mcp.example",
		"MCP_PUBLIC_URL":             "https://mcp.example/", // trailing slash trimmed
		"OIDC_CLIENT_ID":             "app-123",
		"OIDC_CLIENT_SECRET":         "shh",
		"OIDC_AUTHORIZE_URL":         "https://idp.example/authorize",
		"OIDC_TOKEN_URL":             "https://idp.example/token",
		"MCP_ALLOWED_REDIRECT_HOSTS": "claude.ai, cursor.sh",
	})
	cfg, err := Load()
	if err != nil {
		t.Fatalf("broker with all settings should load: %v", err)
	}
	b := cfg.Server.Broker
	if b.PublicURL != "https://mcp.example" {
		t.Errorf("public URL should be trimmed of trailing slash, got %q", b.PublicURL)
	}
	if b.ClientID != "app-123" || b.ClientSecret != "shh" {
		t.Errorf("client creds not carried: %+v", b)
	}
	if len(b.AllowedRedirectHosts) != 2 || b.AllowedRedirectHosts[0] != "claude.ai" || b.AllowedRedirectHosts[1] != "cursor.sh" {
		t.Errorf("allowed hosts not split/trimmed: %v", b.AllowedRedirectHosts)
	}
	// broker still loads the bearer OIDC.
	if cfg.Server.OIDC.Issuer != "https://idp.example" {
		t.Errorf("broker should also load OIDC, got %+v", cfg.Server.OIDC)
	}
}
