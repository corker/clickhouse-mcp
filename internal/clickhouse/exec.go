package clickhouse

import (
	"context"
	"sync/atomic"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/ClickHouse/clickhouse-go/v2/lib/proto"
)

// ExecWritten runs a non-row-returning statement and reports the rows the server
// says it wrote. It keeps the driver's progress-protocol detail (proto.Progress,
// WithProgress) behind this package, the same seam the read path uses via
// CappedContext — callers stay off clickhouse-go directly.
//
// rowsWritten is the sum of per-packet WroteRows deltas. The server only sends
// write progress from a recent-enough revision (DBMS_MIN_REVISION_WITH_CLIENT_
// WRITE_INFO); against an older server it stays 0 even when rows were written, so
// treat it as best-effort, not a guarantee. DDL writes no rows and reports 0.
func ExecWritten(ctx context.Context, conn driver.Conn, sql string) (rowsWritten uint64, err error) {
	// atomic because on ctx-cancel the progress goroutine can outlive Exec's
	// return and keep adding; a plain field would make that a data race. On the
	// success path all callbacks have completed before Exec returns.
	var wrote atomic.Uint64
	pctx := clickhouse.Context(ctx, clickhouse.WithProgress(func(p *proto.Progress) {
		wrote.Add(p.WroteRows)
	}))
	if err := conn.Exec(pctx, sql); err != nil {
		// Drop any partial count on error: a failed statement must not report an
		// honest-looking nonzero rows-written.
		return 0, err
	}
	return wrote.Load(), nil
}
