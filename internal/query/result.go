package query

import (
	"strconv"
	"strings"
)

// StmtClass is how run_query dispatches row bounding, decided by the leading
// keyword of a statement.
type StmtClass int

const (
	// ClassRejected is a statement not on the read-only allowlist.
	ClassRejected StmtClass = iota
	// ClassSelect is SELECT/WITH — bounded by wrapping in SELECT * FROM (...) LIMIT n+1.
	ClassSelect
	// ClassSmall is SHOW/DESCRIBE/EXPLAIN/EXISTS — inherently small, run as-is.
	ClassSmall
)

// Classify returns the statement class from the leading keyword. It is a light
// UX gate that produces a clear "read-only only" rejection; it is NOT the
// security boundary (readonly=2 is). Verified: SELECT/WITH accept the subquery
// wrap; SHOW/DESCRIBE/EXPLAIN/EXISTS do not and need no bounding.
func Classify(sql string) StmtClass {
	head := leadingKeyword(sql)
	switch head {
	case "SELECT", "WITH":
		return ClassSelect
	case "SHOW", "DESCRIBE", "DESC", "EXPLAIN", "EXISTS":
		return ClassSmall
	default:
		return ClassRejected
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

// Bound produces the SQL to execute so that at most fetchLimit (= displayLimit+1)
// rows come back, per the verified per-class rule. For SELECT/WITH it strips a
// trailing semicolon and wraps as SELECT * FROM (<sql>) LIMIT fetchLimit. For
// small statements it returns them unchanged (the throw-mode cap is the backstop).
func Bound(sql string, class StmtClass, fetchLimit int) string {
	switch class {
	case ClassSelect:
		inner := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(sql), "; "))
		return "SELECT * FROM (" + inner + ") LIMIT " + strconv.Itoa(fetchLimit)
	default:
		return sql
	}
}

// Result holds the shaped output of a query: columns once, positional rows, and
// machine-readable truncation signals for the calling agent.
type Result struct {
	Columns   []string `json:"columns" jsonschema:"column names, aligned to each row"`
	Rows      [][]any  `json:"rows" jsonschema:"result rows as positional arrays aligned to columns; large integers and decimals are strings to avoid precision loss"`
	RowCount  int      `json:"row_count" jsonschema:"number of rows returned"`
	Truncated bool     `json:"truncated" jsonschema:"true if more rows existed beyond the limit"`
	Limit     int      `json:"limit" jsonschema:"the applied row display limit"`
	Note      string   `json:"note,omitempty" jsonschema:"guidance when truncated or when rows are an arbitrary subset"`
}

// Shape turns the fetched rows (which may contain up to displayLimit+1) into a
// Result, dropping the sentinel row and reporting truncation. ordered indicates
// whether the query had a top-level ORDER BY; if not and the result was
// truncated, the subset is arbitrary and the note says so.
func Shape(columns []string, fetched [][]any, displayLimit int, ordered bool) Result {
	truncated := len(fetched) > displayLimit
	rows := fetched
	if truncated {
		rows = fetched[:displayLimit]
	}
	r := Result{
		Columns:   columns,
		Rows:      rows,
		RowCount:  len(rows),
		Truncated: truncated,
		Limit:     displayLimit,
	}
	switch {
	case truncated && !ordered:
		r.Note = "showing an arbitrary subset; more rows exist. Add ORDER BY + LIMIT or a WHERE filter for a defined result."
	case truncated:
		r.Note = "showing the first rows; more exist. Add LIMIT to control the count."
	}
	return r
}

// HasTopLevelOrderBy is a cheap check (not a SQL parse) for whether the outer
// query orders its results, used only to phrase the truncation note.
func HasTopLevelOrderBy(sql string) bool {
	return strings.Contains(strings.ToUpper(sql), "ORDER BY")
}
