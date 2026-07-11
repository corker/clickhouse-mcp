//go:build integration

package tools

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"github.com/corker/clickhouse-mcp/internal/clickhouse/testsupport"
)

// seedOrders creates the shared "orders" table (with rows) and a view in db.
func seedOrders(t *testing.T, conn driver.Conn, db string) {
	t.Helper()
	ctx := context.Background()
	stmts := []string{
		fmt.Sprintf("CREATE TABLE %s.orders (id UInt64, amount Decimal(10,2)) ENGINE=MergeTree ORDER BY id COMMENT 'customer orders'", db),
		fmt.Sprintf("INSERT INTO %s.orders SELECT number, number FROM system.numbers LIMIT 10", db),
		fmt.Sprintf("CREATE VIEW %s.orders_view AS SELECT id FROM %s.orders", db, db),
	}
	for _, s := range stmts {
		if err := conn.Exec(ctx, s); err != nil {
			t.Fatalf("seed %q: %v", s, err)
		}
	}
}

// wideColumns builds a "c0 UInt8, c1 UInt8, ..." definition of n columns.
func wideColumns(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "c%d UInt8", i)
	}
	return b.String()
}

func TestListDatabases(t *testing.T) {
	conn := testsupport.Start(t)
	_, out, err := listDatabases(context.Background(), conn, listDatabasesArgs{})
	if err != nil {
		t.Fatalf("listDatabases: %v", err)
	}
	// default and system always exist on the shared container.
	if !slices.Contains(out.Databases, "default") || !slices.Contains(out.Databases, "system") {
		t.Errorf("expected default and system in %v", out.Databases)
	}
}

func TestListTables_Lean(t *testing.T) {
	conn, db := testsupport.Database(t)
	seedOrders(t, conn, db)

	_, out, err := listTables(context.Background(), conn, listTablesArgs{Database: db})
	if err != nil {
		t.Fatalf("listTables: %v", err)
	}
	var orders, view *tableInfo
	for i := range out.Tables {
		switch out.Tables[i].Name {
		case "orders":
			orders = &out.Tables[i]
		case "orders_view":
			view = &out.Tables[i]
		}
	}
	if orders == nil || view == nil {
		t.Fatalf("expected orders and orders_view, got %+v", out.Tables)
	}
	if orders.Columns != nil {
		t.Errorf("lean listing should not fold columns, got %v", orders.Columns)
	}
	if orders.Engine != "MergeTree" || orders.RowCount == nil || *orders.RowCount != 10 {
		t.Errorf("orders: engine/rowcount wrong: %+v", orders)
	}
	if orders.Comment != "customer orders" {
		t.Errorf("orders comment = %q", orders.Comment)
	}
	// A view has no tracked row count -> null.
	if view.RowCount != nil {
		t.Errorf("view row_count should be null, got %v", *view.RowCount)
	}
}

func TestListDatabases_Truncation(t *testing.T) {
	conn := testsupport.Start(t)
	ctx := context.Background()

	// A tiny explicit limit truncates with a note that mentions the count.
	_, dbs, err := listDatabases(ctx, conn, listDatabasesArgs{Limit: 2})
	if err != nil || len(dbs.Databases) != 2 || !dbs.Truncated || !strings.Contains(dbs.Note, "2") {
		t.Errorf("limit=2 should truncate with a note: n=%d truncated=%v note=%q err=%v",
			len(dbs.Databases), dbs.Truncated, dbs.Note, err)
	}

	// The common (non-truncated) case is clean: no truncation, no note, default limit.
	_, all, err := listDatabases(ctx, conn, listDatabasesArgs{})
	if err != nil || all.Truncated || all.Note != "" || all.Limit != DefaultDatabaseLimit {
		t.Errorf("small server should not truncate: truncated=%v note=%q limit=%d err=%v",
			all.Truncated, all.Note, all.Limit, err)
	}
}

// A single wide table's columns are capped at MaxColumnsPerTable with a signal.
func TestListTables_ColumnCap(t *testing.T) {
	conn, db := testsupport.Database(t)
	ctx := context.Background()

	if err := conn.Exec(ctx, fmt.Sprintf("CREATE TABLE %s.wide (%s) ENGINE=Memory", db, wideColumns(MaxColumnsPerTable+50))); err != nil {
		t.Fatalf("seed wide: %v", err)
	}
	_, out, err := listTables(ctx, conn, listTablesArgs{Database: db, Table: "wide"})
	if err != nil {
		t.Fatalf("list wide: %v", err)
	}
	c := out.Tables[0]
	if len(c.Columns) != MaxColumnsPerTable || !c.ColumnsTruncated {
		t.Errorf("wide table columns should cap at %d and flag truncated, got %d truncated=%v",
			MaxColumnsPerTable, len(c.Columns), c.ColumnsTruncated)
	}
}

func TestListTables_SingleTableSchema(t *testing.T) {
	conn, db := testsupport.Database(t)
	seedOrders(t, conn, db)

	_, out, err := listTables(context.Background(), conn, listTablesArgs{Database: db, Table: "orders"})
	if err != nil {
		t.Fatalf("listTables table=: %v", err)
	}
	if len(out.Tables) != 1 || out.Tables[0].Name != "orders" {
		t.Fatalf("expected only orders, got %+v", out.Tables)
	}
	cols := out.Tables[0].Columns
	if len(cols) != 2 || cols[0].Name != "id" || cols[0].Type != "UInt64" {
		t.Errorf("orders columns wrong: %+v", cols)
	}
}

// Bad arguments each produce a clear error (case-sensitive names included).
func TestListTables_ArgErrors(t *testing.T) {
	conn := testsupport.Start(t)
	ctx := context.Background()
	cases := []struct {
		name string
		args listTablesArgs
	}{
		{"missing table", listTablesArgs{Database: "default", Table: "nope_no_such"}},
		{"missing database", listTablesArgs{Database: "does_not_exist"}},
		{"wrong-case database", listTablesArgs{Database: "DEFAULT"}}, // names are case-sensitive
		{"database required", listTablesArgs{}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := listTables(ctx, conn, tt.args); err == nil {
				t.Errorf("expected an error for %+v, got nil", tt.args)
			}
		})
	}
}

// A real-but-empty database returns [] without error — distinct from the missing
// database above, which errors.
func TestListTables_EmptyDatabaseIsNotAnError(t *testing.T) {
	conn, db := testsupport.Database(t) // Database creates it empty
	_, out, err := listTables(context.Background(), conn, listTablesArgs{Database: db})
	if err != nil {
		t.Errorf("existing-but-empty database should not error, got: %v", err)
	}
	if out.Tables == nil || len(out.Tables) != 0 || out.Truncated {
		t.Errorf("empty database should return [] not truncated, got %v truncated=%v", out.Tables, out.Truncated)
	}
}

// A database with more tables than the limit is truncated with a signal, so a
// large database cannot flood the caller's context. table= ignores the limit.
func TestListTables_Truncation(t *testing.T) {
	conn, db := testsupport.Database(t)
	ctx := context.Background()

	// Seed more tables than the limit.
	for i := 0; i < 8; i++ {
		if err := conn.Exec(ctx, fmt.Sprintf("CREATE TABLE %s.t%d (x UInt8) ENGINE=Memory", db, i)); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	_, out, err := listTables(ctx, conn, listTablesArgs{Database: db, Limit: 5})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(out.Tables) != 5 || !out.Truncated || out.Limit != 5 || out.Note == "" {
		t.Errorf("expected 5 tables truncated with a note, got %d truncated=%v note=%q", len(out.Tables), out.Truncated, out.Note)
	}

	// table= addresses one table and must ignore the browse limit.
	if err := conn.Exec(ctx, fmt.Sprintf("CREATE TABLE %s.zz (a UInt8, b UInt8) ENGINE=Memory", db)); err != nil {
		t.Fatalf("seed zz: %v", err)
	}
	_, one, err := listTables(ctx, conn, listTablesArgs{Database: db, Table: "zz", Limit: 1})
	if err != nil || len(one.Tables) != 1 || one.Truncated || len(one.Tables[0].Columns) != 2 {
		t.Errorf("table= should ignore limit and return full schema: tables=%d truncated=%v err=%v", len(one.Tables), one.Truncated, err)
	}
}

func TestListTables_IncludeColumns(t *testing.T) {
	conn, db := testsupport.Database(t)
	seedOrders(t, conn, db)

	_, out, err := listTables(context.Background(), conn, listTablesArgs{Database: db, Columns: true})
	if err != nil {
		t.Fatalf("listTables include_columns: %v", err)
	}
	var orders *tableInfo
	for i := range out.Tables {
		if out.Tables[i].Name == "orders" {
			orders = &out.Tables[i]
		}
		if len(out.Tables[i].Columns) == 0 {
			t.Errorf("include_columns should fold schema for %q, got none", out.Tables[i].Name)
		}
	}
	if orders == nil || len(orders.Columns) != 2 ||
		orders.Columns[0].Name != "id" || orders.Columns[0].Type != "UInt64" {
		t.Errorf("orders should fold to {id UInt64, amount Decimal}, got %+v", orders)
	}
}

// The per-table column cap must fire on the include_columns fold path too, not
// only via table=.
func TestListTables_IncludeColumns_ColumnCap(t *testing.T) {
	conn, db := testsupport.Database(t)
	ctx := context.Background()

	if err := conn.Exec(ctx, fmt.Sprintf("CREATE TABLE %s.wide (%s) ENGINE=Memory", db, wideColumns(MaxColumnsPerTable+50))); err != nil {
		t.Fatalf("seed wide: %v", err)
	}
	_, out, err := listTables(ctx, conn, listTablesArgs{Database: db, Columns: true})
	if err != nil {
		t.Fatalf("include_columns: %v", err)
	}
	var wide *tableInfo
	for i := range out.Tables {
		if out.Tables[i].Name == "wide" {
			wide = &out.Tables[i]
		}
	}
	if wide == nil || len(wide.Columns) != MaxColumnsPerTable || !wide.ColumnsTruncated {
		t.Errorf("wide table via include_columns should cap at %d with ColumnsTruncated, got %+v",
			MaxColumnsPerTable, wide)
	}
}

// include_columns uses a tighter default limit than a lean listing. system has
// many tables, so the folded default truncates while the lean default does not.
func TestListTables_IncludeColumnsTighterDefault(t *testing.T) {
	conn := testsupport.Start(t)
	ctx := context.Background()

	_, folded, err := listTables(ctx, conn, listTablesArgs{Database: "system", Columns: true})
	if err != nil {
		t.Fatalf("include_columns system: %v", err)
	}
	if len(folded.Tables) != DefaultFoldedTableLimit || !folded.Truncated {
		t.Errorf("folded default should cap at %d and truncate, got %d truncated=%v",
			DefaultFoldedTableLimit, len(folded.Tables), folded.Truncated)
	}
	_, lean, _ := listTables(ctx, conn, listTablesArgs{Database: "system"})
	if len(lean.Tables) <= DefaultFoldedTableLimit {
		t.Errorf("lean listing should not be clamped to the folded limit, got %d", len(lean.Tables))
	}
}
