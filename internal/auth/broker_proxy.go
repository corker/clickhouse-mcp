package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ProxyConfig adds the authorize/callback/token proxy to BrokerConfig (slice 3b).
// The broker registers ONE redirect URI with the upstream — its own /oauth/callback
// — and carries the client's real redirect_uri through a signed state, so the
// upstream never needs to know client callback URLs (Entra's one-URI constraint).
type ProxyConfig struct {
	Broker BrokerConfig
	// UpstreamAuthURL / UpstreamTokenURL are the IdP's real endpoints.
	UpstreamAuthURL  string
	UpstreamTokenURL string
	// ClientSecret authenticates the broker to the upstream token endpoint.
	ClientSecret string
	// AllowedRedirectHosts are the non-loopback host suffixes a client redirect_uri
	// may use (e.g. "claude.ai"). Loopback is always allowed. Empty means only
	// loopback — the safe default.
	AllowedRedirectHosts []string
	// stateKey signs the state blob (HMAC-SHA256). Generated at startup.
	stateKey []byte
}

// validateClientRedirect enforces the guards that stop open-redirect / account-
// takeover: present, parseable, http/https only, HTTPS unless loopback, no
// fragment, and host is loopback or an allowed suffix. Returns nil if safe.
func (p ProxyConfig) validateClientRedirect(raw string) error {
	if raw == "" {
		return fmt.Errorf("missing redirect_uri")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("unparseable redirect_uri")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("redirect_uri scheme must be http or https")
	}
	if u.Fragment != "" {
		return fmt.Errorf("redirect_uri must not contain a fragment")
	}
	loopback := isLoopbackHost(u.Hostname())
	if u.Scheme == "http" && !loopback {
		return fmt.Errorf("redirect_uri must use https unless loopback")
	}
	if loopback {
		return nil
	}
	host := u.Hostname()
	for _, suffix := range p.AllowedRedirectHosts {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return nil
		}
	}
	return fmt.Errorf("redirect_uri host %q not allowed", host)
}

func isLoopbackHost(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

// signState packs the client's real redirect + original state into a tamper-proof
// blob: an HMAC over the fields plus a nonce and timestamp for replay resistance.
func (p ProxyConfig) signState(clientRedirect, clientState string) (string, error) {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	data := stateData{
		Redirect:  clientRedirect,
		State:     clientState,
		Nonce:     hex.EncodeToString(nonce),
		Timestamp: time.Now().Unix(),
	}
	data.Sig = p.stateMAC(data)
	raw, err := json.Marshal(data)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// verifyState decodes and authenticates a state blob, rejecting a bad signature or
// one older than maxStateAge (replay/stale defense). Returns the client's redirect
// and original state.
func (p ProxyConfig) verifyState(encoded string) (redirect, state string, err error) {
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", "", fmt.Errorf("state not decodable")
	}
	var d stateData
	if err := json.Unmarshal(raw, &d); err != nil {
		return "", "", fmt.Errorf("state not parseable")
	}
	if !hmac.Equal([]byte(d.Sig), []byte(p.stateMAC(d))) {
		return "", "", fmt.Errorf("state signature mismatch")
	}
	if time.Since(time.Unix(d.Timestamp, 0)) > maxStateAge {
		return "", "", fmt.Errorf("state expired")
	}
	return d.Redirect, d.State, nil
}

const maxStateAge = 10 * time.Minute

type stateData struct {
	Redirect  string `json:"redirect"`
	State     string `json:"state"`
	Nonce     string `json:"nonce"`
	Timestamp int64  `json:"timestamp"`
	Sig       string `json:"sig,omitempty"`
}

// stateMAC computes the HMAC over the state fields (excluding the signature).
func (p ProxyConfig) stateMAC(d stateData) string {
	mac := hmac.New(sha256.New, p.stateKey)
	// A fixed field order and separator so the signed bytes are unambiguous.
	// hash.Hash.Write never returns an error.
	_, _ = fmt.Fprintf(mac, "redirect=%s&state=%s&nonce=%s&timestamp=%s",
		d.Redirect, d.State, d.Nonce, strconv.FormatInt(d.Timestamp, 10))
	return hex.EncodeToString(mac.Sum(nil))
}
