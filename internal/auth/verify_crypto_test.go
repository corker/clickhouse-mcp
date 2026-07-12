package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	mcpauth "github.com/modelcontextprotocol/go-sdk/auth"

	"github.com/corker/clickhouse-mcp/internal/config"
)

const resourceURI = "https://mcp.example"

// newTestVerifier builds a Verifier against the fake issuer with the given access
// gate (empty claim = authenticate-only).
func newTestVerifier(t *testing.T, fi *fakeIssuer, requiredClaim, requiredValue string) *Verifier {
	t.Helper()
	v, err := NewVerifier(context.Background(), config.OIDCConfig{
		Issuer:      fi.issuerURL(),
		ResourceURI: resourceURI,
		AccessPolicy: config.AccessPolicy{
			IdentityClaim: "email",
			RequiredClaim: requiredClaim,
			RequiredValue: requiredValue,
		},
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	return v
}

// A well-formed token from the issuer, carrying our audience, verifies — and the
// identity + claims come back. This exercises real discovery, JWKS fetch, and
// RS256 signature verification.
func TestVerify_ValidToken(t *testing.T) {
	fi := newFakeIssuer(t)
	v := newTestVerifier(t, fi, "", "")
	token := fi.mint(t, map[string]any{"aud": resourceURI, "email": "user@example.com"})

	info, err := v.Verify(context.Background(), token, nil)
	if err != nil {
		t.Fatalf("valid token should verify: %v", err)
	}
	if info.UserID != "user@example.com" {
		t.Errorf("UserID = %q, want user@example.com", info.UserID)
	}
	// Expiration is load-bearing — the SDK re-checks it downstream; a zero value
	// would make the session look immediately expired or never-expiring.
	if info.Expiration.IsZero() || !info.Expiration.After(time.Now()) {
		t.Errorf("Expiration should be a future time, got %v", info.Expiration)
	}
	// Extra carries the raw claims for downstream use.
	if info.Extra["email"] != "user@example.com" {
		t.Errorf("Extra should carry the claims, got %v", info.Extra)
	}
}

// The security-critical negatives: each must be rejected as ErrInvalidToken.
func TestVerify_Rejects(t *testing.T) {
	fi := newFakeIssuer(t)
	other := newFakeIssuer(t) // a different issuer, for the wrong-signer case

	tests := []struct {
		name  string
		token func() string
		// wantMsg, when set, pins the rejection to a specific gate — otherwise a
		// case could silently start failing at a different (earlier) gate and still
		// pass the ErrInvalidToken class check. Used for our own gates (audience,
		// identity); go-oidc's sig/iss/exp gates can't be confused for each other.
		wantMsg string
	}{
		{"wrong audience (token for another service)", func() string {
			return fi.mint(t, map[string]any{"aud": "https://other.service"})
		}, "audience"},
		{"no audience", func() string {
			return fi.mint(t, map[string]any{})
		}, "audience"},
		{"expired", func() string {
			return fi.mint(t, map[string]any{"aud": resourceURI, "exp": time.Now().Add(-time.Hour).Unix()})
		}, ""},
		{"wrong issuer", func() string {
			return fi.mint(t, map[string]any{"aud": resourceURI, "iss": "https://evil.example"})
		}, ""},
		{"signed by a different key (JWKS mismatch)", func() string {
			return other.mint(t, map[string]any{"aud": resourceURI, "iss": fi.issuerURL()})
		}, ""},
		{"no identity claim (would collapse sessions)", func() string {
			// valid aud/exp/iss but no email/preferred_username/sub
			return fi.mint(t, map[string]any{"aud": resourceURI})
		}, "no usable identity"},
		{"garbage", func() string { return "not.a.jwt" }, ""},
	}
	v := newTestVerifier(t, fi, "", "")
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := v.Verify(context.Background(), tt.token(), nil)
			if err == nil {
				t.Fatal("must reject")
			}
			if !errors.Is(err, mcpauth.ErrInvalidToken) {
				t.Errorf("want ErrInvalidToken, got %v", err)
			}
			if tt.wantMsg != "" && !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("want rejection at the %q gate, got %v", tt.wantMsg, err)
			}
		})
	}
}

// The access gate: a token missing the required claim/value is rejected even
// though it is otherwise valid; one that has it passes.
func TestVerify_AccessGate(t *testing.T) {
	fi := newFakeIssuer(t)
	v := newTestVerifier(t, fi, "groups", "mcp-users")

	denied := fi.mint(t, map[string]any{"aud": resourceURI, "groups": []any{"other"}})
	if _, err := v.Verify(context.Background(), denied, nil); err == nil || !strings.Contains(err.Error(), "required claim") {
		t.Errorf("token without the required group must be denied, got %v", err)
	}

	allowed := fi.mint(t, map[string]any{"aud": resourceURI, "groups": []any{"mcp-users"}, "email": "u@e.com"})
	if _, err := v.Verify(context.Background(), allowed, nil); err != nil {
		t.Errorf("token with the required group must pass: %v", err)
	}
}
