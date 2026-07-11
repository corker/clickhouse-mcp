package tools

import "fmt"

// resolveTableLimit picks the row limit for a table listing. A caller-supplied
// positive limit wins; otherwise the default is tiered — folding every table's
// columns (include_columns) uses a much tighter default than a lean listing,
// since each folded table costs many columns.
func resolveTableLimit(argLimit int, folded bool) int {
	if argLimit > 0 {
		return argLimit
	}
	if folded {
		return DefaultFoldedTableLimit
	}
	return DefaultTableLimit
}

// resolveLimit returns argLimit when positive, else def — the flat (non-tiered)
// counterpart to resolveTableLimit.
func resolveLimit(argLimit, def int) int {
	if argLimit > 0 {
		return argLimit
	}
	return def
}

// truncate caps items to limit, reporting whether more existed. items is expected
// to hold up to limit+1 (the sentinel used to detect overflow). The note is
// non-empty only when truncated, and mentions the count so the caller can act.
//
// A non-positive limit is treated as "no cap" (return items unchanged): callers
// pass resolved defaults (always positive), so 0/negative only reaches here by
// mistake, and returning everything is safer than slicing to an empty list with
// a nonsensical "showing 0 ... more exist" note (or panicking on a negative bound).
func truncate[T any](items []T, limit int, noun string) (kept []T, truncated bool, note string) {
	if limit <= 0 || len(items) <= limit {
		return items, false, ""
	}
	return items[:limit], true, fmt.Sprintf(
		"showing %d %s; more exist. Pass a larger limit to see the rest.", limit, noun)
}
