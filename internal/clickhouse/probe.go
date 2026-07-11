package clickhouse

import (
	"context"
	"fmt"
	"strings"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

const probeTable = "__clickhouse_mcp_write_probe__"

// WriteProbe verifies that the read-only guard actually holds against the live
// connection, across any ClickHouse setup. It attempts a real write (INSERT)
// under readonly=2 and reports whether the server refused it.
//
// The probe MUST use INSERT (or persistent DDL), never CREATE TEMPORARY TABLE:
// readonly=2 exempts temporary tables, so a temp-table probe would always
// appear to succeed and wrongly report the guard as broken.
//
// Returns guardHolds=true when the write was refused (the safe case). A false
// result means writes got through and the caller should fail closed. A non-nil
// error means the probe itself could not run (e.g. cannot create the probe
// table); the caller should also treat that as "do not serve run_query".
func WriteProbe(ctx context.Context, conn driver.Conn) (guardHolds bool, err error) {
	// Create the probe table outside readonly so the INSERT has a target.
	create := fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (x UInt8) ENGINE=Memory", probeTable)
	if err := conn.Exec(ctx, create); err != nil {
		return false, fmt.Errorf("write-probe setup: %w", err)
	}
	// Drop it afterward so it does not clutter list_tables. Best-effort, and via
	// the unrestricted context since DROP is refused under readonly.
	defer func() { _ = conn.Exec(ctx, fmt.Sprintf("DROP TABLE IF EXISTS %s", probeTable)) }()

	insertErr := conn.Exec(ReadOnlyContext(ctx), fmt.Sprintf("INSERT INTO %s VALUES (1)", probeTable))
	switch {
	case insertErr == nil:
		// The write went through under readonly=2 — the guard is NOT holding.
		return false, nil
	case strings.Contains(insertErr.Error(), "readonly"):
		return true, nil
	default:
		// Refused for some other reason (e.g. permissions) — still refused, so
		// the guard effectively holds for this connection.
		return true, nil
	}
}
