package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// postMCP sends one streamable-HTTP MCP message to the server at addr. sessionID
// is optional (empty on the initialize call, then set from the response header).
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
	return http.DefaultClient.Do(req)
}

const initBody = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`

// serveOnFreePort starts runHTTP on an OS-assigned port and returns its address,
// a stop func, and the channel runHTTP's return lands on.
func serveOnFreePort(t *testing.T, s *mcp.Server) (addr string, stop func(), done chan error) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done = make(chan error, 1)
	go func() { done <- runHTTP(ctx, s, ln) }()
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
	type noArgs struct{}
	mcp.AddTool(s, &mcp.Tool{Name: "slow"}, func(_ context.Context, _ *mcp.CallToolRequest, _ noArgs) (*mcp.CallToolResult, any, error) {
		time.Sleep(300 * time.Millisecond)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "done"}}}, nil, nil
	})
	addr, stop, done := serveOnFreePort(t, s)
	sid := waitReady(t, addr)

	var wg sync.WaitGroup
	wg.Add(1)
	var status int
	var callErr error
	go func() {
		defer wg.Done()
		resp, err := postMCP(addr, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"slow","arguments":{}}}`, sid)
		if err != nil {
			callErr = err
			return
		}
		status = resp.StatusCode
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	time.Sleep(80 * time.Millisecond) // the slow tool is now mid-flight
	stop()                            // shut down WHILE the call runs
	wg.Wait()

	if callErr != nil {
		t.Errorf("in-flight call was severed, not drained: %v", callErr)
	}
	if status != http.StatusOK {
		t.Errorf("in-flight call status = %d, want 200 (drained)", status)
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
	go func() { done <- runHTTP(context.Background(), s, ln) }() // ctx never cancels

	time.Sleep(50 * time.Millisecond)
	_ = ln.Close() // Serve returns a real error, not ErrServerClosed

	select {
	case err := <-done:
		if err == nil {
			t.Error("a listener error should propagate, not be swallowed")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runHTTP hung after listener closed")
	}
}
