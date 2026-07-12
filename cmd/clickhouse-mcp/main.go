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
		handler, hErr := buildHTTPHandler(ctx, s, cfg.Server)
		if hErr != nil {
			log.Fatalf("auth: %v", hErr)
		}
		ln, lnErr := net.Listen("tcp", cfg.Server.HTTPAddr)
		if lnErr != nil {
			log.Fatalf("http listen on %s: %v", cfg.Server.HTTPAddr, lnErr)
		}
		log.Printf("serving MCP over HTTP on %s (auth: %s)", ln.Addr(), cfg.Server.AuthMode)
		err = runHTTP(ctx, ln, handler)
	default:
		err = s.Run(ctx, &mcp.StdioTransport{})
	}
	if err != nil {
		log.Fatalf("server: %v", err)
	}
}

// buildHTTPHandler assembles the full HTTP handler for the configured auth mode:
// the MCP endpoint (bearer-gated when auth is on) plus, in broker mode, the OAuth
// broker's public endpoints. The verifier/broker are built here, at startup, so a
// misconfiguration or discovery failure aborts the process rather than surfacing
// per-request. A mode config accepts but that isn't wired must fail closed.
func buildHTTPHandler(ctx context.Context, s *mcp.Server, cfg config.ServerConfig) (http.Handler, error) {
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, nil)

	switch cfg.AuthMode {
	case config.AuthOff:
		return mcpHandler, nil

	case config.AuthBearer:
		gate, err := bearerGate(ctx, cfg.OIDC)
		if err != nil {
			return nil, err
		}
		return gate(mcpHandler), nil

	case config.AuthBroker:
		gate, err := bearerGate(ctx, cfg.OIDC)
		if err != nil {
			return nil, err
		}
		proxy, err := auth.NewProxy(auth.ProxyConfig{
			Broker: auth.BrokerConfig{
				PublicURL: cfg.Broker.PublicURL,
				ClientID:  cfg.Broker.ClientID,
				Scopes:    cfg.Broker.Scopes,
			},
			ClientSecret:         cfg.Broker.ClientSecret,
			UpstreamAuthURL:      cfg.Broker.UpstreamAuthURL,
			UpstreamTokenURL:     cfg.Broker.UpstreamTokenURL,
			AllowedRedirectHosts: cfg.Broker.AllowedRedirectHosts,
		})
		if err != nil {
			return nil, err
		}
		mux := http.NewServeMux()
		proxy.RegisterRoutes(mux) // broker OAuth endpoints, unauthenticated (public discovery + flow)
		mux.Handle("/", gate(mcpHandler))
		return mux, nil

	default:
		return nil, fmt.Errorf("auth mode %q has no HTTP handler wired", cfg.AuthMode)
	}
}

// bearerGate builds the RequireBearerToken middleware backed by our OIDC verifier.
func bearerGate(ctx context.Context, oidc config.OIDCConfig) (func(http.Handler) http.Handler, error) {
	v, err := auth.NewVerifier(ctx, oidc)
	if err != nil {
		return nil, err
	}
	return mcpauth.RequireBearerToken(v.Verify, &mcpauth.RequireBearerTokenOptions{
		ResourceMetadataURL: oidc.ResourceURI,
	}), nil
}

// runHTTP serves handler over ln until ctx is cancelled, then drains in-flight
// sessions (bounded) before stopping. The listener is injected so callers (and
// tests) own the bind.
func runHTTP(ctx context.Context, ln net.Listener, handler http.Handler) error {
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
