// Command clickhouse-mcp is a Model Context Protocol server for ClickHouse.
package main

import (
	"context"
	"errors"
	"log"
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
		err = runHTTP(ctx, s, cfg.Server.HTTPAddr)
	default:
		err = s.Run(ctx, &mcp.StdioTransport{})
	}
	if err != nil {
		log.Fatalf("server: %v", err)
	}
}

// runHTTP serves the MCP server over streamable HTTP until the context is
// cancelled, then shuts down gracefully. The same *mcp.Server backs every
// session (one ClickHouse connection, shared).
func runHTTP(ctx context.Context, s *mcp.Server, addr string) error {
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, nil)
	// ReadHeaderTimeout bounds slow-header (Slowloris) clients. Streamable HTTP
	// keeps response bodies open for server-sent events, so no WriteTimeout.
	srv := &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second}

	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()

	log.Printf("serving MCP over HTTP on %s", addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
