// Package auth validates bearer access tokens for the HTTP transport. The server
// is an OAuth 2.1 resource server (ADR-0007): it verifies a token's signature,
// issuer, expiry, and — critically — that the token's audience is this server, so
// a token minted for another service cannot be replayed here (RFC 8707).
package auth

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
	mcpauth "github.com/modelcontextprotocol/go-sdk/auth"

	"github.com/corker/clickhouse-mcp/internal/config"
)

type Verifier struct {
	cfg      config.OIDCConfig
	verifier *oidc.IDTokenVerifier
}

// NewVerifier discovers the issuer's metadata (incl. JWKS) and returns a Verifier.
// The go-oidc verifier is configured to check signature/issuer/expiry but NOT
// audience: its audience check has ID-token semantics (aud == client id), while
// we must check access-token audience (aud == resource URI), which we do in
// Verify. The RemoteKeySet caches JWKS and re-fetches on unseen key IDs, so key
// rotation is handled without restart.
func NewVerifier(ctx context.Context, cfg config.OIDCConfig) (*Verifier, error) {
	provider, err := oidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery for %s: %w", cfg.Issuer, err)
	}
	v := provider.Verifier(&oidc.Config{
		SkipClientIDCheck: true, // we do the RFC 8707 audience check ourselves
	})
	return &Verifier{cfg: cfg, verifier: v}, nil
}

// Verify implements the SDK's TokenVerifier signature.
func (v *Verifier) Verify(ctx context.Context, token string, _ *http.Request) (*mcpauth.TokenInfo, error) {
	idToken, err := v.verifier.Verify(ctx, token)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", mcpauth.ErrInvalidToken, err)
	}

	// idToken.Audience is the aud claim already normalized to a list by go-oidc.
	if !contains(idToken.Audience, v.cfg.ResourceURI) {
		return nil, fmt.Errorf("%w: token audience %v does not include %s", mcpauth.ErrInvalidToken, idToken.Audience, v.cfg.ResourceURI)
	}

	var claims map[string]any
	if err := idToken.Claims(&claims); err != nil {
		return nil, fmt.Errorf("%w: reading claims: %v", mcpauth.ErrInvalidToken, err)
	}
	if !accessAllowed(claims, v.cfg.RequiredClaim, v.cfg.RequiredValue) {
		return nil, fmt.Errorf("%w: missing required claim %s=%s", mcpauth.ErrInvalidToken, v.cfg.RequiredClaim, v.cfg.RequiredValue)
	}

	// UserID binds the session to a principal (the SDK uses it to prevent session
	// hijacking). An empty id would collapse distinct no-identity principals into
	// one, so fail closed rather than issue a session with no identity.
	userID := identity(v.cfg.AccessPolicy, claims)
	if userID == "" {
		return nil, fmt.Errorf("%w: token carries no usable identity claim", mcpauth.ErrInvalidToken)
	}

	return &mcpauth.TokenInfo{
		UserID:     userID,
		Expiration: idToken.Expiry,
		Extra:      claims,
	}, nil
}

// identity picks the configured identity claim, falling back to preferred_username,
// email, then sub so an issuer that omits one (e.g. Entra omits email) still yields
// a stable user id. sub is last: it is always present in a spec-compliant token but
// is opaque, so a human-readable claim wins when available. Whitespace-only values
// are treated as absent so they cannot become a collapsing "   " identity.
func identity(policy config.AccessPolicy, claims map[string]any) string {
	for _, key := range []string{policy.IdentityClaim, "preferred_username", "email", "sub"} {
		if s, ok := claims[key].(string); ok {
			if trimmed := strings.TrimSpace(s); trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// accessAllowed reports whether the access gate passes. An empty requiredClaim
// means authenticate-only (no gate). The claim may be a JSON array (groups/roles)
// or a string; a string is treated as space-delimited (as OAuth scope and some
// issuers' role claims are), so "admin mcp-users" grants membership of either.
// Any other shape (number, object, absent) fails closed.
func accessAllowed(claims map[string]any, requiredClaim, requiredValue string) bool {
	if requiredClaim == "" {
		return true
	}
	switch c := claims[requiredClaim].(type) {
	case string:
		return contains(strings.Fields(c), requiredValue)
	case []any:
		for _, item := range c {
			if s, ok := item.(string); ok && s == requiredValue {
				return true
			}
		}
	}
	return false
}
