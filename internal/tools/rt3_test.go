//go:build integration

package tools

import (
	"context"
	"testing"

	"github.com/corker/clickhouse-mcp/internal/clickhouse/testsupport"
)

// A SELECT ending in a trailing line comment must still execute: the wrap must
// put ") LIMIT n" on its own line so the comment does not swallow it.
func TestRunQuery_TrailingLineComment(t *testing.T) {
	conn := testsupport.Start(t)
	cases := []string{
		"SELECT 1 -- trailing comment",
		"SELECT number FROM system.numbers LIMIT 3 -- note",
		"SELECT '--x' AS s", // -- inside a literal must round-trip
	}
	for _, sql := range cases {
		if _, _, err := runQuery(context.Background(), conn, runQueryArgs{SQL: sql, Limit: 5}); err != nil {
			t.Errorf("%q should execute, got: %v", sql, err)
		}
	}
}
