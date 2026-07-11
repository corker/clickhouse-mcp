package clickhouse

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// ProbeTable is the table WriteProbe creates and drops. Exported so the
// inspection tools can filter it out defensively if a drop ever fails.
const ProbeTable = "__clickhouse_mcp_write_probe__"

// WriteProbe verifies that the read-only guard actually holds against the live
// connection, across any ClickHouse setup. It attempts a real write (INSERT)
// under readonly=2 in the given database and reports whether the server refused
// it. database is trusted (from config, not user input); pass the configured
// database in production, or an isolated one in tests.
//
// The probe MUST use INSERT (or persistent DDL), never CREATE TEMPORARY TABLE:
// readonly=2 exempts temporary tables, so a temp-table probe would always
// appear to succeed and wrongly report the guard as broken.
//
// Returns guardHolds=true when the write was refused (the safe case). A false
// result means writes got through and the caller should fail closed. A non-nil
// error means the probe itself could not run (e.g. cannot create the probe
// table); the caller should also treat that as "do not serve run_query".
func WriteProbe(ctx context.Context, conn driver.Conn, database string) (guardHolds bool, err error) {
	// Backtick-quote the database identifier (from config, not user input) so a
	// name with spaces/dots is parsed as one identifier rather than mangling the
	// DDL; embedded backticks are doubled per ClickHouse's escaping.
	table := fmt.Sprintf("`%s`.%s", strings.ReplaceAll(database, "`", "``"), ProbeTable)

	// Create the probe table outside readonly so the INSERT has a target.
	if err := conn.Exec(ctx, fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (x UInt8) ENGINE=Memory", table)); err != nil {
		return false, fmt.Errorf("write-probe setup: %w", err)
	}
	// Drop it afterward so it does not clutter list_tables. Best-effort, via the
	// unrestricted context (DROP is refused under readonly). list_tables also
	// filters ProbeTable, so a failed drop here is cosmetic, but log it.
	defer func() {
		if e := conn.Exec(ctx, fmt.Sprintf("DROP TABLE IF EXISTS %s", table)); e != nil {
			log.Printf("write-probe: failed to drop %s: %v", table, e)
		}
	}()

	insertErr := conn.Exec(ReadOnlyContext(ctx), fmt.Sprintf("INSERT INTO %s VALUES (1)", table))
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
