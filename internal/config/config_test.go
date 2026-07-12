package config

import (
	"os"
	"strings"
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

// The entra provider derives the issuer, authorize/token endpoints, and — the
// point of the special case — the audience from the tenant + client id, so the
// operator never hand-wires them. A real Entra v2.0 token's aud is the app id, so
// the audience must default to AZURE_CLIENT_ID, not the server URL.
func TestLoad_BrokerEntraProvider(t *testing.T) {
	setEnv(t, map[string]string{
		"MCP_AUTH_MODE":       "broker",
		"MCP_TRANSPORT":       "http",
		"MCP_BROKER_PROVIDER": "entra",
		"AZURE_TENANT_ID":     "tenant-abc",
		"AZURE_CLIENT_ID":     "client-xyz",
		"AZURE_CLIENT_SECRET": "shh",
		"MCP_PUBLIC_URL":      "https://mcp.example",
	})
	cfg, err := Load()
	if err != nil {
		t.Fatalf("entra provider should load with tenant+client only: %v", err)
	}
	o, b := cfg.Server.OIDC, cfg.Server.Broker
	if o.Issuer != "https://login.microsoftonline.com/tenant-abc/v2.0" {
		t.Errorf("issuer not derived from tenant: %q", o.Issuer)
	}
	if o.ResourceURI != "client-xyz" {
		t.Errorf("audience must default to the client id (Entra stamps aud=app id), got %q", o.ResourceURI)
	}
	if b.UpstreamAuthURL != "https://login.microsoftonline.com/tenant-abc/oauth2/v2.0/authorize" {
		t.Errorf("authorize URL not derived: %q", b.UpstreamAuthURL)
	}
	if b.UpstreamTokenURL != "https://login.microsoftonline.com/tenant-abc/oauth2/v2.0/token" {
		t.Errorf("token URL not derived: %q", b.UpstreamTokenURL)
	}
	if b.ClientID != "client-xyz" || b.ClientSecret != "shh" {
		t.Errorf("client creds not carried from AZURE_* vars: %+v", b)
	}
}

// An operator who exposed a custom api:// scope in Entra can override the derived
// client-id audience via MCP_RESOURCE_URI.
func TestLoad_BrokerEntraAudienceOverride(t *testing.T) {
	setEnv(t, map[string]string{
		"MCP_AUTH_MODE":       "broker",
		"MCP_TRANSPORT":       "http",
		"MCP_BROKER_PROVIDER": "entra",
		"AZURE_TENANT_ID":     "tenant-abc",
		"AZURE_CLIENT_ID":     "client-xyz",
		"AZURE_CLIENT_SECRET": "shh",
		"MCP_PUBLIC_URL":      "https://mcp.example",
		"MCP_RESOURCE_URI":    "api://mcp-clickhouse",
	})
	cfg, err := Load()
	if err != nil {
		t.Fatalf("entra provider with audience override should load: %v", err)
	}
	if cfg.Server.OIDC.ResourceURI != "api://mcp-clickhouse" {
		t.Errorf("explicit MCP_RESOURCE_URI should override the client-id default, got %q", cfg.Server.OIDC.ResourceURI)
	}
}

// Each named provider fails loudly when one of its required vars is missing, and an
// unknown provider name is rejected rather than silently treated as generic. Assert
// the error names the offending var/provider, so a failure for an unrelated reason
// (e.g. a renamed base var) can't pass the row and hide a missing validation.
func TestLoad_BrokerProviderErrors(t *testing.T) {
	base := map[string]string{
		"MCP_AUTH_MODE": "broker", "MCP_TRANSPORT": "http", "MCP_PUBLIC_URL": "https://mcp.example",
	}
	withBase := func(extra map[string]string) map[string]string {
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	cases := []struct {
		name    string
		wantMsg string
		env     map[string]string
	}{
		{"entra without tenant", "AZURE_TENANT_ID", withBase(map[string]string{"MCP_BROKER_PROVIDER": "entra", "AZURE_CLIENT_ID": "c", "AZURE_CLIENT_SECRET": "s"})},
		{"entra without client id", "AZURE_CLIENT_ID", withBase(map[string]string{"MCP_BROKER_PROVIDER": "entra", "AZURE_TENANT_ID": "t", "AZURE_CLIENT_SECRET": "s"})},
		{"entra without secret", "AZURE_CLIENT_SECRET", withBase(map[string]string{"MCP_BROKER_PROVIDER": "entra", "AZURE_TENANT_ID": "t", "AZURE_CLIENT_ID": "c"})},
		{"google without client id", "GOOGLE_CLIENT_ID", withBase(map[string]string{"MCP_BROKER_PROVIDER": "google", "GOOGLE_CLIENT_SECRET": "s"})},
		{"google without secret", "GOOGLE_CLIENT_SECRET", withBase(map[string]string{"MCP_BROKER_PROVIDER": "google", "GOOGLE_CLIENT_ID": "c"})},
		{"unknown provider", "unknown provider", withBase(map[string]string{"MCP_BROKER_PROVIDER": "okta-magic"})},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			setEnv(t, tt.env)
			_, err := Load()
			if err == nil {
				t.Fatalf("%s: Load should error", tt.name)
			}
			if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("%s: error should name %q, got %v", tt.name, tt.wantMsg, err)
			}
		})
	}
}

// The google provider selects Google's fixed endpoints (no tenant in the URL) from
// the client id/secret alone, defaulting the audience to the client id. The endpoint
// assertions pin the exact well-known OAuth URLs the broker POSTs the client secret
// to, where a silent typo is a real defect.
func TestLoad_BrokerGoogleProvider(t *testing.T) {
	setEnv(t, map[string]string{
		"MCP_AUTH_MODE":        "broker",
		"MCP_TRANSPORT":        "http",
		"MCP_BROKER_PROVIDER":  "google",
		"GOOGLE_CLIENT_ID":     "g-client",
		"GOOGLE_CLIENT_SECRET": "g-secret",
		"MCP_PUBLIC_URL":       "https://mcp.example",
	})
	cfg, err := Load()
	if err != nil {
		t.Fatalf("google provider should load with client id/secret only: %v", err)
	}
	o, b := cfg.Server.OIDC, cfg.Server.Broker
	if o.Issuer != "https://accounts.google.com" {
		t.Errorf("google issuer wrong: %q", o.Issuer)
	}
	if o.ResourceURI != "g-client" {
		t.Errorf("audience should default to the client id, got %q", o.ResourceURI)
	}
	if b.UpstreamAuthURL != "https://accounts.google.com/o/oauth2/v2/auth" || b.UpstreamTokenURL != "https://oauth2.googleapis.com/token" {
		t.Errorf("google endpoints not derived: auth=%q token=%q", b.UpstreamAuthURL, b.UpstreamTokenURL)
	}
	if b.ClientID != "g-client" || b.ClientSecret != "g-secret" {
		t.Errorf("client creds not carried from GOOGLE_* vars: %+v", b)
	}
}

// The provider endpoint bases are package-level vars (not operator config) so a
// validation harness can point a named provider at a mock IdP. Overriding the entra
// base must repoint issuer + authorize + token together; the default is restored.
func TestDeriveEntra_HonorsAuthorityBaseSeam(t *testing.T) {
	orig := entraAuthorityBase
	t.Cleanup(func() { entraAuthorityBase = orig })
	entraAuthorityBase = "http://127.0.0.1:19000"

	setEnv(t, map[string]string{
		"AZURE_TENANT_ID": "t1", "AZURE_CLIENT_ID": "c1", "AZURE_CLIENT_SECRET": "s1",
	})
	d, err := deriveEntra()
	if err != nil {
		t.Fatalf("deriveEntra: %v", err)
	}
	want := "http://127.0.0.1:19000/t1"
	if d.Issuer != want+"/v2.0" || d.AuthorizeURL != want+"/oauth2/v2.0/authorize" || d.TokenURL != want+"/oauth2/v2.0/token" {
		t.Errorf("authority base not applied to all three endpoints: %+v", d)
	}
}

// google, like entra, lets an operator override the client-id audience default via
// MCP_RESOURCE_URI (for a custom API audience).
func TestLoad_BrokerGoogleAudienceOverride(t *testing.T) {
	setEnv(t, map[string]string{
		"MCP_AUTH_MODE":        "broker",
		"MCP_TRANSPORT":        "http",
		"MCP_BROKER_PROVIDER":  "google",
		"GOOGLE_CLIENT_ID":     "g-client",
		"GOOGLE_CLIENT_SECRET": "g-secret",
		"MCP_PUBLIC_URL":       "https://mcp.example",
		"MCP_RESOURCE_URI":     "api://mcp-clickhouse",
	})
	cfg, err := Load()
	if err != nil {
		t.Fatalf("google provider with audience override should load: %v", err)
	}
	if cfg.Server.OIDC.ResourceURI != "api://mcp-clickhouse" {
		t.Errorf("explicit MCP_RESOURCE_URI should override the client-id default, got %q", cfg.Server.OIDC.ResourceURI)
	}
}

// A whitespace MCP_RESOURCE_URI on a named provider trims to empty, collapsing the
// client-id audience default — Load must reject it rather than serve an empty
// audience (which would validate a token minted for any resource). Pins both the
// TrimSpace in the derive step and the downstream empty-audience guard.
func TestLoad_BrokerRejectsEmptyDerivedAudience(t *testing.T) {
	for _, provider := range []string{"entra", "google"} {
		t.Run(provider, func(t *testing.T) {
			env := map[string]string{
				"MCP_AUTH_MODE": "broker", "MCP_TRANSPORT": "http",
				"MCP_BROKER_PROVIDER": provider, "MCP_PUBLIC_URL": "https://mcp.example",
				"MCP_RESOURCE_URI": "   ", // whitespace → trims to empty, overriding the client-id default
			}
			if provider == "entra" {
				env["AZURE_TENANT_ID"], env["AZURE_CLIENT_ID"], env["AZURE_CLIENT_SECRET"] = "t", "c", "s"
			} else {
				env["GOOGLE_CLIENT_ID"], env["GOOGLE_CLIENT_SECRET"] = "c", "s"
			}
			setEnv(t, env)
			_, err := Load()
			if err == nil {
				t.Fatalf("%s: a whitespace MCP_RESOURCE_URI must be rejected, not accepted as an empty audience", provider)
			}
			if !strings.Contains(err.Error(), "MCP_RESOURCE_URI") {
				t.Errorf("%s: rejection should name the empty audience, got %v", provider, err)
			}
		})
	}
}
