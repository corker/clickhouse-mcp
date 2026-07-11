package tools

import (
	"context"
	"strings"
	"testing"
)

// The tool gates reject before touching the connection, so they are pure logic
// and run without a container (conn is nil — a live call would panic, proving
// the gate short-circuits first).

func TestRunQuery_RejectsNonRowReturning(t *testing.T) {
	cases := []struct{ name, sql string }{
		{"insert", "INSERT INTO t VALUES (1)"},
		{"create", "CREATE TABLE t (x UInt8) ENGINE=Memory"},
		{"drop", "DROP TABLE t"},
		{"empty", ""},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := runQuery(context.Background(), nil, runQueryArgs{SQL: tt.sql})
			if err == nil || !strings.Contains(err.Error(), "run_statement") {
				t.Errorf("want rejection pointing to run_statement, got: %v", err)
			}
		})
	}
}

func TestRunQuery_RejectsOutputClauses(t *testing.T) {
	cases := []struct{ name, sql string }{
		{"format", "SELECT 1 FORMAT JSON"},
		{"outfile", "SELECT 1 INTO OUTFILE '/tmp/x'"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := runQuery(context.Background(), nil, runQueryArgs{SQL: tt.sql})
			if err == nil || !strings.Contains(err.Error(), "not supported") {
				t.Errorf("want a clear 'not supported' rejection, got: %v", err)
			}
		})
	}
}

func TestRunStatement_RejectsRowReturning(t *testing.T) {
	cases := []struct{ name, sql string }{
		{"select", "SELECT 1"},
		{"show", "SHOW DATABASES"},
		{"describe", "DESCRIBE system.numbers"},
		{"with", "WITH x AS (SELECT 1) SELECT * FROM x"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := runStatement(context.Background(), nil, runStatementArgs{SQL: tt.sql})
			if err == nil || !strings.Contains(err.Error(), "run_query") {
				t.Errorf("want rejection pointing to run_query, got: %v", err)
			}
		})
	}
}
