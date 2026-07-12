package auth

import (
	"testing"

	"github.com/corker/clickhouse-mcp/internal/config"
)

func TestAccessAllowed(t *testing.T) {
	tests := []struct {
		name         string
		claims       map[string]any
		claim, value string
		want         bool
	}{
		{"no gate (empty claim) allows all", map[string]any{}, "", "", true},
		{"string claim matches", map[string]any{"role": "admin"}, "role", "admin", true},
		{"string claim mismatch", map[string]any{"role": "user"}, "role", "admin", false},
		{"list claim contains value", map[string]any{"groups": []any{"a", "mcp-users"}}, "groups", "mcp-users", true},
		{"list claim missing value", map[string]any{"groups": []any{"a", "b"}}, "groups", "mcp-users", false},
		{"space-delimited string contains value", map[string]any{"roles": "admin mcp-users"}, "roles", "mcp-users", true},
		{"space-delimited string missing value", map[string]any{"roles": "admin viewer"}, "roles", "mcp-users", false},
		{"claim absent", map[string]any{}, "groups", "mcp-users", false},
		{"claim wrong type (number)", map[string]any{"groups": 42}, "groups", "mcp-users", false},
	}
	for _, tt := range tests {
		if got := accessAllowed(tt.claims, tt.claim, tt.value); got != tt.want {
			t.Errorf("%s: accessAllowed = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestIdentity(t *testing.T) {
	tests := []struct {
		name        string
		configClaim string
		claims      map[string]any
		want        string
	}{
		{"configured claim wins", "email", map[string]any{"email": "a@b.c", "preferred_username": "ab"}, "a@b.c"},
		{"falls back to preferred_username", "email", map[string]any{"preferred_username": "ab"}, "ab"},
		{"falls back to email", "role", map[string]any{"email": "a@b.c"}, "a@b.c"},
		{"falls back to sub last", "email", map[string]any{"sub": "opaque-id"}, "opaque-id"},
		{"nothing usable -> empty", "email", map[string]any{"role": "admin"}, ""},
		{"empty string skipped", "email", map[string]any{"email": "", "preferred_username": "ab"}, "ab"},
		{"whitespace-only skipped (would collapse sessions)", "email", map[string]any{"email": "   ", "preferred_username": "ab"}, "ab"},
		{"trims the returned value", "email", map[string]any{"email": " a@b.c "}, "a@b.c"},
	}
	for _, tt := range tests {
		policy := config.AccessPolicy{IdentityClaim: tt.configClaim}
		if got := identity(policy, tt.claims); got != tt.want {
			t.Errorf("%s: identity = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestContains(t *testing.T) {
	if !contains([]string{"a", "https://mcp.example"}, "https://mcp.example") {
		t.Error("should find the resource uri in the audience list")
	}
	if contains([]string{"other"}, "https://mcp.example") {
		t.Error("must not match a token minted for another audience")
	}
	if contains(nil, "https://mcp.example") {
		t.Error("empty audience must not match")
	}
}
