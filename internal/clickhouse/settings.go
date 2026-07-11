package clickhouse

import (
	"context"

	"github.com/ClickHouse/clickhouse-go/v2"
)

// Default hard caps applied to every row-returning query as a backstop. These
// are the safety ceiling (throw mode: the query errors rather than streaming an
// enormous result); the tool's LIMIT n+1 is the primary display bound. They are
// ergonomic guardrails, not an authorization control — that is the connected
// ClickHouse user's privileges (ADR-0006).
const (
	DefaultMaxResultRows  = 100_000
	DefaultMaxResultBytes = 64 << 20 // 64 MiB
	DefaultMaxExecSeconds = 30
)

// CappedContext returns a context with hard row, byte, and time ceilings. The
// overflow mode is throw: exceeding a cap fails the query (the safety backstop)
// rather than silently returning a partial result, which result_overflow_mode=
// break was verified to do unreliably. Verified to apply without any readonly
// setting.
func CappedContext(ctx context.Context, maxRows, maxBytes, maxExecSeconds int) context.Context {
	return clickhouse.Context(ctx, clickhouse.WithSettings(clickhouse.Settings{
		"max_result_rows":      maxRows,
		"max_result_bytes":     maxBytes,
		"result_overflow_mode": "throw",
		"max_execution_time":   maxExecSeconds,
	}))
}

// DefaultReadContext is the capped context row-returning tools use for the
// default ceilings.
func DefaultReadContext(ctx context.Context) context.Context {
	return CappedContext(ctx, DefaultMaxResultRows, DefaultMaxResultBytes, DefaultMaxExecSeconds)
}
