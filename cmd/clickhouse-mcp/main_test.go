package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/corker/clickhouse-mcp/internal/config"
)

// testClient is a dedicated client so tests never share a connection pool with
// each other (or with a prior test's now-dead :0 server) via http.DefaultClient.
var testClient = &http.Client{}

// postMCP sends one MCP message. sessionID is empty on the initialize call, then
// taken from that response's header for subsequent calls.
func postMCP(addr, body, sessionID string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/", strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	return testClient.Do(req)
}

const initBody = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`

// serveOnFreePort runs the server on an OS-assigned port (127.0.0.1:0) so
// concurrent tests never collide on a fixed port.
func serveOnFreePort(t *testing.T, s *mcp.Server) (addr string, stop func(), done chan error) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done = make(chan error, 1)
	go func() { done <- runHTTP(ctx, s, ln, nil) }()
	return ln.Addr().String(), cancel, done
}

// waitReady polls until the initialize handshake succeeds, returning the session
// id — so a test never races the listener's first Accept.
func waitReady(t *testing.T, addr string) string {
	t.Helper()
	for i := 0; i < 50; i++ {
		resp, err := postMCP(addr, initBody, "")
		if err == nil {
			sid := resp.Header.Get("Mcp-Session-Id")
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			return sid
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("server never became ready")
	return ""
}

// runHTTP takes an injected listener, so it can be driven without binding a fixed
// port or standing up ClickHouse: a bare server with no tools still completes the
// MCP initialize handshake, which is enough to prove the transport is wired and
// that ctx-cancel returns cleanly (no hang, no leaked goroutine).
func TestRunHTTP_ServesAndShutsDown(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "test"}, nil)
	addr, stop, done := serveOnFreePort(t, s)

	if sid := waitReady(t, addr); sid == "" {
		t.Error("initialize should return a session id")
	}

	stop()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("runHTTP returned %v, want nil on ctx cancel", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runHTTP did not return after ctx cancel (hang or leaked goroutine)")
	}
}

// An in-flight request must be DRAINED to completion on shutdown, not severed —
// this is the behavior that makes srv.Shutdown correct and srv.Close wrong.
// Regression guard: reverting to Close would break this.
func TestRunHTTP_DrainsInflightOnShutdown(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "test"}, nil)
	// The tool signals when it has actually started, then blocks — so the test
	// triggers shutdown only once the request is provably in-flight (a fixed
	// sleep would race a slow CI box: shutdown could fire before the call began).
	started := make(chan struct{})
	type noArgs struct{}
	mcp.AddTool(s, &mcp.Tool{Name: "slow"}, func(_ context.Context, _ *mcp.CallToolRequest, _ noArgs) (*mcp.CallToolResult, any, error) {
		close(started)
		time.Sleep(200 * time.Millisecond)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "tool-finished"}}}, nil, nil
	})
	addr, stop, done := serveOnFreePort(t, s)
	sid := waitReady(t, addr)

	var wg sync.WaitGroup
	wg.Add(1)
	var status int
	var body string
	var callErr error
	go func() {
		defer wg.Done()
		resp, err := postMCP(addr, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"slow","arguments":{}}}`, sid)
		if err != nil {
			callErr = err
			return
		}
		status = resp.StatusCode
		b, _ := io.ReadAll(resp.Body)
		body = string(b)
		_ = resp.Body.Close()
	}()

	<-started // the tool is now executing; the request is in-flight
	stop()    // shut down WHILE the call runs
	wg.Wait()

	if callErr != nil {
		t.Fatalf("in-flight call was severed, not drained: %v", callErr)
	}
	if status != http.StatusOK {
		t.Errorf("in-flight call status = %d, want 200 (drained)", status)
	}
	// The tool's output must be in the response — proving the call ran to
	// completion (drained), not that it merely got a status line before a sever.
	if !strings.Contains(body, "tool-finished") {
		t.Errorf("drained response must contain the tool output; got %q", body)
	}
	<-done
}

// A listener error (not a clean shutdown) must propagate, not be swallowed as
// ErrServerClosed — otherwise main would exit 0 on a real failure.
func TestRunHTTP_PropagatesServeError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := mcp.NewServer(&mcp.Implementation{Name: "test"}, nil)
	done := make(chan error, 1)
	go func() { done <- runHTTP(context.Background(), s, ln, nil) }() // ctx never cancels

	// Closing the listener makes Serve return a real error regardless of whether
	// it has reached Accept yet, so no synchronizing sleep is needed.
	_ = ln.Close()

	select {
	case err := <-done:
		if err == nil || errors.Is(err, http.ErrServerClosed) {
			t.Errorf("a listener error should propagate, not be swallowed; got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runHTTP hung after listener closed")
	}
}

func TestAuthMiddleware_OffIsNil(t *testing.T) {
	mw, err := authMiddleware(context.Background(), config.ServerConfig{AuthMode: config.AuthOff})
	if err != nil || mw != nil {
		t.Errorf("auth off should yield no middleware: mw=%v err=%v", mw != nil, err)
	}
}

func TestAuthMiddleware_BearerFailsOnBadIssuer(t *testing.T) {
	// Discovery must fail fast at startup, not per-request, when the issuer is
	// unreachable/invalid.
	_, err := authMiddleware(context.Background(), config.ServerConfig{
		AuthMode: config.AuthBearer,
		OIDC: config.OIDCConfig{
			Issuer:      "http://127.0.0.1:1/nonexistent", // nothing listening
			ResourceURI: "https://mcp.example",
		},
	})
	if err == nil {
		t.Error("bearer with an unreachable issuer should error at startup")
	}
}
