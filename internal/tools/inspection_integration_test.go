//go:build integration

package tools

import (
	"context"
	"slices"
	"testing"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"github.com/corker/clickhouse-mcp/internal/clickhouse/testsupport"
)

func seedInspectionFixture(t *testing.T, conn driver.Conn) {
	t.Helper()
	ctx := context.Background()
	stmts := []string{
		"CREATE TABLE default.orders (id UInt64, amount Decimal(10,2)) ENGINE=MergeTree ORDER BY id COMMENT 'customer orders'",
		"INSERT INTO default.orders SELECT number, number FROM system.numbers LIMIT 10",
		"CREATE VIEW default.orders_view AS SELECT id FROM default.orders",
	}
	for _, s := range stmts {
		if err := conn.Exec(ctx, s); err != nil {
			t.Fatalf("seed %q: %v", s, err)
		}
	}
}

func TestListDatabases(t *testing.T) {
	conn := testsupport.Start(t)
	_, out, err := listDatabases(context.Background(), conn)
	if err != nil {
		t.Fatalf("listDatabases: %v", err)
	}
	if !slices.Contains(out.Databases, "default") || !slices.Contains(out.Databases, "system") {
		t.Errorf("expected default and system in %v", out.Databases)
	}
}

func TestListTables_Lean(t *testing.T) {
	conn := testsupport.Start(t)
	seedInspectionFixture(t, conn)

	_, out, err := listTables(context.Background(), conn, listTablesArgs{Database: "default"})
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
	// Lean default: no columns folded.
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

func TestListTables_SingleTableSchema(t *testing.T) {
	conn := testsupport.Start(t)
	seedInspectionFixture(t, conn)

	_, out, err := listTables(context.Background(), conn, listTablesArgs{Database: "default", Table: "orders"})
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

func TestListTables_NotFound(t *testing.T) {
	conn := testsupport.Start(t)
	_, _, err := listTables(context.Background(), conn, listTablesArgs{Database: "default", Table: "nope"})
	if err == nil {
		t.Fatal("expected a not-found error for a missing table, got nil")
	}
}

func TestListTables_IncludeColumns(t *testing.T) {
	conn := testsupport.Start(t)
	seedInspectionFixture(t, conn)

	_, out, err := listTables(context.Background(), conn, listTablesArgs{Database: "default", Columns: true})
	if err != nil {
		t.Fatalf("listTables include_columns: %v", err)
	}
	for _, tbl := range out.Tables {
		if len(tbl.Columns) == 0 {
			t.Errorf("include_columns should fold schema for %q, got none", tbl.Name)
		}
	}
}

func TestListTables_RequiresDatabase(t *testing.T) {
	conn := testsupport.Start(t)
	if _, _, err := listTables(context.Background(), conn, listTablesArgs{}); err == nil {
		t.Fatal("expected error when database is empty")
	}
}
