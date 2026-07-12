package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxOAuthField = 4096 // cap OAuth query/form values against DoS.

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
// blob: an HMAC binds all fields so the callback can trust the redirect it carries.
// The nonce makes identical (redirect,state) pairs produce distinct blobs; it is
// not a replay guard (the OAuth code it ultimately carries is single-use upstream).
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
	mac, err := p.stateMAC(data)
	if err != nil {
		return "", err
	}
	data.Sig = mac
	raw, err := json.Marshal(data)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// verifyState decodes and authenticates a state blob, rejecting a bad signature or
// a timestamp outside the freshness window (too old, or in the future — a clock-
// skew/compromise guard against a long-lived token). Returns the client's redirect
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
	want, err := p.stateMAC(d)
	if err != nil {
		return "", "", fmt.Errorf("state not verifiable")
	}
	if !hmac.Equal([]byte(d.Sig), []byte(want)) {
		return "", "", fmt.Errorf("state signature mismatch")
	}
	age := time.Since(time.Unix(d.Timestamp, 0))
	if age > maxStateAge || age < -clockSkew {
		return "", "", fmt.Errorf("state expired")
	}
	return d.Redirect, d.State, nil
}

const (
	maxStateAge = 10 * time.Minute
	clockSkew   = 1 * time.Minute
)

type stateData struct {
	Redirect  string `json:"redirect"`
	State     string `json:"state"`
	Nonce     string `json:"nonce"`
	Timestamp int64  `json:"timestamp"`
	Sig       string `json:"sig,omitempty"`
}

// stateMAC computes the HMAC over the canonical JSON of the state with the
// signature field cleared. Signing the marshaled bytes (rather than a hand-built
// delimited string) removes any field-boundary ambiguity between fields.
func (p ProxyConfig) stateMAC(d stateData) (string, error) {
	d.Sig = ""
	canonical, err := json.Marshal(d)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, p.stateKey)
	mac.Write(canonical)
	return hex.EncodeToString(mac.Sum(nil)), nil
}

// HandleAuthorize starts the proxied auth-code+PKCE flow: validate the client's
// redirect, sign it into state, and redirect to the upstream with OUR client_id,
// OUR fixed callback, and the client's PKCE challenge passed through unchanged.
func (p ProxyConfig) HandleAuthorize(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	clientRedirect := q.Get("redirect_uri")
	if err := p.validateClientRedirect(clientRedirect); err != nil {
		// Do not echo the rejected URI back — just refuse.
		http.Error(w, "invalid redirect_uri", http.StatusBadRequest)
		return
	}
	challenge := q.Get("code_challenge")
	method := q.Get("code_challenge_method")
	if len(challenge) > maxOAuthField || challenge == "" || method != "S256" {
		http.Error(w, "PKCE S256 code_challenge required", http.StatusBadRequest)
		return
	}

	signed, err := p.signState(clientRedirect, q.Get("state"))
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	up, err := url.Parse(p.UpstreamAuthURL)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	uq := up.Query()
	uq.Set("client_id", p.Broker.ClientID)
	uq.Set("response_type", "code")
	uq.Set("redirect_uri", p.Broker.PublicURL+"/oauth/callback") // our fixed callback
	uq.Set("code_challenge", challenge)
	uq.Set("code_challenge_method", "S256")
	uq.Set("state", signed)
	if s := strings.Join(p.Broker.Scopes, " "); s != "" {
		uq.Set("scope", s)
	}
	up.RawQuery = uq.Encode()
	http.Redirect(w, r, up.String(), http.StatusFound)
}

// HandleCallback receives the upstream redirect (to our fixed callback), verifies
// the signed state, and redirects the browser to the client's real redirect_uri
// with the code and the client's original state.
func (p ProxyConfig) HandleCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		http.Error(w, "upstream authorization failed", http.StatusBadRequest)
		return
	}
	code := q.Get("code")
	if code == "" || len(code) > maxOAuthField {
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}

	clientRedirect, clientState, err := p.verifyState(q.Get("state"))
	if err != nil {
		http.Error(w, "invalid state", http.StatusBadRequest)
		return
	}
	// Defense in depth: re-validate the redirect from the (now trusted) state
	// before sending the browser to it.
	if err := p.validateClientRedirect(clientRedirect); err != nil {
		http.Error(w, "invalid state redirect", http.StatusBadRequest)
		return
	}

	dest, err := url.Parse(clientRedirect)
	if err != nil {
		http.Error(w, "invalid state redirect", http.StatusBadRequest)
		return
	}
	dq := dest.Query()
	dq.Set("code", code)
	if clientState != "" {
		dq.Set("state", clientState)
	}
	dest.RawQuery = dq.Encode()
	http.Redirect(w, r, dest.String(), http.StatusFound)
}

// HandleToken proxies the code exchange to the upstream token endpoint,
// server-to-server, adding the client_id/secret the public client does not hold.
// The client's PKCE code_verifier is passed through. The upstream's token
// response is returned verbatim.
func (p ProxyConfig) HandleToken(w http.ResponseWriter, r *http.Request) {
	corsMetadata(w, "POST, OPTIONS")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	// Only the grants we advertise (authorization_code, refresh_token) may borrow
	// the broker's confidential client_secret. Refuse others before contacting the
	// upstream, so a client cannot lend our credentials to client_credentials,
	// password, or on-behalf-of grants it could never invoke on its own.
	grant := r.PostForm.Get("grant_type")
	if grant != "authorization_code" && grant != "refresh_token" {
		http.Error(w, "unsupported grant_type", http.StatusBadRequest)
		return
	}

	form := url.Values{}
	form.Set("grant_type", grant)
	form.Set("code", r.PostForm.Get("code"))
	form.Set("code_verifier", r.PostForm.Get("code_verifier"))
	form.Set("redirect_uri", p.Broker.PublicURL+"/oauth/callback") // must match authorize
	form.Set("client_id", p.Broker.ClientID)
	form.Set("client_secret", p.ClientSecret)
	if rt := r.PostForm.Get("refresh_token"); rt != "" {
		form.Set("refresh_token", rt)
	}

	// Bound the outbound call so a hung upstream can't tie up this (public,
	// unauthenticated) endpoint indefinitely.
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.UpstreamTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, "upstream token exchange failed", http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(resp.Body, 1<<20))
}
