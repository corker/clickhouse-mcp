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

// The probe must clean up its table so it does not clutter list_tables.
func TestWriteProbe_LeavesNoTable(t *testing.T) {
	conn := testsupport.Start(t)
	ctx := context.Background()

	if _, err := chdriver.WriteProbe(ctx, conn); err != nil {
		t.Fatalf("write-probe: %v", err)
	}
	var count uint64
	err := conn.QueryRow(ctx,
		"SELECT count() FROM system.tables WHERE name = '__clickhouse_mcp_write_probe__'").Scan(&count)
	if err != nil {
		t.Fatalf("count probe table: %v", err)
	}
	if count != 0 {
		t.Errorf("probe table should be dropped after probing, found %d", count)
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
