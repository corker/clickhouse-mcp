//go:build integration

// Package testsupport starts a real ClickHouse in a container for integration
// tests. One container is booted per test package (via sync.Once) and shared;
// each test gets its own isolated database via Database.
package testsupport

import (
	"context"
	"fmt"
	"hash/fnv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	tcclickhouse "github.com/testcontainers/testcontainers-go/modules/clickhouse"

	"github.com/corker/clickhouse-mcp/internal/clickhouse"
	"github.com/corker/clickhouse-mcp/internal/config"
)

// Pinned so integration runs are reproducible and don't drift with :latest.
const image = "clickhouse/clickhouse-server:25.6"

var (
	once       sync.Once
	sharedConn driver.Conn
	sharedErr  error
)

// Start does not isolate: fixtures created here land in the shared default
// database and leak across tests — use Database for an isolated one.
func Start(t *testing.T) driver.Conn {
	t.Helper()
	once.Do(func() { sharedConn, sharedErr = boot() })
	if sharedErr != nil {
		t.Fatalf("start shared clickhouse: %v", sharedErr)
	}
	return sharedConn
}

// Database returns a per-test database (dropped on cleanup) whose name qualifies
// fixtures so tests never collide on the shared container.
func Database(t *testing.T) (conn driver.Conn, database string) {
	t.Helper()
	conn = Start(t)
	database = sanitize(t.Name())
	ctx := context.Background()
	if err := conn.Exec(ctx, "DROP DATABASE IF EXISTS "+database); err != nil {
		t.Fatalf("reset database %s: %v", database, err)
	}
	if err := conn.Exec(ctx, "CREATE DATABASE "+database); err != nil {
		t.Fatalf("create database %s: %v", database, err)
	}
	t.Cleanup(func() { _ = conn.Exec(context.Background(), "DROP DATABASE IF EXISTS "+database) })
	return conn, database
}

// sanitize turns a test name into a valid, unique ClickHouse identifier. The
// readable part maps non-alphanumerics to "_"; a hash of the original name is
// appended so distinct names that would otherwise collapse to the same string
// (e.g. "TestA/b" and "TestA_b") never collide on one database.
func sanitize(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	return fmt.Sprintf("test_%s_%08x", b.String(), h.Sum32())
}

func boot() (driver.Conn, error) {
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
		return nil, fmt.Errorf("run container: %w", err)
	}

	host, err := ctr.Host(ctx)
	if err != nil {
		return nil, fmt.Errorf("container host: %w", err)
	}
	port, err := ctr.MappedPort(ctx, "9000/tcp")
	if err != nil {
		return nil, fmt.Errorf("container port: %w", err)
	}

	cfg := &config.Config{Host: host, Port: int(port.Num()), User: user, Password: pass, Database: db}
	connCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	conn, err := clickhouse.New(connCtx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	return conn, nil
}
