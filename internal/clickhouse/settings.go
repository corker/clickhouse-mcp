package clickhouse

import (
	"context"

	"github.com/ClickHouse/clickhouse-go/v2"
)

// Default hard caps applied to every read query as a backstop. These are the
// safety ceiling (throw mode: the query errors rather than streaming an
// enormous result); the tool's LIMIT n+1 is the primary display bound.
const (
	DefaultMaxResultRows  = 100_000
	DefaultMaxResultBytes = 64 << 20 // 64 MiB
	DefaultMaxExecSeconds = 30
)

// ReadOnlyContext returns a context that runs queries under readonly=2 —
// server-enforced no writes and no DDL, while still allowing per-query settings
// (readonly=1 would forbid the caps below).
func ReadOnlyContext(ctx context.Context) context.Context {
	return clickhouse.Context(ctx, clickhouse.WithSettings(clickhouse.Settings{
		"readonly": 2,
	}))
}

// ReadOnlyCappedContext returns a read-only context with hard row, byte, and
// time ceilings. The overflow mode is throw: exceeding a cap fails the query
// (the safety backstop) rather than silently returning a partial result, which
// result_overflow_mode=break was verified to do unreliably.
func ReadOnlyCappedContext(ctx context.Context, maxRows, maxBytes, maxExecSeconds int) context.Context {
	return clickhouse.Context(ctx, clickhouse.WithSettings(clickhouse.Settings{
		"readonly":             2,
		"max_result_rows":      maxRows,
		"max_result_bytes":     maxBytes,
		"result_overflow_mode": "throw",
		"max_execution_time":   maxExecSeconds,
	}))
}

// DefaultReadContext is the read-only context every tool uses: the default caps.
func DefaultReadContext(ctx context.Context) context.Context {
	return ReadOnlyCappedContext(ctx, DefaultMaxResultRows, DefaultMaxResultBytes, DefaultMaxExecSeconds)
}
