package query

import (
	"strconv"
	"strings"
)

// IsRowReturning reports whether a statement produces rows, i.e. belongs on
// run_query rather than run_statement. This is a routing decision, not an
// authorization one — ClickHouse's per-user privileges are the boundary
// (ADR-0006). It keeps a write out of the Query path, where the driver would
// still execute the write and then error on the empty result set.
func IsRowReturning(sql string) bool {
	switch leadingKeyword(sql) {
	case "SELECT", "WITH", "SHOW", "DESCRIBE", "DESC", "EXPLAIN", "EXISTS":
		return true
	default:
		return false
	}
}

// HasUnsupportedOutputClause reports whether the query ends in FORMAT or INTO
// OUTFILE, which redirect output away from the structured rows this tool returns.
// Unwrapped they run but yield no rows (and INTO OUTFILE may write a file
// server-side), so run_query rejects them with a clear message instead.
func HasUnsupportedOutputClause(sql string) bool {
	upper := strings.ToUpper(strings.TrimRight(strings.TrimSpace(stripLineComments(sql)), "; "))
	if strings.HasSuffix(upper, "INTO OUTFILE") || strings.Contains(upper, "INTO OUTFILE ") {
		return true
	}
	// FORMAT <name> as the final clause: the last whole-word FORMAT followed only
	// by a single identifier (the format name).
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

// ContainsMultipleStatements reports whether sql holds more than one statement —
// a semicolon with real content after it (a single trailing ';' does not count).
// Both tools reject a multi-statement before executing: run_query's wrap would
// otherwise turn it into a syntax error that leaks the injected LIMIT wrapper,
// and — verified against a live server — clickhouse-go's Exec runs only the FIRST
// statement of a multi-statement write and silently drops the rest with no error
// (ClickHouse issue #66931). Rejecting keeps "one statement per call" honest.
//
// Semicolons inside string/identifier literals and comments do not separate
// statements, so they are skipped. This is a scanner, not a full parser: it
// tracks quote and comment state, which is enough to place the real separators.
func ContainsMultipleStatements(sql string) bool {
	const (
		normal = iota
		inSingle
		inDouble
		inBacktick
		inLine
		inBlock
	)
	state := normal
	for i := 0; i < len(sql); i++ {
		c := sql[i]
		switch state {
		case normal:
			switch {
			case c == '\'':
				state = inSingle
			case c == '"':
				state = inDouble
			case c == '`':
				state = inBacktick
			case c == '-' && i+1 < len(sql) && sql[i+1] == '-':
				state = inLine
				i++
			case c == '/' && i+1 < len(sql) && sql[i+1] == '*':
				state = inBlock
				i++
			case c == ';':
				// A separator only if anything but whitespace follows.
				if strings.TrimSpace(sql[i+1:]) != "" {
					return true
				}
			}
		case inSingle:
			switch c {
			case '\\':
				i++ // skip an escaped char inside the literal
			case '\'':
				state = normal
			}
		case inDouble:
			switch c {
			case '\\':
				i++
			case '"':
				state = normal
			}
		case inBacktick:
			if c == '`' {
				state = normal
			}
		case inLine:
			if c == '\n' {
				state = normal
			}
		case inBlock:
			if c == '*' && i+1 < len(sql) && sql[i+1] == '/' {
				state = normal
				i++
			}
		}
	}
	return false
}

// canBound reports whether a statement can be wrapped in SELECT * FROM (...)
// LIMIT n+1 for row bounding. Only SELECT/WITH accept the wrap; the other
// row-returning statements run as-is under the cap ceiling.
func canBound(sql string) bool {
	switch leadingKeyword(sql) {
	case "SELECT", "WITH":
		return true
	default:
		return false
	}
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
// whitespace, line/block comments, and opening parens. The paren skip matters
// because "(SELECT 1)" and "(SELECT 1) UNION ALL (SELECT 2)" are valid
// row-returning queries whose keyword would otherwise read as empty.
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
		case strings.HasPrefix(s, "("):
			s = strings.TrimSpace(s[1:])
			continue
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
// (the throw-mode cap is their backstop). It owns the boundability decision so a
// caller cannot wrap a statement that should not be wrapped.
//
// The newline before the closing paren is load-bearing: if the inner query ends
// in a trailing "-- comment", putting ") LIMIT n" on its own line stops the
// comment from swallowing it (which would leave an unmatched paren).
func Bound(sql string, fetchLimit int) string {
	if !canBound(sql) {
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
