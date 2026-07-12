// Command clickhouse-mcp is a Model Context Protocol server for ClickHouse.
package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/corker/clickhouse-mcp/internal/clickhouse"
	"github.com/corker/clickhouse-mcp/internal/config"
	"github.com/corker/clickhouse-mcp/internal/server"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	conn, err := clickhouse.New(ctx, cfg)
	if err != nil {
		log.Fatalf("clickhouse: %v", err)
	}
	defer func() { _ = conn.Close() }()

	s := server.New("clickhouse-mcp", conn)

	switch cfg.Server.Transport {
	case config.TransportHTTP:
		var ln net.Listener
		ln, err = net.Listen("tcp", cfg.Server.HTTPAddr)
		if err != nil {
			log.Fatalf("http listen on %s: %v", cfg.Server.HTTPAddr, err)
		}
		log.Printf("serving MCP over HTTP on %s", ln.Addr())
		err = runHTTP(ctx, s, ln)
	default:
		err = s.Run(ctx, &mcp.StdioTransport{})
	}
	if err != nil {
		log.Fatalf("server: %v", err)
	}
}

// runHTTP serves the MCP server over streamable HTTP on ln until the context is
// cancelled, then drains in-flight sessions (bounded) and stops. The same
// *mcp.Server backs every session — the SDK creates a per-connection session, so
// the shared server is just a factory over the one (concurrency-safe) ClickHouse
// connection. Takes a listener so callers (and tests) own the bind.
func runHTTP(ctx context.Context, s *mcp.Server, ln net.Listener) error {
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, nil)
	// ReadHeaderTimeout bounds slow-header (Slowloris) clients. Streamable HTTP
	// keeps response bodies open for server-sent events, so no WriteTimeout.
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}

	srvErr := make(chan error, 1)
	go func() { srvErr <- srv.Serve(ln) }()

	select {
	case err := <-srvErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		// Drain in-flight requests, but bound the wait: streamable SSE streams
		// never end on their own, so fall back to an abrupt Close on timeout.
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutCtx); err != nil {
			_ = srv.Close()
		}
		return nil
	}
}
