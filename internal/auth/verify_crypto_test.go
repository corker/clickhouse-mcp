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
		Issuer:        fi.issuerURL(),
		ResourceURI:   resourceURI,
		IdentityClaim: "email",
		RequiredClaim: requiredClaim,
		RequiredValue: requiredValue,
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
}

// The security-critical negatives: each must be rejected as ErrInvalidToken.
func TestVerify_Rejects(t *testing.T) {
	fi := newFakeIssuer(t)
	other := newFakeIssuer(t) // a different issuer, for the wrong-signer case

	tests := []struct {
		name  string
		token func() string
	}{
		{"wrong audience (token for another service)", func() string {
			return fi.mint(t, map[string]any{"aud": "https://other.service"})
		}},
		{"no audience", func() string {
			return fi.mint(t, map[string]any{})
		}},
		{"expired", func() string {
			return fi.mint(t, map[string]any{"aud": resourceURI, "exp": time.Now().Add(-time.Hour).Unix()})
		}},
		{"wrong issuer", func() string {
			return fi.mint(t, map[string]any{"aud": resourceURI, "iss": "https://evil.example"})
		}},
		{"signed by a different key (JWKS mismatch)", func() string {
			return other.mint(t, map[string]any{"aud": resourceURI, "iss": fi.issuerURL()})
		}},
		{"no identity claim (would collapse sessions)", func() string {
			// valid aud/exp/iss but no email/preferred_username/sub
			return fi.mint(t, map[string]any{"aud": resourceURI})
		}},
		{"garbage", func() string { return "not.a.jwt" }},
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
