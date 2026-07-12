// Command clickhouse-mcp is a Model Context Protocol server for ClickHouse.
package main

import (
	"context"
	"errors"
	"fmt"
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

// authMiddleware builds the HTTP auth gate for the configured mode (nil = no
// gate). The verifier is built here, at startup, so a discovery failure aborts
// the process rather than surfacing per-request.
func authMiddleware(ctx context.Context, cfg config.ServerConfig) (func(http.Handler) http.Handler, error) {
	switch cfg.AuthMode {
	case config.AuthOff:
		return nil, nil
	case config.AuthBearer:
		v, err := auth.NewVerifier(ctx, cfg.OIDC)
		if err != nil {
			return nil, err
		}
		return mcpauth.RequireBearerToken(v.Verify, &mcpauth.RequireBearerTokenOptions{
			ResourceMetadataURL: cfg.OIDC.ResourceURI,
		}), nil
	default:
		// A mode config accepts but that isn't wired here (e.g. broker) must fail
		// closed — never fall through to an unauthenticated server.
		return nil, fmt.Errorf("auth mode %q has no HTTP gate wired", cfg.AuthMode)
	}
}

// runHTTP serves over ln until ctx is cancelled, then drains in-flight sessions
// (bounded) before stopping.
//
// One shared *mcp.Server backs every session — the SDK gives each connection its
// own session, so the server is just a factory over the one (concurrency-safe)
// ClickHouse connection. The listener is injected so callers (and tests) own the
// bind; mw, when non-nil, wraps the handler (the auth gate).
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
