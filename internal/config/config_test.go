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
// an unknown transport/mode, the not-yet-wired broker mode, or bearer without the
// issuer/audience it needs to validate a token. Per-row t.Run so each row's
// t.Setenv is restored before the next.
func TestLoad_RejectsInvalidServerConfig(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
	}{
		{"unknown transport", map[string]string{"MCP_TRANSPORT": "grpc"}},
		{"unknown auth mode", map[string]string{"MCP_AUTH_MODE": "magic"}},
		{"broker not implemented", map[string]string{"MCP_AUTH_MODE": "broker"}},
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
