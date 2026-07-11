package query

import (
	"strconv"
	"strings"
)

// CanBound reports whether a statement can be wrapped in SELECT * FROM (...)
// LIMIT n+1 for row bounding. SELECT/WITH can; SHOW/DESCRIBE/EXPLAIN/EXISTS and
// everything else cannot be wrapped and run as-is under the cap ceiling. This is
// a bounding decision, not an authorization one — ClickHouse's per-user
// privileges are the boundary (ADR-0006).
func CanBound(sql string) bool {
	switch leadingKeyword(sql) {
	case "SELECT", "WITH":
		return true
	default:
		return false
	}
}

// leadingKeyword returns the upper-cased first token, skipping leading
// whitespace and a leading line/block comment.
func leadingKeyword(sql string) string {
	s := strings.TrimSpace(sql)
	for {
		switch {
		case strings.HasPrefix(s, "--"):
			if i := strings.IndexByte(s, '\n'); i >= 0 {
				s = strings.TrimSpace(s[i+1:])
				continue
			}
			return ""
		case strings.HasPrefix(s, "/*"):
			if i := strings.Index(s, "*/"); i >= 0 {
				s = strings.TrimSpace(s[i+2:])
				continue
			}
			return ""
		}
		break
	}
	i := strings.IndexFunc(s, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '('
	})
	if i < 0 {
		i = len(s)
	}
	return strings.ToUpper(s[:i])
}

// Bound wraps a boundable statement to cap the row count; others pass through
// (the throw-mode cap is their backstop).
//
// The newline before the closing paren is load-bearing: if the inner query ends
// in a trailing "-- comment", putting ") LIMIT n" on its own line stops the
// comment from swallowing it (which would leave an unmatched paren).
func Bound(sql string, canBound bool, fetchLimit int) string {
	if !canBound {
		return sql
	}
	inner := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(sql), "; "))
	return "SELECT * FROM (" + inner + "\n) LIMIT " + strconv.Itoa(fetchLimit)
}

// Truncation is embedded by every list-shaped tool's output so they share one
// count/truncated/limit/note contract.
type Truncation struct {
	Count     int    `json:"count" jsonschema:"number of items returned"`
	Truncated bool   `json:"truncated" jsonschema:"true if more items existed beyond the limit"`
	Limit     int    `json:"limit" jsonschema:"the applied limit"`
	Note      string `json:"note,omitempty" jsonschema:"guidance when truncated"`
}

type Result struct {
	Columns     []string `json:"columns" jsonschema:"column names, aligned to each row"`
	ColumnTypes []string `json:"column_types" jsonschema:"ClickHouse type of each column, aligned to columns; tells the caller which string values are stringified numerics (e.g. Decimal, UInt64) versus real strings"`
	Rows        [][]any  `json:"rows" jsonschema:"result rows as positional arrays aligned to columns; large integers and decimals are strings to avoid precision loss"`
	Truncation           // embedded: its fields flatten into the JSON object
}

// Shape drops the sentinel (displayLimit+1) row and reports truncation. When a
// truncated result was not ordered, the subset is arbitrary and the note says so.
func Shape(columns, columnTypes []string, fetched [][]any, displayLimit int, ordered bool) Result {
	truncated := len(fetched) > displayLimit
	rows := fetched
	if truncated {
		rows = fetched[:displayLimit]
	}
	r := Result{
		Columns:     columns,
		ColumnTypes: columnTypes,
		Rows:        rows,
		Truncation: Truncation{
			Count:     len(rows),
			Truncated: truncated,
			Limit:     displayLimit,
		},
	}
	switch {
	case truncated && !ordered:
		r.Note = "showing an arbitrary subset; more rows exist. Add ORDER BY + LIMIT or a WHERE filter for a defined result."
	case truncated:
		r.Note = "showing the first rows; more exist. Add LIMIT to control the count."
	}
	return r
}

// HasTopLevelOrderBy is a cheap substring check (not a SQL parse), used only to
// phrase the truncation note.
func HasTopLevelOrderBy(sql string) bool {
	return strings.Contains(strings.ToUpper(sql), "ORDER BY")
}
