package parser

import "testing"

func TestStringCoreParser(t *testing.T) {
	for _, query := range []string{
		"SELECT btrim(' x ')", "SELECT ltrim('xxa','x')||rtrim('azz','z')",
		"SELECT trim(' x ')", "SELECT trim(BOTH 'x' FROM 'xxaxx')", "SELECT trim(LEADING FROM ' a ')", "SELECT trim(TRAILING FROM ' a ')",
		"SELECT trim(FROM 'x','y')", "SELECT trim(BOTH 'x' FROM 'a','b')",
		"SELECT normalize('é')", "SELECT normalize('ﬃ',NFKC)", "SELECT pg_catalog.normalize($1,$2)", "SELECT \"normalize\"('é','NFC')",
		"SELECT 'é' IS NFC NORMALIZED", "SELECT 'é' IS NOT NFD NORMALIZED", "SELECT btrim('x') IS NORMALIZED",
		"SELECT 'x'::text||42", "SELECT 'x'||setseed(0)", "SELECT setseed(0)||'x'", "SELECT 'x'||round(1.5)",
		"SELECT btrim(CASE WHEN true THEN ' x ' ELSE 'y' END)", "SELECT btrim(coalesce(NULL,' x '))",
		"SELECT pg_catalog.btrim(' x ') OPERATOR(pg_catalog.||) 'y'",
	} {
		statements, err := Parse(query)
		if err != nil || len(statements) != 1 {
			t.Fatal(query, err)
		}
		selected, ok := statements[0].(*Select)
		if !ok || !selected.IsSingleton() {
			t.Fatal("string singleton rejected", query)
		}
	}
	for _, query := range []string{"SELECT 1", "SELECT true", "SELECT 1+1", "SELECT round(1+1)"} {
		statements, err := Parse(query)
		if err == nil && len(statements) == 1 {
			if selected, ok := statements[0].(*Select); ok && selected.IsSingleton() {
				t.Fatal("unrelated singleton widened", query)
			}
		}
	}
	for _, query := range []string{"SELECT normalize('x','NFC')", "SELECT normalize('x',$1)", "SELECT normalize('x',BAD)", "SELECT 'x' IS 'NFC' NORMALIZED"} {
		if _, err := Parse(query); err == nil {
			t.Fatal("dedicated form accepted non-keyword", query)
		}
	}
	for _, query := range []string{`CREATE TABLE s(normalize TEXT,trim TEXT,nfc TEXT,"leading" TEXT)`, `SELECT normalize,trim,nfc,"leading" FROM s`, "SELECT btrim(v)||v FROM s", "SELECT v IS NFKD NORMALIZED FROM s"} {
		if _, err := Parse(query); err != nil {
			t.Fatal("scalar/identifier composition", query, err)
		}
	}
}

func TestStringCoreParserLowering(t *testing.T) {
	statements, err := Parse("SELECT trim(LEADING 'xy' FROM 'xyabc'), 'é' IS NOT NFD NORMALIZED")
	if err != nil {
		t.Fatal(err)
	}
	selected := statements[0].(*Select)
	trim, ok := selected.Targets[0].(FunctionExpr)
	if !ok || trim.StringSyntax != TrimStringSyntax || trim.Name.Parts[0].Text != "pg_catalog" || trim.Name.Parts[1].Text != "ltrim" || len(trim.Args) != 2 {
		t.Fatal(trim)
	}
	source := trim.Args[0].(LiteralExpr)
	set := trim.Args[1].(LiteralExpr)
	if source.Value.Text != "xyabc" || set.Value.Text != "xy" {
		t.Fatal("trim argument order", trim)
	}
	negation, ok := selected.Targets[1].(LogicalExpr)
	if !ok || negation.Op != LogicalNot {
		t.Fatal(selected.Targets[1])
	}
	predicate := negation.Left.(FunctionExpr)
	if predicate.StringSyntax != NormalizedStringSyntax || predicate.Name.Parts[1].Text != "is_normalized" || predicate.Args[1].(LiteralExpr).Value.Text != "NFD" {
		t.Fatal(predicate)
	}
}

func TestStringCoreParserSyntaxProvenance(t *testing.T) {
	for _, tc := range []struct {
		query  string
		syntax StringSyntax
		count  int
	}{
		{"SELECT normalize('x')", NormalizeStringSyntax, 1},
		{"SELECT normalize('x',NFC)", NormalizeStringSyntax, 2},
		{"SELECT pg_catalog.normalize('x','NFC')", OrdinaryStringSyntax, 2},
		{`SELECT "normalize"('x')`, OrdinaryStringSyntax, 1},
		{"SELECT 'x' IS NORMALIZED", NormalizedStringSyntax, 1},
		{"SELECT 'x' IS NFC NORMALIZED", NormalizedStringSyntax, 2},
		{"SELECT trim('x')", TrimStringSyntax, 1},
		{"SELECT btrim('x')", OrdinaryStringSyntax, 1},
	} {
		t.Run(tc.query, func(t *testing.T) {
			statements, err := Parse(tc.query)
			if err != nil {
				t.Fatal(err)
			}
			call, ok := statements[0].(*Select).Targets[0].(FunctionExpr)
			if !ok || call.StringSyntax != tc.syntax || len(call.Args) != tc.count {
				t.Fatal("lost string syntax or omitted-form provenance", call)
			}
		})
	}
}
