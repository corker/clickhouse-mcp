//go:build integration

package clickhouse_test

import (
	"context"
	"strings"
	"testing"

	chdriver "github.com/corker/clickhouse-mcp/internal/clickhouse"
	"github.com/corker/clickhouse-mcp/internal/clickhouse/testsupport"
)

// CappedContext exists to make the server THROW when a result cap is exceeded
// (result_overflow_mode=throw), not silently return a partial result — the
// behavior ADR-0004/settings.go rely on. Prove the throw actually fires.
func TestCappedContext_ThrowsWhenRowCapExceeded(t *testing.T) {
	conn := testsupport.Start(t)
	// A tiny row cap over an unbounded source must error, not truncate silently.
	capped := chdriver.CappedContext(context.Background(), 10, 1<<30, 30)
	rows, err := conn.Query(capped, "SELECT number FROM system.numbers")
	if err == nil {
		// Some drivers surface the cap on iteration; drain to reach it.
		for rows.Next() {
		}
		err = rows.Err()
		_ = rows.Close()
	}
	if err == nil {
		t.Fatal("exceeding max_result_rows must error (throw mode), not return a partial result")
	}
	if !strings.Contains(err.Error(), "Limit for result exceeded") && !strings.Contains(err.Error(), "396") {
		t.Errorf("want a result-cap (code 396) error, got: %v", err)
	}
}

// ExecWritten must fail cleanly (no panic, no hang) when its context is already
// cancelled — the path the atomic/progress-goroutine comment guards.
func TestExecWritten_CancelledContext(t *testing.T) {
	conn := testsupport.Start(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled before the call

	n, err := chdriver.ExecWritten(ctx, conn, "INSERT INTO system.numbers SELECT number FROM system.numbers LIMIT 1000000")
	if err == nil {
		t.Error("a cancelled context must produce an error")
	}
	if n != 0 {
		t.Errorf("no rows should be reported on a cancelled exec, got %d", n)
	}
}
