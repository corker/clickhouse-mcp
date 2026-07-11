//go:build integration

// Package testsupport starts a real ClickHouse in a container for integration tests.
package testsupport

import (
	"context"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/testcontainers/testcontainers-go"
	tcclickhouse "github.com/testcontainers/testcontainers-go/modules/clickhouse"

	"github.com/corker/clickhouse-mcp/internal/clickhouse"
	"github.com/corker/clickhouse-mcp/internal/config"
)

// Pinned so integration runs are reproducible and don't drift with :latest.
const image = "clickhouse/clickhouse-server:25.6"

// Start boots a ClickHouse container and returns a live connection built through
// the project's own clickhouse.New (so tests exercise the real connection path).
// The container and connection are torn down via t.Cleanup.
func Start(t *testing.T) driver.Conn {
	t.Helper()
	ctx := context.Background()

	// The module defaults CLICKHOUSE_PASSWORD to a non-empty value and requires
	// auth, so set explicit credentials and use them for the connection.
	const user, pass, db = "default", "test", "default"
	ctr, err := tcclickhouse.Run(ctx, image,
		tcclickhouse.WithUsername(user),
		tcclickhouse.WithPassword(pass),
		tcclickhouse.WithDatabase(db),
	)
	if err != nil {
		t.Fatalf("start clickhouse container: %v", err)
	}
	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(ctr); err != nil {
			t.Logf("terminate container: %v", err)
		}
	})

	host, err := ctr.Host(ctx)
	if err != nil {
		t.Fatalf("container host: %v", err)
	}
	port, err := ctr.MappedPort(ctx, "9000/tcp")
	if err != nil {
		t.Fatalf("container port: %v", err)
	}

	cfg := &config.Config{
		Host:     host,
		Port:     int(port.Num()),
		User:     user,
		Password: pass,
		Database: db,
	}

	connCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	conn, err := clickhouse.New(connCtx, cfg)
	if err != nil {
		t.Fatalf("connect to container: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}
