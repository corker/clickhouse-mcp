package auth

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// fakeUpstream stands in for Entra's authorize + token endpoints. It records what
// the broker sends so tests can assert the proxy behavior (client_id swap, fixed
// callback, PKCE passthrough).
type fakeUpstream struct {
	server        *httptest.Server
	gotClientID   string
	gotRedirect   string
	gotChallenge  string
	gotTokenForm  url.Values
	tokenResponse string
}

func newFakeUpstream(t *testing.T) *fakeUpstream {
	t.Helper()
	fu := &fakeUpstream{tokenResponse: `{"access_token":"upstream-token","token_type":"Bearer","expires_in":3600}`}
	mux := http.NewServeMux()
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		fu.gotClientID = q.Get("client_id")
		fu.gotRedirect = q.Get("redirect_uri")
		fu.gotChallenge = q.Get("code_challenge")
		// Behave like the IdP: redirect back to the broker's callback with a code,
		// preserving the (broker-signed) state.
		back, _ := url.Parse(q.Get("redirect_uri"))
		bq := back.Query()
		bq.Set("code", "upstream-auth-code")
		bq.Set("state", q.Get("state"))
		back.RawQuery = bq.Encode()
		// This mock deliberately reflects the received redirect_uri — that is what a
		// real IdP does. The broker under test is what validates redirects.
		w.Header().Set("Location", back.String()) //nolint:gosec // test IdP mock, intentional reflect
		w.WriteHeader(http.StatusFound)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		fu.gotTokenForm = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, fu.tokenResponse)
	})
	fu.server = httptest.NewServer(mux)
	t.Cleanup(fu.server.Close)
	return fu
}

func proxyWithUpstream(fu *fakeUpstream) ProxyConfig {
	p := testProxy()
	p.UpstreamAuthURL = fu.server.URL + "/authorize"
	p.UpstreamTokenURL = fu.server.URL + "/token"
	p.ClientSecret = "broker-holds-this"
	return p
}

// The full happy path: authorize → (upstream) → callback → the client gets the
// code at its own redirect_uri, and the broker swapped in our client_id + fixed
// callback while passing the client's PKCE challenge through.
func TestProxyFlow_AuthorizeThroughCallback(t *testing.T) {
	fu := newFakeUpstream(t)
	p := proxyWithUpstream(fu)

	// 1. Client hits authorize. Follow no redirects — inspect the Location.
	authReq := httptest.NewRequest(http.MethodGet, "/oauth/authorize?"+url.Values{
		"redirect_uri":          {"http://localhost:5555/cb"},
		"code_challenge":        {"abc123challenge"},
		"code_challenge_method": {"S256"},
		"state":                 {"client-state-1"},
	}.Encode(), nil)
	rec := httptest.NewRecorder()
	p.HandleAuthorize(rec, authReq)
	if rec.Code != http.StatusFound {
		t.Fatalf("authorize status = %d, want 302", rec.Code)
	}
	upstreamURL, _ := url.Parse(rec.Header().Get("Location"))
	// The broker must send OUR client_id + OUR callback + the client's challenge.
	uq := upstreamURL.Query()
	if uq.Get("client_id") != p.Broker.ClientID {
		t.Errorf("upstream client_id = %q, want the pre-registered app", uq.Get("client_id"))
	}
	if uq.Get("redirect_uri") != p.Broker.PublicURL+"/oauth/callback" {
		t.Errorf("upstream redirect_uri = %q, want our fixed callback", uq.Get("redirect_uri"))
	}
	if uq.Get("code_challenge") != "abc123challenge" {
		t.Errorf("PKCE challenge not passed through: %q", uq.Get("code_challenge"))
	}
	signedState := uq.Get("state")
	if signedState == "client-state-1" {
		t.Error("state must be the broker's signed blob, not the client's raw state")
	}

	// 2. Simulate the upstream redirecting to the broker callback with a code.
	cbReq := httptest.NewRequest(http.MethodGet, "/oauth/callback?"+url.Values{
		"code":  {"upstream-auth-code"},
		"state": {signedState},
	}.Encode(), nil)
	rec = httptest.NewRecorder()
	p.HandleCallback(rec, cbReq)
	if rec.Code != http.StatusFound {
		t.Fatalf("callback status = %d, want 302", rec.Code)
	}
	// 3. The browser must be redirected to the CLIENT's real redirect_uri with the
	// code and the client's original state.
	clientDest, _ := url.Parse(rec.Header().Get("Location"))
	if clientDest.Scheme+"://"+clientDest.Host+clientDest.Path != "http://localhost:5555/cb" {
		t.Errorf("callback redirected to %q, want the client's URI", clientDest.String())
	}
	cq := clientDest.Query()
	if cq.Get("code") != "upstream-auth-code" {
		t.Errorf("client did not receive the code: %q", cq.Get("code"))
	}
	if cq.Get("state") != "client-state-1" {
		t.Errorf("client state = %q, want the original client-state-1", cq.Get("state"))
	}
}

// The token endpoint proxies the exchange to the upstream, adding the client
// secret the public client doesn't hold, and returns the upstream response.
func TestProxyFlow_TokenExchange(t *testing.T) {
	fu := newFakeUpstream(t)
	p := proxyWithUpstream(fu)

	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {"upstream-auth-code"},
		"code_verifier": {"the-pkce-verifier"},
		"redirect_uri":  {"http://localhost:5555/cb"},
	}
	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	p.HandleToken(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("token status = %d, want 200", rec.Code)
	}
	var tok map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &tok); err != nil {
		t.Fatalf("token body not JSON: %v", err)
	}
	if tok["access_token"] != "upstream-token" {
		t.Errorf("access_token = %v, want the upstream token", tok["access_token"])
	}
	// The broker must have added client_id + secret and passed the verifier.
	if fu.gotTokenForm.Get("client_id") != p.Broker.ClientID {
		t.Errorf("upstream got client_id = %q", fu.gotTokenForm.Get("client_id"))
	}
	if fu.gotTokenForm.Get("client_secret") != "broker-holds-this" {
		t.Error("broker must add the client_secret the public client lacks")
	}
	if fu.gotTokenForm.Get("code_verifier") != "the-pkce-verifier" {
		t.Error("PKCE code_verifier must be passed through")
	}
}

// Adversarial: authorize with an evil redirect must be refused before any state
// is signed or the upstream is contacted.
func TestProxyFlow_AuthorizeRejectsEvilRedirect(t *testing.T) {
	fu := newFakeUpstream(t)
	p := proxyWithUpstream(fu)
	req := httptest.NewRequest(http.MethodGet, "/oauth/authorize?"+url.Values{
		"redirect_uri":          {"https://evil.com/steal"},
		"code_challenge":        {"c"},
		"code_challenge_method": {"S256"},
	}.Encode(), nil)
	rec := httptest.NewRecorder()
	p.HandleAuthorize(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("evil redirect_uri must be refused, got %d", rec.Code)
	}
}

// Adversarial: a callback with a forged/absent state must not redirect anywhere.
func TestProxyFlow_CallbackRejectsForgedState(t *testing.T) {
	fu := newFakeUpstream(t)
	p := proxyWithUpstream(fu)
	req := httptest.NewRequest(http.MethodGet, "/oauth/callback?"+url.Values{
		"code":  {"c"},
		"state": {"not-a-signed-state"},
	}.Encode(), nil)
	rec := httptest.NewRecorder()
	p.HandleCallback(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("forged state must be rejected, got %d (location=%q)", rec.Code, rec.Header().Get("Location"))
	}
}
