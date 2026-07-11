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

// Classify is a light UX gate, NOT the security boundary (readonly=2 is).
// Verified: SELECT/WITH accept the subquery wrap; the ClassSmall statements do
// not and need no bounding.
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

// HasUnsupportedOutputClause reports whether the query ends with a FORMAT or
// INTO OUTFILE clause, which redirect output away from the structured rows this
// tool returns. Caught early for a clear message instead of a confusing wrapped-
// subquery syntax error.
//
// Both are terminal clauses, so it matches only "FORMAT <name>" or "INTO OUTFILE"
// as the tail of the (semicolon-trimmed) statement. This avoids false positives
// on a column named `format`, `formatDateTime(...)`, or a mid-query token, at the
// cost of not catching the (invalid) case where they appear mid-statement — which
// ClickHouse rejects anyway.
func HasUnsupportedOutputClause(sql string) bool {
	// Strip -- line comments first so a trailing "-- FORMAT JSON" comment is not
	// mistaken for an actual output clause. (String literals are not tokenized;
	// a `--` inside a literal is rare and the fallout is only a spurious reject.)
	upper := strings.ToUpper(strings.TrimRight(strings.TrimSpace(stripLineComments(sql)), "; "))
	if strings.HasSuffix(upper, "INTO OUTFILE") || strings.Contains(upper, "INTO OUTFILE ") {
		return true
	}
	// FORMAT <name> as the final clause: find the last FORMAT that is a whole
	// word and is followed only by a single identifier (the format name).
	idx := strings.LastIndex(upper, "FORMAT ")
	if idx < 0 {
		return false
	}
	if idx > 0 && isIdentByte(upper[idx-1]) {
		return false // part of a longer identifier, e.g. formatDateTime
	}
	tail := strings.TrimSpace(upper[idx+len("FORMAT "):])
	return tail != "" && isIdentifier(tail)
}

func stripLineComments(sql string) string {
	lines := strings.Split(sql, "\n")
	for i, line := range lines {
		if j := strings.Index(line, "--"); j >= 0 {
			lines[i] = line[:j]
		}
	}
	return strings.Join(lines, "\n")
}

func isIdentifier(s string) bool {
	for i := 0; i < len(s); i++ {
		if !isIdentByte(s[i]) {
			return false
		}
	}
	return true
}

func isIdentByte(b byte) bool {
	return b == '_' || (b >= '0' && b <= '9') || (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z')
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

// Bound wraps SELECT/WITH to cap the row count; small statements pass through
// (the throw-mode cap is their backstop).
//
// The newline before the closing paren is load-bearing: if the inner query ends
// in a trailing "-- comment", putting ") LIMIT n" on its own line stops the
// comment from swallowing it (which would leave an unmatched paren).
func Bound(sql string, class StmtClass, fetchLimit int) string {
	switch class {
	case ClassSelect:
		inner := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(sql), "; "))
		return "SELECT * FROM (" + inner + "\n) LIMIT " + strconv.Itoa(fetchLimit)
	default:
		return sql
	}
}

// Truncation is the shared shape every list-shaped tool embeds to report how
// many items it returned and whether more existed beyond the applied limit.
type Truncation struct {
	Count     int    `json:"count" jsonschema:"number of items returned"`
	Truncated bool   `json:"truncated" jsonschema:"true if more items existed beyond the limit"`
	Limit     int    `json:"limit" jsonschema:"the applied limit"`
	Note      string `json:"note,omitempty" jsonschema:"guidance when truncated"`
}

type Result struct {
	Columns    []string `json:"columns" jsonschema:"column names, aligned to each row"`
	Rows       [][]any  `json:"rows" jsonschema:"result rows as positional arrays aligned to columns; large integers and decimals are strings to avoid precision loss"`
	Truncation          // embedded: its fields flatten into the JSON object
}

// Shape drops the sentinel (displayLimit+1) row and reports truncation. When a
// truncated result was not ordered, the subset is arbitrary and the note says so.
func Shape(columns []string, fetched [][]any, displayLimit int, ordered bool) Result {
	truncated := len(fetched) > displayLimit
	rows := fetched
	if truncated {
		rows = fetched[:displayLimit]
	}
	r := Result{
		Columns: columns,
		Rows:    rows,
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
