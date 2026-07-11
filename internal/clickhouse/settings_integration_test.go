//go:build integration

package clickhouse_test

import (
	"context"
	"errors"
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
	// Match code 396 specifically so an unrelated failure can't pass as the cap.
	if !strings.Contains(err.Error(), "code: 396") {
		t.Errorf("want the result-cap error (code: 396), got: %v", err)
	}
}

// ExecWritten must fail cleanly (no panic, no hang) when its context is already
// cancelled — the path the atomic/progress-goroutine comment guards. Targets a
// real writable table so the failure can only be the cancellation, not an
// inability to write; asserts nothing landed.
func TestExecWritten_CancelledContext(t *testing.T) {
	conn, db := testsupport.Database(t)
	if err := conn.Exec(context.Background(), "CREATE TABLE "+db+".t (x UInt64) ENGINE=Memory"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled before the call

	n, err := chdriver.ExecWritten(ctx, conn, "INSERT INTO "+db+".t SELECT number FROM system.numbers LIMIT 1000000")
	if err == nil {
		t.Fatal("a cancelled context must produce an error, not a successful write")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("want a context.Canceled error, got: %v", err)
	}
	if n != 0 {
		t.Errorf("no rows should be reported on a cancelled exec, got %d", n)
	}
	// The write must not have landed.
	var got uint64
	if err := conn.QueryRow(context.Background(), "SELECT count() FROM "+db+".t").Scan(&got); err != nil || got != 0 {
		t.Errorf("cancelled insert must not write rows: count=%d err=%v", got, err)
	}
}
