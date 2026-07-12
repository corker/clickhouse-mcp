package auth

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func testProxy() ProxyConfig {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	return ProxyConfig{
		Broker:               testBroker(),
		UpstreamAuthURL:      "https://login.microsoftonline.com/tenant/oauth2/v2.0/authorize",
		UpstreamTokenURL:     "https://login.microsoftonline.com/tenant/oauth2/v2.0/token",
		AllowedRedirectHosts: []string{"claude.ai"},
		stateKey:             key,
	}
}

// The redirect guard is the open-redirect / account-takeover defense. Adversarial
// URIs must be rejected; legitimate client callbacks accepted.
func TestValidateClientRedirect(t *testing.T) {
	p := testProxy()
	tests := []struct {
		name    string
		uri     string
		allowed bool
	}{
		{"loopback http", "http://127.0.0.1:1234/cb", true},
		{"localhost http", "http://localhost:9000/callback", true},
		{"ipv6 loopback", "http://[::1]:8080/cb", true},
		{"allowed host https", "https://claude.ai/api/mcp/callback", true},
		{"allowed subdomain", "https://sub.claude.ai/cb", true},
		{"empty", "", false},
		{"evil host", "https://evil.com/cb", false},
		{"lookalike suffix", "https://notclaude.ai/cb", false}, // must not match "claude.ai"
		{"evil with allowed in path", "https://evil.com/claude.ai/cb", false},
		{"http non-loopback", "http://claude.ai/cb", false}, // https required off-loopback
		{"javascript scheme", "javascript:alert(1)", false},
		{"data scheme", "data:text/html,x", false},
		{"has fragment", "https://claude.ai/cb#frag", false},
		{"unparseable", "https://%zz", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := p.validateClientRedirect(tt.uri)
			if tt.allowed && err != nil {
				t.Errorf("%q should be allowed, got %v", tt.uri, err)
			}
			if !tt.allowed && err == nil {
				t.Errorf("%q should be rejected, got nil", tt.uri)
			}
		})
	}
}

// A default proxy (no AllowedRedirectHosts) permits only loopback.
func TestValidateClientRedirect_DefaultLoopbackOnly(t *testing.T) {
	p := testProxy()
	p.AllowedRedirectHosts = nil
	if err := p.validateClientRedirect("http://localhost:3000/cb"); err != nil {
		t.Errorf("loopback should be allowed by default, got %v", err)
	}
	if err := p.validateClientRedirect("https://claude.ai/cb"); err == nil {
		t.Error("a non-loopback host must be rejected when none are allow-listed")
	}
}

func TestSignState_RoundTrips(t *testing.T) {
	p := testProxy()
	signed, err := p.signState("http://localhost:1234/cb", "client-state-xyz")
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	redirect, state, err := p.verifyState(signed)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if redirect != "http://localhost:1234/cb" || state != "client-state-xyz" {
		t.Errorf("round-trip mismatch: redirect=%q state=%q", redirect, state)
	}
}

// A tampered state — attacker swaps in their own redirect — must fail the signature
// check. This is the account-takeover defense.
func TestVerifyState_RejectsTampered(t *testing.T) {
	p := testProxy()
	signed, _ := p.signState("http://localhost:1234/cb", "s")

	// Flip a base64 char to corrupt the payload/signature.
	tampered := "A" + signed[1:]
	if _, _, err := p.verifyState(tampered); err == nil {
		t.Error("a tampered state must be rejected")
	}

	// A state signed with a DIFFERENT key must not verify under ours.
	other := testProxy()
	otherKey := make([]byte, 32)
	for i := range otherKey {
		otherKey[i] = byte(255 - i)
	}
	other.stateKey = otherKey
	forged, _ := other.signState("https://evil.com/steal", "s")
	if _, _, err := p.verifyState(forged); err == nil {
		t.Error("a state signed with another key must be rejected (no cross-key trust)")
	}
}

func TestVerifyState_RejectsExpired(t *testing.T) {
	p := testProxy()
	// Hand-build an expired-but-correctly-signed state.
	d := stateData{
		Redirect:  "http://localhost/cb",
		State:     "s",
		Nonce:     "abcd",
		Timestamp: time.Now().Add(-2 * maxStateAge).Unix(),
	}
	d.Sig, _ = p.stateMAC(d)
	raw, _ := json.Marshal(d)
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	if _, _, err := p.verifyState(encoded); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Errorf("expired state must be rejected as expired, got %v", err)
	}
}

// A far-future timestamp (only reachable if the key leaked) must be rejected too,
// so a compromised state cannot become a long-lived replay token.
func TestVerifyState_RejectsFuture(t *testing.T) {
	p := testProxy()
	d := stateData{
		Redirect:  "http://localhost/cb",
		State:     "s",
		Nonce:     "abcd",
		Timestamp: time.Now().Add(1 * time.Hour).Unix(),
	}
	d.Sig, _ = p.stateMAC(d)
	raw, _ := json.Marshal(d)
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	if _, _, err := p.verifyState(encoded); err == nil {
		t.Error("a far-future timestamp must be rejected")
	}
}
