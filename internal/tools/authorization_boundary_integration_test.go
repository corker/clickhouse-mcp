//go:build integration

package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/corker/clickhouse-mcp/internal/clickhouse/testsupport"
)

// roUserName derives a ClickHouse-safe, per-test-unique user name. Users are
// container-global (not per-database), so a fixed name would collide across
// parallel or re-run tests on the shared container; qualifying by the test name
// keeps it isolated like Database does for schemas.
func roUserName(t *testing.T) string {
	var b strings.Builder
	b.WriteString("ro_")
	for _, r := range t.Name() {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

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
	roUser, roPass := roUserName(t), "verify"
	if err := admin.Exec(ctx, "DROP USER IF EXISTS "+roUser); err != nil {
		t.Fatalf("drop user: %v", err)
	}
	// The shared container is booted with access management on, so failure here
	// is a real regression, not a reason to skip the security proof.
	if err := admin.Exec(ctx, "CREATE USER "+roUser+" IDENTIFIED BY '"+roPass+"'"); err != nil {
		t.Fatalf("create user (access management should be enabled on the test container): %v", err)
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

	// Every write path is refused — and refused for lack of PRIVILEGE, not some
	// unrelated failure. Distinct target names so each case is proven independently
	// (especially the SETTINGS readonly=0 override, the case that defeated the old
	// guard). ClickHouse reports privilege denials as "Not enough privileges".
	writes := []struct{ name, sql string }{
		{"insert", "INSERT INTO " + db + ".t VALUES (99)"},
		{"create", "CREATE TABLE " + db + ".pwn_plain (x UInt8) ENGINE=Memory"},
		{"settings-override", "CREATE TABLE " + db + ".pwn_override (x UInt8) ENGINE=Memory SETTINGS readonly=0"},
		{"drop", "DROP TABLE " + db + ".t"},
	}
	for _, w := range writes {
		t.Run(w.name, func(t *testing.T) {
			_, _, err := runStatement(ctx, ro, runStatementArgs{SQL: w.sql})
			if err == nil {
				t.Fatalf("read-only user must be refused: %q", w.sql)
			}
			if !strings.Contains(err.Error(), "Not enough privileges") {
				t.Errorf("want a privilege denial, got a different error: %v", err)
			}
		})
	}

	// Neither create landed, and the data is intact — each override target checked
	// independently so a leak of the SETTINGS case cannot hide behind the other.
	for _, name := range []string{"pwn_plain", "pwn_override"} {
		var n uint64
		if err := admin.QueryRow(ctx, "SELECT count() FROM system.tables WHERE database=? AND name=?", db, name).Scan(&n); err != nil || n != 0 {
			t.Errorf("%s must not exist: count=%d err=%v", name, n, err)
		}
	}
	if _, res, _ := runQuery(ctx, admin, runQueryArgs{SQL: "SELECT count() FROM " + db + ".t"}); res.Rows[0][0] != "3" {
		t.Errorf("row count must still be 3, got %v", res.Rows[0][0])
	}
}
