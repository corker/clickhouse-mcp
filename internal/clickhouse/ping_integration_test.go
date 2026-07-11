//go:build integration

package clickhouse_test

import (
	"context"
	"testing"

	"github.com/corker/clickhouse-mcp/internal/clickhouse/testsupport"
)

// Proves the testcontainers harness works end-to-end: a real ClickHouse boots,
// the project's connection path reaches it, and the query the ping tool issues
// (SELECT 1) round-trips. run_query's integration tests will reuse this harness.
func TestHarness_PingQuery(t *testing.T) {
	conn := testsupport.Start(t)

	var n uint8
	if err := conn.QueryRow(context.Background(), "SELECT 1").Scan(&n); err != nil {
		t.Fatalf("SELECT 1: %v", err)
	}
	if n != 1 {
		t.Fatalf("SELECT 1 = %d, want 1", n)
	}
}
