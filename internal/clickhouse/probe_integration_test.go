//go:build integration

package clickhouse_test

import (
	"context"
	"testing"

	chdriver "github.com/corker/clickhouse-mcp/internal/clickhouse"
	"github.com/corker/clickhouse-mcp/internal/clickhouse/testsupport"
)

// The write-probe must report the guard as holding against a normal connection
// (readonly=2 refuses INSERT), and readonly=2 must actually block a write.
func TestWriteProbe_GuardHolds(t *testing.T) {
	conn := testsupport.Start(t)
	ctx := context.Background()

	guardHolds, err := chdriver.WriteProbe(ctx, conn)
	if err != nil {
		t.Fatalf("write-probe errored: %v", err)
	}
	if !guardHolds {
		t.Fatal("write-probe reports guard does NOT hold, but readonly=2 should refuse writes")
	}
}

// readonly=2 blocks an INSERT (the security boundary) but the ReadOnlyCapped
// context still allows the SELECT + caps (readonly=1 would forbid the settings).
func TestReadOnlyContext_BlocksWrite_AllowsRead(t *testing.T) {
	conn := testsupport.Start(t)
	ctx := context.Background()

	if err := conn.Exec(ctx, "CREATE TABLE t (x UInt8) ENGINE=Memory"); err != nil {
		t.Fatalf("setup table: %v", err)
	}

	roCtx := chdriver.ReadOnlyContext(ctx)
	if err := conn.Exec(roCtx, "INSERT INTO t VALUES (1)"); err == nil {
		t.Fatal("INSERT under readonly=2 should be refused")
	}

	capped := chdriver.ReadOnlyCappedContext(ctx, 1000, 1<<20, 10)
	var n uint8
	if err := conn.QueryRow(capped, "SELECT 1").Scan(&n); err != nil {
		t.Fatalf("SELECT under readonly=2 + caps should work: %v", err)
	}
}
