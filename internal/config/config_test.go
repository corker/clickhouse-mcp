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

// Load must reject a bad or not-yet-supported server config rather than start
// with it. bearer/broker parse as valid modes but aren't wired yet, so they must
// fail loudly rather than silently serve unauthenticated. Per-row t.Run so each
// row's t.Setenv is restored before the next.
func TestLoad_RejectsInvalidServerConfig(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
	}{
		{"unknown transport", map[string]string{"MCP_TRANSPORT": "grpc"}},
		{"unknown auth mode", map[string]string{"MCP_AUTH_MODE": "magic"}},
		{"bearer not implemented", map[string]string{"MCP_AUTH_MODE": "bearer"}},
		{"broker not implemented", map[string]string{"MCP_AUTH_MODE": "broker"}},
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
