//go:build integration

package tools

import (
	"context"
	"testing"

	"github.com/corker/clickhouse-mcp/internal/clickhouse/testsupport"
)

// TestAuthorizationBoundary_RBAC is the proof for ADR-0006: authorization is the
// connected ClickHouse user's privileges, not any server-side guard. A user
// granted only SELECT is refused every write — including the SETTINGS readonly=0
// override that defeated the old readonly=2 guard — while reads still work.
func TestAuthorizationBoundary_RBAC(t *testing.T) {
	admin, db := testsupport.Database(t)
	ctx := context.Background()

	// Seed a table the read-only user may select from.
	if err := admin.Exec(ctx, "CREATE TABLE "+db+".t (x UInt64) ENGINE=Memory"); err != nil {
		t.Fatalf("seed table: %v", err)
	}
	if err := admin.Exec(ctx, "INSERT INTO "+db+".t VALUES (1),(2),(3)"); err != nil {
		t.Fatalf("seed rows: %v", err)
	}

	// A SELECT-only user (no write privilege at all).
	const roUser, roPass = "boundary_ro", "verify"
	if err := admin.Exec(ctx, "DROP USER IF EXISTS "+roUser); err != nil {
		t.Fatalf("drop user: %v", err)
	}
	if err := admin.Exec(ctx, "CREATE USER "+roUser+" IDENTIFIED BY '"+roPass+"'"); err != nil {
		t.Skipf("cannot create users (access_management off?): %v", err)
	}
	if err := admin.Exec(ctx, "GRANT SELECT ON *.* TO "+roUser); err != nil {
		t.Fatalf("grant select: %v", err)
	}
	t.Cleanup(func() { _ = admin.Exec(context.Background(), "DROP USER IF EXISTS "+roUser) })

	ro := testsupport.ConnectAs(t, roUser, roPass)

	// SELECT works through run_query.
	if _, res, err := runQuery(ctx, ro, runQueryArgs{SQL: "SELECT count() FROM " + db + ".t"}); err != nil || res.Rows[0][0] != "3" {
		t.Fatalf("read-only user should be able to SELECT: rows=%v err=%v", res.Rows, err)
	}

	// Every write path is refused by ClickHouse, including the SETTINGS override.
	writes := []string{
		"INSERT INTO " + db + ".t VALUES (99)",
		"CREATE TABLE " + db + ".pwn (x UInt8) ENGINE=Memory",
		"CREATE TABLE " + db + ".pwn (x UInt8) ENGINE=Memory SETTINGS readonly=0",
		"DROP TABLE " + db + ".t",
	}
	for _, sql := range writes {
		if _, _, err := runStatement(ctx, ro, runStatementArgs{SQL: sql}); err == nil {
			t.Errorf("read-only user must be refused: %q", sql)
		}
	}

	// The data is intact and pwn never got created.
	var n uint64
	if err := admin.QueryRow(ctx, "SELECT count() FROM system.tables WHERE database=? AND name='pwn'", db).Scan(&n); err != nil || n != 0 {
		t.Errorf("no write should have landed: pwn count=%d err=%v", n, err)
	}
	if _, res, _ := runQuery(ctx, admin, runQueryArgs{SQL: "SELECT count() FROM " + db + ".t"}); res.Rows[0][0] != "3" {
		t.Errorf("row count must still be 3, got %v", res.Rows[0][0])
	}
}
