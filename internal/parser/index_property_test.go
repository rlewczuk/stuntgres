package parser

import (
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestIndexPropertySelectedCallAdmission(t *testing.T) {
	for _, tc := range []struct {
		name, args string
		count      int
	}{
		{"pg_indexam_has_property", "403,'can_order'", 2},
		{"pg_index_has_property", "'ip_index','index_scan'", 2},
		{"pg_index_column_has_property", "'ip_index',1,'asc'", 3},
	} {
		for _, prefix := range []string{"", "pg_catalog.", "public."} {
			query := "SELECT " + prefix + tc.name + "(" + tc.args + ") FROM ip_data"
			statements, err := Parse(query)
			if err != nil || len(statements) != 1 {
				t.Fatal(query, statements, err)
			}
			selectNode, ok := statements[0].(*Select)
			if !ok || len(selectNode.Targets) != 1 {
				t.Fatal(query, statements[0])
			}
			call, ok := selectNode.Targets[0].(FunctionExpr)
			if !ok || len(call.Args) != tc.count || call.Name.Parts[len(call.Name.Parts)-1].Text != tc.name {
				t.Fatal(query, selectNode.Targets[0])
			}
			query = "SELECT a FROM ip_data WHERE " + prefix + tc.name + "(" + tc.args + ") IS TRUE ORDER BY a"
			statements, err = Parse(query)
			if err != nil || len(statements) != 1 {
				t.Fatal(query, statements, err)
			}
			predicate, ok := statements[0].(*Select).Where.(PredicateExpr)
			if !ok || predicate.Kind != IsTrue {
				t.Fatal(query, statements[0])
			}
			call, ok = predicate.Left.(FunctionExpr)
			if !ok || len(call.Args) != tc.count || call.Name.Parts[len(call.Name.Parts)-1].Text != tc.name {
				t.Fatal(query, predicate.Left)
			}
		}
		query := `SELECT "` + strings.ToUpper(tc.name) + `"(` + tc.args + `) FROM ip_data`
		statements, err := Parse(query)
		if err != nil || len(statements) != 1 {
			t.Fatal(query, statements, err)
		}
		call, ok := statements[0].(*Select).Targets[0].(FunctionExpr)
		if !ok || call.Name.Parts[0].Text != strings.ToUpper(tc.name) {
			t.Fatal("quoted case was normalized", call)
		}
		query = "CREATE TABLE ip_checked(a INT CHECK(" + tc.name + "(" + tc.args + ") IS TRUE))"
		if statements, err := Parse(query); err != nil || len(statements) != 1 {
			t.Fatal(query, statements, err)
		}
		query = "CREATE TABLE ip_default_boundary(a BOOL DEFAULT " + tc.name + "(" + tc.args + "))"
		keywordDiagnostic(t, query, "0A000", "functions are unsupported", strconv.Itoa(strings.Index(query, tc.name)+1))
	}
}

func TestIndexPropertyNestedDefaultBoundary(t *testing.T) {
	for _, named := range []string{"", "CONSTRAINT ip_default_name "} {
		for _, expression := range []string{
			"COALESCE(pg_catalog.pg_indexam_has_property(403,'can_order'),false)",
			"CASE WHEN false THEN pg_index_has_property(NULL,'index_scan') ELSE true END",
			"CAST(pg_index_column_has_property(NULL,1,'asc') AS BOOL)",
		} {
			query := `CREATE TABLE "ip_été_default"(a BOOL ` + named + "DEFAULT (" + expression + "))"
			offset := strings.Index(query, "pg_")
			position := utf8.RuneCountInString(query[:offset]) + 1
			keywordDiagnostic(t, query, "0A000", "functions are unsupported", strconv.Itoa(position))
		}
	}
	for _, query := range []string{
		"CREATE TABLE ip_unrelated(a TEXT DEFAULT btrim(' x '))",
		"CREATE TABLE ip_unrelated(a BOOL DEFAULT (NULL IS NULL))",
	} {
		if statements, err := Parse(query); err != nil || len(statements) != 1 {
			t.Fatal("unrelated DEFAULT admission changed", query, statements, err)
		}
	}
}
