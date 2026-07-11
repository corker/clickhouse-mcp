package query

import (
	"strings"
	"testing"
)

func TestClassify(t *testing.T) {
	tests := []struct {
		sql  string
		want StmtClass
	}{
		{"SELECT 1", ClassSelect},
		{"  select 1", ClassSelect},
		{"WITH x AS (SELECT 1) SELECT * FROM x", ClassSelect},
		{"SHOW DATABASES", ClassSmall},
		{"DESCRIBE t", ClassSmall},
		{"DESC t", ClassSmall},
		{"EXPLAIN SELECT 1", ClassSmall},
		{"EXISTS TABLE t", ClassSmall},
		{"-- a comment\nSELECT 1", ClassSelect},
		{"/* c */ SHOW TABLES", ClassSmall},
		{"INSERT INTO t VALUES (1)", ClassRejected},
		{"DROP TABLE t", ClassRejected},
		{"", ClassRejected},
	}
	for _, tt := range tests {
		if got := Classify(tt.sql); got != tt.want {
			t.Errorf("Classify(%q) = %v, want %v", tt.sql, got, tt.want)
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
		{"SELECT 1 FORMAT JSON;", true}, // trailing semicolon
		{"SELECT 1 INTO OUTFILE '/tmp/x'", true},
		{"SELECT 1 into outfile '/x'", true},
		{"SELECT 1", false},
		{"SELECT format FROM t", false},                  // column named format (FROM follows, not an ident tail)
		{"SELECT formatDateTime(x) FROM t", false},       // function prefixed with format
		{"SELECT 'FORMAT' AS s", false},                  // FORMAT inside a literal, mid-query
		{"SELECT 1 -- FORMAT JSON", false},               // FORMAT in a trailing line comment
		{"SELECT 1 -- trailing note\nFORMAT JSON", true}, // real clause after a comment line
		{"SELECT number FROM system.numbers", false},
	}
	for _, tt := range tests {
		if got := HasUnsupportedOutputClause(tt.sql); got != tt.want {
			t.Errorf("HasUnsupportedOutputClause(%q) = %v, want %v", tt.sql, got, tt.want)
		}
	}
}

func TestBound(t *testing.T) {
	tests := []struct {
		name  string
		sql   string
		class StmtClass
		want  string
	}{
		{"select wraps", "SELECT n FROM t", ClassSelect, "SELECT * FROM (SELECT n FROM t\n) LIMIT 6"},
		{"select strips trailing semicolon", "SELECT 1;", ClassSelect, "SELECT * FROM (SELECT 1\n) LIMIT 6"},
		{"select strips trailing space+semicolon", "SELECT 1 ; ", ClassSelect, "SELECT * FROM (SELECT 1\n) LIMIT 6"},
		{"trailing line comment: paren on own line", "SELECT 1 -- c", ClassSelect, "SELECT * FROM (SELECT 1 -- c\n) LIMIT 6"},
		{"small unchanged", "SHOW DATABASES", ClassSmall, "SHOW DATABASES"},
		{"describe unchanged", "DESCRIBE t", ClassSmall, "DESCRIBE t"},
	}
	for _, tt := range tests {
		if got := Bound(tt.sql, tt.class, 6); got != tt.want {
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
		if r.Limit != 5 {
			t.Errorf("limit = %d, want 5", r.Limit)
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

func TestContainsMultipleStatements(t *testing.T) {
	tests := []struct {
		sql  string
		want bool
	}{
		{"SELECT 1", false},
		{"SELECT 1;", false},                          // trailing terminator only
		{"SELECT 1;  \n ", false},                     // trailing terminator + whitespace
		{"SELECT 1; SELECT 2", true},                  // two statements
		{"SELECT 1;SELECT 2;", true},                  // two statements, both terminated
		{"SELECT ';' AS s", false},                    // semicolon inside a string literal
		{"SELECT 1 -- ; not a separator", false},      // semicolon in a line comment
		{"SELECT 1 /* ; still safe */ FROM t", false}, // semicolon in a block comment
		{"SELECT `a;b` FROM t", false},                // semicolon in a backtick identifier
		{"SELECT 'a'; DROP TABLE t", true},            // real separator after a literal closes
		{"SELECT '\\';' AS s", false},                 // escaped quote keeps the literal open
	}
	for _, tt := range tests {
		if got := ContainsMultipleStatements(tt.sql); got != tt.want {
			t.Errorf("ContainsMultipleStatements(%q) = %v, want %v", tt.sql, got, tt.want)
		}
	}
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
