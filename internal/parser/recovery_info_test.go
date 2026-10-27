package parser

import "testing"

func TestRecoveryInfoRegistryCallSyntax(t *testing.T) {
	for _, tc := range []struct {
		call string
		name string
		parts int
	}{
		{"pg_get_wal_resource_managers(*)", "pg_get_wal_resource_managers", 1},
		{"pg_catalog.pg_get_wal_resource_managers(*)", "pg_get_wal_resource_managers", 2},
		{`"PG_GET_WAL_RESOURCE_MANAGERS"(*)`, "PG_GET_WAL_RESOURCE_MANAGERS", 1},
	} {
		statements, err := Parse("/*é🙂*/ SELECT " + tc.call)
		if err != nil || len(statements) != 1 {
			t.Fatal(tc.call, statements, err)
		}
		call, ok := statements[0].(*Select).Targets[0].(FunctionExpr)
		if !ok || !call.Star || len(call.Args) != 0 || len(call.Name.Parts) != tc.parts || call.Name.Parts[tc.parts-1].Text != tc.name || call.Pos.Character != 15 {
			t.Fatal("lost ordinary rejection syntax or spelling", tc.call, call)
		}
	}
	statements, err := Parse("SELECT pg_get_wal_resource_managers FROM t")
	if err != nil || len(statements) != 1 {
		t.Fatal(statements, err)
	}
	column, ok := statements[0].(*Select).Targets[0].(ColumnExpr)
	if !ok || column.Name.Parts[0].Text != "pg_get_wal_resource_managers" {
		t.Fatal("bare identifier changed", statements)
	}
}
