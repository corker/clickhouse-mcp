package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// fakeIssuer is an in-process OIDC provider: it serves a real discovery document
// and JWKS over httptest and mints RS256-signed JWTs. This exercises the whole
// crypto path in Verifier (discovery, RemoteKeySet JWKS fetch, signature verify)
// against real signatures — only the IdP is local, nothing in Verifier is mocked.
type fakeIssuer struct {
	server *httptest.Server
	key    *rsa.PrivateKey
	keyID  string
}

func newFakeIssuer(t *testing.T) *fakeIssuer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	fi := &fakeIssuer{key: key, keyID: "test-key-1"}

	mux := http.NewServeMux()
	fi.server = httptest.NewServer(mux)

	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{
			"issuer":                 fi.server.URL,
			"jwks_uri":               fi.server.URL + "/jwks",
			"authorization_endpoint": fi.server.URL + "/authorize",
			"token_endpoint":         fi.server.URL + "/token",
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
			Key:       key.Public(),
			KeyID:     fi.keyID,
			Algorithm: string(jose.RS256),
			Use:       "sig",
		}}})
	})
	t.Cleanup(fi.server.Close)
	return fi
}

func (fi *fakeIssuer) issuerURL() string { return fi.server.URL }

// mint signs a JWT with the given claims. Standard claims (iss/exp) default to
// this issuer and one hour out unless overridden via the claims map.
func (fi *fakeIssuer) mint(t *testing.T, claims map[string]any) string {
	t.Helper()
	full := map[string]any{
		"iss": fi.server.URL,
		"exp": time.Now().Add(time.Hour).Unix(),
		"iat": time.Now().Unix(),
	}
	for k, v := range claims {
		full[k] = v
	}
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: fi.key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", fi.keyID),
	)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	token, err := jwt.Signed(signer).Claims(full).Serialize()
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return token
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
