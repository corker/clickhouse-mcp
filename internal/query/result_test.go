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

func TestBound(t *testing.T) {
	tests := []struct {
		name  string
		sql   string
		class StmtClass
		want  string
	}{
		{"select wraps", "SELECT n FROM t", ClassSelect, "SELECT * FROM (SELECT n FROM t) LIMIT 6"},
		{"select strips trailing semicolon", "SELECT 1;", ClassSelect, "SELECT * FROM (SELECT 1) LIMIT 6"},
		{"select strips trailing space+semicolon", "SELECT 1 ; ", ClassSelect, "SELECT * FROM (SELECT 1) LIMIT 6"},
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
	mk := func(count int) [][]any {
		rows := make([][]any, count)
		for i := range rows {
			rows[i] = []any{i}
		}
		return rows
	}

	t.Run("not truncated when fetched <= limit", func(t *testing.T) {
		r := Shape(cols, mk(3), 5, false)
		if r.Truncated || r.RowCount != 3 || len(r.Rows) != 3 {
			t.Fatalf("got %+v", r)
		}
		if r.Note != "" {
			t.Errorf("expected no note, got %q", r.Note)
		}
	})

	t.Run("truncated at limit+1, sentinel dropped", func(t *testing.T) {
		r := Shape(cols, mk(6), 5, false) // fetched 6 = displayLimit(5)+1
		if !r.Truncated || r.RowCount != 5 || len(r.Rows) != 5 {
			t.Fatalf("got %+v", r)
		}
		if r.Limit != 5 {
			t.Errorf("limit = %d, want 5", r.Limit)
		}
	})

	t.Run("exactly at limit is not truncated", func(t *testing.T) {
		r := Shape(cols, mk(5), 5, false)
		if r.Truncated {
			t.Fatalf("5 rows at limit 5 should not be truncated: %+v", r)
		}
	})

	t.Run("unordered truncated note says arbitrary", func(t *testing.T) {
		r := Shape(cols, mk(6), 5, false)
		if r.Note == "" || !strings.Contains(r.Note, "arbitrary") {
			t.Errorf("expected arbitrary-subset note, got %q", r.Note)
		}
	})

	t.Run("ordered truncated note omits arbitrary", func(t *testing.T) {
		r := Shape(cols, mk(6), 5, true)
		if strings.Contains(r.Note, "arbitrary") {
			t.Errorf("ordered result should not say arbitrary, got %q", r.Note)
		}
		if r.Note == "" {
			t.Errorf("expected a truncation note")
		}
	})
}

func TestHasTopLevelOrderBy(t *testing.T) {
	if !HasTopLevelOrderBy("SELECT 1 ORDER BY x") {
		t.Error("should detect ORDER BY")
	}
	if HasTopLevelOrderBy("select 1") {
		t.Error("should not detect ORDER BY")
	}
	if !HasTopLevelOrderBy("select 1 order by x") {
		t.Error("should be case-insensitive")
	}
}
