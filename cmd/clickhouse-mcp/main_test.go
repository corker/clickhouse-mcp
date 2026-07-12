package main

import (
	"context"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// runHTTP takes an injected listener, so it can be driven without binding a fixed
// port or standing up ClickHouse: a bare server with no tools still completes the
// MCP initialize handshake, which is enough to prove the transport is wired and
// that ctx-cancel returns cleanly (no hang, no leaked goroutine).
func TestRunHTTP_ServesAndShutsDown(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0") // :0 = OS-assigned free port
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()

	s := mcp.NewServer(&mcp.Implementation{Name: "test"}, nil)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- runHTTP(ctx, s, ln) }()

	// The handler should answer an initialize over streamable HTTP.
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`
	req, _ := http.NewRequest(http.MethodPost, "http://"+addr+"/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")

	var resp *http.Response
	for i := 0; i < 50; i++ { // poll until the listener is accepting
		resp, err = http.DefaultClient.Do(req)
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
		req, _ = http.NewRequest(http.MethodPost, "http://"+addr+"/", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
	}
	if err != nil {
		t.Fatalf("request never succeeded: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("initialize status = %d, want 200", resp.StatusCode)
	}
	_ = resp.Body.Close()

	// Cancelling the context must make runHTTP return cleanly and promptly.
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("runHTTP returned %v, want nil on ctx cancel", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runHTTP did not return after ctx cancel (hang or leaked goroutine)")
	}
}
