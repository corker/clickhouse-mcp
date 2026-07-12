package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testBroker() BrokerConfig {
	return BrokerConfig{
		PublicURL: "https://mcp.example",
		ClientID:  "preregistered-entra-app",
		Scopes:    []string{"openid", "profile"},
	}
}

// The metadata must carry the fields Entra omits but MCP clients require, and in
// broker mode every endpoint points at this server, not the upstream.
func TestAuthServerMetadata(t *testing.T) {
	m := testBroker().authServerMetadata()

	if got := m["code_challenge_methods_supported"]; !contains(toStrings(got), "S256") {
		t.Errorf("metadata must advertise S256 PKCE (Entra omits it), got %v", got)
	}
	for _, key := range []string{"authorization_endpoint", "token_endpoint", "registration_endpoint"} {
		v, _ := m[key].(string)
		if !strings.HasPrefix(v, "https://mcp.example/oauth/") {
			t.Errorf("%s must point at this server, got %q", key, v)
		}
	}
	if m["issuer"] != "https://mcp.example" {
		t.Errorf("issuer = %v, want the server URL", m["issuer"])
	}
}

// Fake DCR returns OUR pre-registered client_id no matter what the client asks
// for — that is what lets a DCR-only client work against Entra (no DCR).
func TestDCRResponse_ReturnsPreregisteredClientID(t *testing.T) {
	b := testBroker()
	resp := b.dcrResponse(map[string]any{
		"client_id":     "attacker-chosen-id", // must be ignored
		"client_name":   "Claude",
		"redirect_uris": []any{"http://localhost:1234/callback"},
	})
	if resp["client_id"] != "preregistered-entra-app" {
		t.Errorf("client_id = %v, want the pre-registered app (client input must not override)", resp["client_id"])
	}
	if resp["client_name"] != "Claude" {
		t.Errorf("client_name should echo the request, got %v", resp["client_name"])
	}
	if resp["token_endpoint_auth_method"] != "none" {
		t.Errorf("must register as a public (PKCE) client, got %v", resp["token_endpoint_auth_method"])
	}
}

func TestHandleAuthServerMetadata(t *testing.T) {
	b := testBroker()

	rec := httptest.NewRecorder()
	b.HandleAuthServerMetadata(rec, httptest.NewRequest(http.MethodGet, "/.well-known/oauth-authorization-server", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Error("metadata must be CORS-open for browser clients")
	}
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	if m["registration_endpoint"] == nil {
		t.Error("served metadata missing registration_endpoint")
	}

	// A non-GET/HEAD/OPTIONS method is rejected.
	rec = httptest.NewRecorder()
	b.HandleAuthServerMetadata(rec, httptest.NewRequest(http.MethodPost, "/x", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST status = %d, want 405", rec.Code)
	}
}

func TestHandleRegister(t *testing.T) {
	b := testBroker()

	body := `{"client_name":"Cursor","redirect_uris":["http://127.0.0.1:9000/cb"]}`
	rec := httptest.NewRecorder()
	b.HandleRegister(rec, httptest.NewRequest(http.MethodPost, "/oauth/register", strings.NewReader(body)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST status = %d, want 201", rec.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if resp["client_id"] != "preregistered-entra-app" {
		t.Errorf("client_id = %v, want the pre-registered app", resp["client_id"])
	}

	// Malformed body is rejected, not accepted with a blank client.
	rec = httptest.NewRecorder()
	b.HandleRegister(rec, httptest.NewRequest(http.MethodPost, "/oauth/register", strings.NewReader("not json")))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("malformed body status = %d, want 400", rec.Code)
	}

	// GET is rejected — registration is POST-only.
	rec = httptest.NewRecorder()
	b.HandleRegister(rec, httptest.NewRequest(http.MethodGet, "/oauth/register", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET status = %d, want 405", rec.Code)
	}
}

// toStrings coerces a []any or []string metadata value to []string for assertions.
func toStrings(v any) []string {
	switch x := v.(type) {
	case []string:
		return x
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// NewProxy must generate a fresh 32-byte signing key each call (CSPRNG, not a
// constant) — a shared/constant key would let one deployment forge another's state.
func TestNewProxy_GeneratesDistinctKeys(t *testing.T) {
	p1, err := NewProxy(ProxyConfig{Broker: testBroker()})
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	if len(p1.stateKey) != 32 {
		t.Errorf("state key = %d bytes, want 32", len(p1.stateKey))
	}
	allZero := true
	for _, b := range p1.stateKey {
		if b != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		t.Error("state key must not be all-zero")
	}
	p2, _ := NewProxy(ProxyConfig{Broker: testBroker()})
	if string(p1.stateKey) == string(p2.stateKey) {
		t.Error("two NewProxy calls must produce different keys")
	}
}
