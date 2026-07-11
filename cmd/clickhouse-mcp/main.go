package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

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
	defer conn.Close()

	s := server.New("clickhouse-mcp", conn)
	if err := s.Run(ctx, &mcp.StdioTransport{}); err != nil {
		log.Fatalf("server: %v", err)
	}
}
