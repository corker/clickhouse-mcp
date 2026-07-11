package query

import (
	"strings"
	"testing"
)

func TestCanBound(t *testing.T) {
	tests := []struct {
		sql  string
		want bool
	}{
		{"SELECT 1", true},
		{"  select 1", true},
		{"WITH x AS (SELECT 1) SELECT * FROM x", true},
		{"-- a comment\nSELECT 1", true},
		{"SHOW DATABASES", false},
		{"DESCRIBE t", false},
		{"EXPLAIN SELECT 1", false},
		{"EXISTS TABLE t", false},
		{"INSERT INTO t VALUES (1)", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := canBound(tt.sql); got != tt.want {
			t.Errorf("canBound(%q) = %v, want %v", tt.sql, got, tt.want)
		}
	}
}

func TestIsRowReturning(t *testing.T) {
	tests := []struct {
		sql  string
		want bool
	}{
		{"SELECT 1", true},
		{"WITH x AS (SELECT 1) SELECT * FROM x", true},
		{"SHOW DATABASES", true},
		{"DESCRIBE t", true},
		{"DESC t", true},
		{"EXPLAIN SELECT 1", true},
		{"EXISTS TABLE t", true},
		{"INSERT INTO t VALUES (1)", false},
		{"CREATE TABLE t (x UInt8) ENGINE=Memory", false},
		{"DROP TABLE t", false},
		{"ALTER TABLE t ADD COLUMN y UInt8", false},
		{"", false},
		// Parenthesized queries are valid and row-returning — the gate must see
		// through the leading paren, or run_query rejects them and run_statement
		// silently swallows them.
		{"(SELECT 1)", true},
		{"( SELECT 1 )", true},
		{"((SELECT 1))", true},
		{"(SELECT 1) UNION ALL (SELECT 2)", true},
		{"(INSERT INTO t VALUES (1))", false},
	}
	for _, tt := range tests {
		if got := IsRowReturning(tt.sql); got != tt.want {
			t.Errorf("IsRowReturning(%q) = %v, want %v", tt.sql, got, tt.want)
		}
	}
}

func TestHasUnsupportedOutputClause(t *testing.T) {
	tests := []struct {
		sql  string
		want bool
	}{
		{"SELECT 1 FORMAT JSON", true},
		{"SELECT 1 format json", true},
		{"SELECT 1 FORMAT JSON;", true},
		{"SELECT 1 INTO OUTFILE '/tmp/x'", true},
		{"SELECT 1", false},
		{"SELECT format FROM t", false},            // column named format
		{"SELECT formatDateTime(x) FROM t", false}, // function prefixed with format
		{"SELECT 'FORMAT' AS s", false},            // FORMAT inside a literal
		{"SELECT 1 -- FORMAT JSON", false},         // FORMAT in a trailing comment
		{"SELECT number FROM system.numbers", false},
	}
	for _, tt := range tests {
		if got := HasUnsupportedOutputClause(tt.sql); got != tt.want {
			t.Errorf("HasUnsupportedOutputClause(%q) = %v, want %v", tt.sql, got, tt.want)
		}
	}
}

func TestContainsMultipleStatements(t *testing.T) {
	tests := []struct {
		sql  string
		want bool
	}{
		{"SELECT 1", false},
		{"SELECT 1;", false},         // trailing terminator only
		{"SELECT 1;  \n ", false},    // trailing terminator + whitespace
		{"SELECT 1; SELECT 2", true}, // two statements
		{"SELECT 1;SELECT 2;", true}, // two statements, both terminated
		{"INSERT INTO t VALUES (1); INSERT INTO t VALUES (2)", true}, // the silent-partial-write case
		{"SELECT ';' AS s", false},                                   // semicolon inside a string literal
		{"SELECT 1 -- ; not a separator", false},                     // semicolon in a line comment
		{"SELECT 1 /* ; still safe */ FROM t", false},                // semicolon in a block comment
		{"SELECT `a;b` FROM t", false},                               // semicolon in a backtick identifier
		{"SELECT 'a'; DROP TABLE t", true},                           // real separator after a literal closes
		{"SELECT '\\';' AS s", false},                                // escaped quote keeps the literal open
		// ClickHouse's doubled-escaping ('' for a quote, `` for a backtick): the
		// scanner stays in sync by parity, or a ';' after a doubled literal reads
		// as a false negative and the silent-partial-write reopens.
		{"SELECT 'it''s' AS s", false},                                       // '' inside a single literal
		{"SELECT 'a''; b' AS s", false},                                      // ';' inside a doubled-quote literal
		{"INSERT INTO t VALUES ('x'';''y'); INSERT INTO t VALUES (1)", true}, // real 2nd stmt after a doubled-quote literal
		{"SELECT `a``b` FROM t; DROP TABLE t", true},                         // real 2nd stmt after a doubled-backtick identifier
	}
	for _, tt := range tests {
		if got := ContainsMultipleStatements(tt.sql); got != tt.want {
			t.Errorf("ContainsMultipleStatements(%q) = %v, want %v", tt.sql, got, tt.want)
		}
	}
}

func TestBound(t *testing.T) {
	tests := []struct {
		name string
		sql  string
		want string
	}{
		{"boundable wraps", "SELECT n FROM t", "SELECT * FROM (SELECT n FROM t\n) LIMIT 6"},
		{"strips trailing semicolon", "SELECT 1;", "SELECT * FROM (SELECT 1\n) LIMIT 6"},
		{"strips trailing space+semicolon", "SELECT 1 ; ", "SELECT * FROM (SELECT 1\n) LIMIT 6"},
		{"trailing line comment: paren on own line", "SELECT 1 -- c", "SELECT * FROM (SELECT 1 -- c\n) LIMIT 6"},
		{"non-boundable unchanged", "SHOW DATABASES", "SHOW DATABASES"},
		{"describe unchanged", "DESCRIBE t", "DESCRIBE t"},
	}
	for _, tt := range tests {
		if got := Bound(tt.sql, 6); got != tt.want {
			t.Errorf("Bound(%q) = %q, want %q", tt.sql, got, tt.want)
		}
	}
}

func TestShape(t *testing.T) {
	cols := []string{"n"}
	types := []string{"UInt64"}
	mk := func(count int) [][]any {
		rows := make([][]any, count)
		for i := range rows {
			rows[i] = []any{i}
		}
		return rows
	}

	t.Run("not truncated when fetched <= limit", func(t *testing.T) {
		r := Shape(cols, types, mk(3), 5, false)
		if r.Truncated || r.Count != 3 || len(r.Rows) != 3 {
			t.Fatalf("got %+v", r)
		}
		if r.Note != "" {
			t.Errorf("expected no note, got %q", r.Note)
		}
	})

	t.Run("truncated at limit+1, sentinel dropped", func(t *testing.T) {
		r := Shape(cols, types, mk(6), 5, false) // fetched 6 = displayLimit(5)+1
		if !r.Truncated || r.Count != 5 || len(r.Rows) != 5 {
			t.Fatalf("got %+v", r)
		}
		// The dropped row must be the last one (the sentinel), so the kept rows are
		// the first 5 (values 0..4), not an arbitrary subset.
		if r.Rows[4][0] != 4 {
			t.Errorf("kept rows should be the first 5 (0..4); last kept = %v, want 4", r.Rows[4][0])
		}
	})

	t.Run("exactly at limit is not truncated", func(t *testing.T) {
		r := Shape(cols, types, mk(5), 5, false)
		if r.Truncated {
			t.Fatalf("5 rows at limit 5 should not be truncated: %+v", r)
		}
	})

	t.Run("unordered truncated note says arbitrary", func(t *testing.T) {
		r := Shape(cols, types, mk(6), 5, false)
		if r.Note == "" || !strings.Contains(r.Note, "arbitrary") {
			t.Errorf("expected arbitrary-subset note, got %q", r.Note)
		}
	})

	t.Run("ordered truncated note omits arbitrary", func(t *testing.T) {
		r := Shape(cols, types, mk(6), 5, true)
		if strings.Contains(r.Note, "arbitrary") {
			t.Errorf("ordered result should not say arbitrary, got %q", r.Note)
		}
		if r.Note == "" {
			t.Errorf("expected a truncation note")
		}
	})
}

func TestHasTopLevelOrderBy(t *testing.T) {
	tests := []struct {
		sql  string
		want bool
	}{
		{"SELECT 1 ORDER BY x", true},
		{"select 1 order by x", true}, // case-insensitive
		{"select 1", false},
	}
	for _, tt := range tests {
		if got := HasTopLevelOrderBy(tt.sql); got != tt.want {
			t.Errorf("HasTopLevelOrderBy(%q) = %v, want %v", tt.sql, got, tt.want)
		}
	}
}
