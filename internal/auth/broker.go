package auth

import (
	"crypto/rand"
	"encoding/json"
	"net/http"
)

// BrokerConfig configures the interactive OAuth broker (ADR-0008). It fronts an
// upstream IdP that MCP clients cannot use directly — notably Entra, which has no
// Dynamic Client Registration and serves non-spec metadata. This slice covers the
// discovery half: the authorization-server metadata and the fake-DCR endpoint. The
// authorize/callback/token proxy lands in a later slice.
type BrokerConfig struct {
	// PublicURL is this server's externally reachable base URL (no trailing
	// slash). The broker advertises its own endpoints under it, so clients send
	// the OAuth flow here rather than to the upstream.
	PublicURL string
	// ClientID is the app pre-registered once with the upstream IdP. The fake-DCR
	// endpoint hands this back to every client, satisfying the client's mandatory
	// registration step without the upstream needing to support DCR.
	ClientID string
	// Scopes advertised in metadata and requested upstream.
	Scopes []string
}

// authServerMetadata is the RFC 8414 authorization-server metadata. In broker
// mode every endpoint points at this server; the fields Entra omits but clients
// require (code_challenge_methods_supported, registration_endpoint) are always
// present.
func (b BrokerConfig) authServerMetadata() map[string]any {
	return map[string]any{
		"issuer":                                b.PublicURL,
		"authorization_endpoint":                b.PublicURL + "/oauth/authorize",
		"token_endpoint":                        b.PublicURL + "/oauth/token",
		"registration_endpoint":                 b.PublicURL + "/oauth/register",
		"response_types_supported":              []string{"code"},
		"response_modes_supported":              []string{"query"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"token_endpoint_auth_methods_supported": []string{"none"},
		"code_challenge_methods_supported":      []string{"S256"},
		"scopes_supported":                      b.Scopes,
	}
}

// dcrResponse is the fake dynamic-client-registration reply (RFC 7591 shape). It
// echoes the client's requested redirect_uris and name, but the client_id is
// always our one pre-registered upstream app — the trick that lets a DCR-only
// client work against an IdP (Entra) that has no DCR.
func (b BrokerConfig) dcrResponse(req map[string]any) map[string]any {
	resp := map[string]any{
		"client_id":                  b.ClientID,
		"token_endpoint_auth_method": "none", // public client, PKCE
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
	}
	if v, ok := req["redirect_uris"]; ok {
		resp["redirect_uris"] = v
	}
	if v, ok := req["client_name"]; ok {
		resp["client_name"] = v
	}
	return resp
}

const maxDCRBody = 1 << 18 // 256 KB — cap the registration body against DoS.

func writeJSONResponse(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// corsMetadata allows any origin — OAuth discovery metadata is public and MCP
// clients (and MCP Inspector) fetch it cross-origin (RFC 9728 §3.1).
func corsMetadata(w http.ResponseWriter, methods string) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", methods)
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, *")
	w.Header().Set("Access-Control-Max-Age", "86400")
}

// HandleAuthServerMetadata serves RFC 8414 authorization-server metadata.
func (b BrokerConfig) HandleAuthServerMetadata(w http.ResponseWriter, r *http.Request) {
	corsMetadata(w, "GET, HEAD, OPTIONS")
	switch r.Method {
	case http.MethodOptions, http.MethodHead:
		w.WriteHeader(http.StatusOK)
	case http.MethodGet:
		w.Header().Set("Cache-Control", "public, max-age=300")
		writeJSONResponse(w, http.StatusOK, b.authServerMetadata())
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// HandleRegister is the fake-DCR endpoint: it accepts any registration and returns
// the pre-registered upstream client_id.
func (b BrokerConfig) HandleRegister(w http.ResponseWriter, r *http.Request) {
	corsMetadata(w, "POST, OPTIONS")
	switch r.Method {
	case http.MethodOptions:
		w.WriteHeader(http.StatusOK)
		return
	case http.MethodPost:
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxDCRBody)
	defer func() { _ = r.Body.Close() }()

	var req map[string]any
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid registration body", http.StatusBadRequest)
		return
	}
	writeJSONResponse(w, http.StatusCreated, b.dcrResponse(req))
}

// NewProxy finishes a ProxyConfig by generating its state signing key. The caller
// fills the settings (named fields — no positional-arg transposition risk); the
// key is owned here so a CSPRNG failure aborts startup. Fails if the key cannot
// be read from the system CSPRNG.
func NewProxy(cfg ProxyConfig) (ProxyConfig, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return ProxyConfig{}, err
	}
	cfg.stateKey = key
	return cfg, nil
}

// RegisterRoutes mounts the broker's discovery + proxy endpoints on mux.
func (p ProxyConfig) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/.well-known/oauth-authorization-server", p.Broker.HandleAuthServerMetadata)
	mux.HandleFunc("/oauth/register", p.Broker.HandleRegister)
	mux.HandleFunc("/oauth/authorize", p.HandleAuthorize)
	mux.HandleFunc("/oauth/callback", p.HandleCallback)
	mux.HandleFunc("/oauth/token", p.HandleToken)
}
