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

	mcpauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/corker/clickhouse-mcp/internal/auth"
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
		mw, mwErr := authMiddleware(ctx, cfg.Server)
		if mwErr != nil {
			log.Fatalf("auth: %v", mwErr)
		}
		ln, lnErr := net.Listen("tcp", cfg.Server.HTTPAddr)
		if lnErr != nil {
			log.Fatalf("http listen on %s: %v", cfg.Server.HTTPAddr, lnErr)
		}
		log.Printf("serving MCP over HTTP on %s (auth: %s)", ln.Addr(), cfg.Server.AuthMode)
		err = runHTTP(ctx, s, ln, mw)
	default:
		err = s.Run(ctx, &mcp.StdioTransport{})
	}
	if err != nil {
		log.Fatalf("server: %v", err)
	}
}

// authMiddleware builds the HTTP auth gate for the configured mode. off → nil (no
// gate). bearer → the SDK's RequireBearerToken backed by our OIDC verifier; the
// verifier is built here so a discovery failure aborts startup rather than
// surfacing per-request.
func authMiddleware(ctx context.Context, cfg config.ServerConfig) (func(http.Handler) http.Handler, error) {
	if cfg.AuthMode != config.AuthBearer {
		return nil, nil
	}
	v, err := auth.NewVerifier(ctx, cfg.OIDC)
	if err != nil {
		return nil, err
	}
	return mcpauth.RequireBearerToken(v.Verify, &mcpauth.RequireBearerTokenOptions{
		ResourceMetadataURL: cfg.OIDC.ResourceURI,
	}), nil
}

// runHTTP serves the MCP server over streamable HTTP on ln until the context is
// cancelled, then drains in-flight sessions (bounded) and stops. The same
// *mcp.Server backs every session — the SDK creates a per-connection session, so
// the shared server is just a factory over the one (concurrency-safe) ClickHouse
// connection. Takes a listener so callers (and tests) own the bind, and an
// optional middleware (the auth gate) that wraps the handler when non-nil.
func runHTTP(ctx context.Context, s *mcp.Server, ln net.Listener, mw func(http.Handler) http.Handler) error {
	var handler http.Handler = mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, nil)
	if mw != nil {
		handler = mw(handler)
	}
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
