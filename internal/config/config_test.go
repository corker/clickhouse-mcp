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

func TestLoad_RejectsUnknownTransport(t *testing.T) {
	setEnv(t, map[string]string{"MCP_TRANSPORT": "grpc"})
	if _, err := Load(); err == nil {
		t.Error("unknown transport should error")
	}
}

func TestLoad_RejectsUnknownAuthMode(t *testing.T) {
	setEnv(t, map[string]string{"MCP_AUTH_MODE": "magic"})
	if _, err := Load(); err == nil {
		t.Error("unknown auth mode should error")
	}
}

// bearer/broker parse as valid modes but are not implemented yet, so Load must
// fail loudly rather than silently serve unauthenticated.
func TestLoad_RejectsUnimplementedAuthModes(t *testing.T) {
	for _, mode := range []string{"bearer", "broker"} {
		setEnv(t, map[string]string{"MCP_AUTH_MODE": mode})
		if _, err := Load(); err == nil {
			t.Errorf("auth mode %q should error until implemented", mode)
		}
	}
}
