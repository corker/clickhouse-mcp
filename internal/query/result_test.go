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
