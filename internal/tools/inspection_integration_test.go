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
	_, out, err := listDatabases(context.Background(), conn, listDatabasesArgs{})
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

// Every collection a tool returns is bounded: database names, and a single
// table's columns (a wide table must not dump all columns unbounded).
func TestInspection_PayloadsBounded(t *testing.T) {
	conn := testsupport.Start(t)
	ctx := context.Background()

	// list_databases: an explicit tiny limit truncates.
	_, dbs, err := listDatabases(ctx, conn, listDatabasesArgs{Limit: 2})
	if err != nil || len(dbs.Databases) != 2 || !dbs.Truncated {
		t.Errorf("list_databases limit=2 should truncate: n=%d truncated=%v err=%v", len(dbs.Databases), dbs.Truncated, err)
	}

	// A wide table's columns are capped at MaxColumnsPerTable with a signal.
	var b strings.Builder
	for i := 0; i < MaxColumnsPerTable+50; i++ {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "c%d UInt8", i)
	}
	if err := conn.Exec(ctx, "CREATE TABLE default.wide ("+b.String()+") ENGINE=Memory"); err != nil {
		t.Fatalf("seed wide: %v", err)
	}
	_, out, err := listTables(ctx, conn, listTablesArgs{Database: "default", Table: "wide"})
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

// A missing (or wrong-case) database errors clearly, but a real-but-empty
// database returns an empty list without error — the two must be distinguished.
func TestListTables_MissingVsEmptyDatabase(t *testing.T) {
	conn := testsupport.Start(t)
	ctx := context.Background()

	if _, _, err := listTables(ctx, conn, listTablesArgs{Database: "does_not_exist"}); err == nil {
		t.Error("missing database should error, not return empty")
	}
	// ClickHouse names are case-sensitive: DEFAULT != default.
	if _, _, err := listTables(ctx, conn, listTablesArgs{Database: "DEFAULT"}); err == nil {
		t.Error("wrong-case database should error (case-sensitive)")
	}

	if err := conn.Exec(ctx, "CREATE DATABASE IF NOT EXISTS empty_db"); err != nil {
		t.Fatalf("create empty_db: %v", err)
	}
	_, out, err := listTables(ctx, conn, listTablesArgs{Database: "empty_db"})
	if err != nil {
		t.Errorf("existing-but-empty database should not error, got: %v", err)
	}
	if out.Tables == nil || len(out.Tables) != 0 {
		t.Errorf("empty database should return [], got %v", out.Tables)
	}
}

// A database with more tables than the limit is truncated with a signal, so a
// large database cannot flood the caller's context. table= ignores the limit.
func TestListTables_Truncation(t *testing.T) {
	conn := testsupport.Start(t)
	ctx := context.Background()

	// system has many tables; a low limit must truncate and report it.
	_, out, err := listTables(ctx, conn, listTablesArgs{Database: "system", Limit: 5})
	if err != nil {
		t.Fatalf("list system: %v", err)
	}
	if len(out.Tables) != 5 || !out.Truncated || out.Limit != 5 || out.Note == "" {
		t.Errorf("expected 5 tables truncated with a note, got %d truncated=%v note=%q", len(out.Tables), out.Truncated, out.Note)
	}

	// table= addresses one table and must ignore the browse limit.
	if err := conn.Exec(ctx, "CREATE TABLE default.zz (a UInt8, b UInt8) ENGINE=Memory"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, one, err := listTables(ctx, conn, listTablesArgs{Database: "default", Table: "zz", Limit: 1})
	if err != nil || len(one.Tables) != 1 || one.Truncated || len(one.Tables[0].Columns) != 2 {
		t.Errorf("table= should ignore limit and return full schema: tables=%d truncated=%v err=%v", len(one.Tables), one.Truncated, err)
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

// include_columns uses a tighter default limit than a lean listing (folding every
// column of every table is a bigger payload), so a large database truncates
// rather than dumping every column. An explicit limit still overrides.
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
	// The lean listing keeps the larger default (not clamped by include_columns).
	_, lean, _ := listTables(ctx, conn, listTablesArgs{Database: "system"})
	if len(lean.Tables) <= DefaultFoldedTableLimit {
		t.Errorf("lean listing should not be clamped to the folded limit, got %d", len(lean.Tables))
	}
}

func TestListTables_RequiresDatabase(t *testing.T) {
	conn := testsupport.Start(t)
	if _, _, err := listTables(context.Background(), conn, listTablesArgs{}); err == nil {
		t.Fatal("expected error when database is empty")
	}
}
