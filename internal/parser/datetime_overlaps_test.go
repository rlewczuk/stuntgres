package parser

import (
	"strings"
	"testing"
)

func TestDatetimeOverlapsRowsAndProvenance(t *testing.T) {
	for _, q := range []string{"SELECT ($1,$2) OVERLAPS ($3,$4)", "SELECT ROW($1,$2) OVERLAPS ROW($3,$4)", "SELECT ROW($1,$2) OVERLAPS ($3,$4)"} {
		statements, err := Parse(q)
		if err != nil {
			t.Fatal(q, err)
		}
		call, ok := statements[0].(*Select).Targets[0].(FunctionExpr)
		if !ok || call.Syntax != SQLFunctionSyntax || len(call.Args) != 4 || len(call.Name.Parts) != 2 || call.Name.Parts[0].Text != "pg_catalog" || call.Name.Parts[1].Text != "overlaps" || call.Pos.Character != strings.Index(q, "OVERLAPS")+1 {
			t.Fatal(q, call)
		}
		for i, arg := range call.Args {
			p, ok := arg.(ParameterExpr)
			if !ok || p.Number != i+1 {
				t.Fatal("lexical occurrence lost", i, arg)
			}
		}
	}
	for _, q := range []string{"SELECT overlaps($1,$2,$3,$4)", "SELECT pg_catalog.overlaps($1,$2,$3,$4)", "SELECT \"overlaps\"($1,$2,$3,$4)"} {
		statements, err := Parse(q)
		if err != nil {
			t.Fatal(q, err)
		}
		call := statements[0].(*Select).Targets[0].(FunctionExpr)
		if call.Syntax == SQLFunctionSyntax || len(call.Args) != 4 {
			t.Fatal("ordinary provenance lost", call)
		}
	}
	k := lookupKeyword("overlaps")
	if k == nil || k.category != TypeFunctionName || k.columnName() || !k.typeFunctionName() || k.label() {
		t.Fatal("keyword category changed", k)
	}
	statements, err := Parse("SELECT 1 AS overlaps")
	if err != nil || statements[0].(*Select).Labels[0].Text != "overlaps" {
		t.Fatal("AS label legality", statements, err)
	}
}

func TestDatetimeOverlapsQualifiedIdentifierConsumers(t *testing.T) {
	for _, q := range []string{
		"CREATE TABLE public.overlaps(x INT)",
		"INSERT INTO public.overlaps(x) VALUES(7)",
		"SELECT x FROM public.overlaps WHERE x=7 ORDER BY x",
		"SELECT t.overlaps FROM public.overlaps t",
	} {
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatal("qualified keyword identifier", q, statements, err)
		}
		if table, ok := statements[0].(*CreateTable); ok {
			if len(table.Name.Parts) != 2 || table.Name.Parts[0].Text != "public" || table.Name.Parts[1].Text != "overlaps" {
				t.Fatal("qualified relation identity lost", table.Name)
			}
		}
	}
	// Qualified ordinary calls and SQL row syntax are independently covered by
	// TestDatetimeOverlapsRowsAndProvenance; qualification must not relax bare names.
	for _, q := range []string{"CREATE TABLE overlaps(x INT)", "CREATE TABLE overlaps.t(x INT)", "CREATE TABLE t(overlaps INT)", "SELECT 1 overlaps"} {
		if statements, err := Parse(q); err == nil || statements != nil {
			t.Fatal("bare keyword category relaxed", q, statements, err)
		}
	}
}
