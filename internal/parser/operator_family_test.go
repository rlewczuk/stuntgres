package parser

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// PostgreSQL 18 ALTER OPERATOR FAMILY synopsis permits OPERATOR entries both
// after ADD/DROP and after commas. Public acceptance is independently covered
// by TestReferenceOperatorFamilySmoke and SecondaryUniqueness.
// https://www.postgresql.org/docs/18/sql-alteropfamily.html
func TestOperatorFamilyParser(t *testing.T) {
	for _, action := range []string{"ADD", "DROP"} {
		query := "/*é*/ ALTER OPERATOR FAMILY public.family_probe USING btree " + action + " "
		if action == "ADD" {
			query += "OPERATOR 1 pg_catalog.<(integer,integer), OPERATOR 2 pg_catalog.<=(integer,integer) FOR SEARCH, FUNCTION 1 pg_catalog.btint4cmp(integer,integer)"
		} else {
			query += "OPERATOR 1(integer), OPERATOR 2(integer,integer), FUNCTION 1(integer)"
		}
		statements, err := Parse(query)
		if err != nil || len(statements) != 1 {
			t.Fatal(action, statements, err)
		}
		node, ok := statements[0].(*AlterOperatorFamily)
		if !ok || node.Action != strings.ToLower(action) || node.Method.Text != "btree" || len(node.Items) != 3 {
			t.Fatalf("%s AST: %#v", action, statements[0])
		}
		for i, spelling := range []string{"OPERATOR 1", "OPERATOR 2", "FUNCTION 1"} {
			item := node.Items[i]
			want := utf8.RuneCountInString(query[:strings.Index(query, spelling)]) + 1
			if item.Pos.Character != want || item.Function != (i == 2) {
				t.Fatalf("%s item %d: %#v, want position %d", action, i, item, want)
			}
		}
		if action == "ADD" {
			if node.Items[0].Name.Parts[1].Text != "<" || node.Items[1].Name.Parts[1].Text != "<=" || !node.Items[0].Signature || len(node.Items[0].Arguments) != 2 || node.Items[1].Purpose != "s" {
				t.Fatalf("operator signatures lost: %#v", node.Items)
			}
		} else if len(node.Items[0].Association) != 1 || len(node.Items[1].Association) != 2 {
			t.Fatalf("DROP associations lost: %#v", node.Items)
		}
	}
	for _, query := range []string{
		`ALTER OPERATOR FAMILY public.f USING btree ADD nonsense 1 pg_catalog.<(integer,integer)`,
		`ALTER OPERATOR FAMILY public.f USING btree ADD "operator" 1 pg_catalog.<(integer,integer)`,
		`ALTER OPERATOR FAMILY public.f USING btree ADD OPERATOR 1 pg_catalog.<(integer,integer),`,
		`ALTER OPERATOR FAMILY public.f USING btree DROP OPERATOR 1(integer) trailing`,
	} {
		if _, err := Parse(query); err == nil {
			t.Fatal("accepted incomplete or invalid member syntax", query)
		}
	}
}

// FAMILY's contextual keyword must not consume IF from DROP OPERATOR IF EXISTS.
func TestOperatorFamilyDropPrefix(t *testing.T) {
	for _, query := range []string{
		`DROP OPERATOR IF EXISTS public.===(integer,integer)`,
		`DROP OPERATOR IF EXISTS if.===(integer,integer) CASCADE`,
		`DROP OPERATOR IF EXISTS family.===(integer,integer)`,
		`DROP OPERATOR public.===(integer,integer)`,
	} {
		statements, err := Parse(query)
		if err != nil || len(statements) != 1 {
			t.Fatal(query, statements, err)
		}
		node, ok := statements[0].(*DropOperator)
		if !ok || len(node.Signatures) != 1 || node.IfExists != strings.Contains(query, "IF EXISTS") {
			t.Fatalf("operator DROP AST for %s: %#v", query, statements[0])
		}
	}
	for _, query := range []string{
		`DROP OPERATOR FAMILY public.f USING btree`,
		`DROP OPERATOR FAMILY IF EXISTS public.f USING btree CASCADE`,
	} {
		statements, err := Parse(query)
		if err != nil || len(statements) != 1 {
			t.Fatal(query, statements, err)
		}
		node, ok := statements[0].(*DropOperatorFamily)
		if !ok || node.Method.Text != "btree" || node.IfExists != strings.Contains(query, "IF EXISTS") {
			t.Fatalf("family DROP AST for %s: %#v", query, statements[0])
		}
	}
}
