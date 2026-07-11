package tools

import (
	"fmt"

	"github.com/corker/clickhouse-mcp/internal/query"
)

// resolveTableLimit tiers the default: folding every table's columns
// (include_columns) uses a much tighter default than a lean listing, since each
// folded table costs many columns. A caller's positive limit always wins.
func resolveTableLimit(argLimit int, folded bool) int {
	if argLimit > 0 {
		return argLimit
	}
	if folded {
		return DefaultFoldedTableLimit
	}
	return DefaultTableLimit
}

func resolveLimit(argLimit, def int) int {
	if argLimit > 0 {
		return argLimit
	}
	return def
}

// truncate caps items to limit. items is expected to hold up to limit+1 (the
// sentinel used to detect overflow).
//
// A non-positive limit is treated as "no cap" (return items unchanged): callers
// pass resolved defaults (always positive), so 0/negative only reaches here by
// mistake, and returning everything is safer than slicing to an empty list with
// a nonsensical "showing 0 ... more exist" note (or panicking on a negative bound).
func truncate[T any](items []T, limit int, noun string) (kept []T, tr query.Truncation) {
	if limit <= 0 || len(items) <= limit {
		return items, query.Truncation{Count: len(items), Limit: limit}
	}
	kept = items[:limit]
	return kept, query.Truncation{
		Count:     len(kept),
		Truncated: true,
		Limit:     limit,
		Note:      fmt.Sprintf("showing %d %s; more exist. Pass a larger limit to see the rest.", limit, noun),
	}
}
