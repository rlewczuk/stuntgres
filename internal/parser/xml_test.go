package parser

import "testing"

func TestXMLPositionedGrammar(t *testing.T) {
	source := `/*é*/ SELECT XMLSERIALIZE(DOCUMENT XMLPARSE(CONTENT $1 STRIP WHITESPACE) AS varchar(20) NO INDENT)`
	statements, err := Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	serialize, ok := statements[0].(*Select).Targets[0].(XMLSerializeExpr)
	if !ok || !serialize.Document || serialize.Indent != "no indent" || serialize.Target.Name != "varchar" || serialize.Pos.Character != 14 {
		t.Fatalf("serialize %#v", serialize)
	}
	parse, ok := serialize.Child.(XMLParseExpr)
	if !ok || parse.Document || parse.Whitespace != "strip" {
		t.Fatalf("parse %#v", serialize.Child)
	}
	parameter, ok := parse.Child.(ParameterExpr)
	if !ok || parameter.Number != 1 {
		t.Fatal(parse.Child)
	}
	seen := 0
	if err := VisitQueryExpressions(statements[0], func(raw Expr) error {
		if _, ok := raw.(ParameterExpr); ok {
			seen++
		}
		return nil
	}); err != nil || seen != 1 {
		t.Fatal(seen, err)
	}
	if ExpressionStart(serialize) != serialize.Pos || ExpressionStart(parse) != parse.Pos {
		t.Fatal("XML opener provenance lost")
	}
	for _, sql := range []string{
		`SELECT XMLPARSE(CONTENT '<a/>' PRESERVE WHITESPACE)`,
		`SELECT XMLPARSE(CONTENT indent) FROM t`,
		`SELECT XMLPARSE(CONTENT xmlparse) FROM t`,
		`SELECT xmlserialize,xmlparse,document,content,indent FROM t`,
		`SELECT XMLSERIALIZE(CONTENT (SELECT $2::xml FROM source) AS pg_catalog.text INDENT)`,
	} {
		if _, err := Parse(sql); err != nil {
			t.Fatal(sql, err)
		}
	}
	for _, sql := range []string{
		`SELECT XMLPARSE(CONTENT '<a/>' NO PRESERVE WHITESPACE)`,
		`SELECT XMLPARSE('<a/>')`,
		`SELECT XMLSERIALIZE(CONTENT '<a/>' AS text[])`,
		`SELECT XMLSERIALIZE(CONTENT '<a/>' AS text) extra junk`,
	} {
		if _, err := Parse(sql); err == nil {
			t.Fatal("accepted invalid complete input", sql)
		}
	}
}

func TestXMLSourceAndAdmissionTraversal(t *testing.T) {
	statements, err := Parse(`SELECT XMLSERIALIZE(CONTENT (SELECT XMLPARSE(CONTENT $1) FROM xml_source) AS text)`)
	if err != nil {
		t.Fatal(err)
	}
	tables, queries := 0, 0
	if err := VisitQuerySources(statements[0], func(*Select) error { queries++; return nil }, func(table *TableReference, _ *Select) error { tables++; return nil }); err != nil || tables != 1 || queries != 2 {
		t.Fatal(tables, queries, err)
	}
	pos := Position{Offset: 17, Character: 18}
	nested := XMLParseExpr{Child: XMLSerializeExpr{Child: FunctionExpr{Name: ObjectName{Parts: []Identifier{{Text: "pg_index_has_property", Pos: pos}}}, Pos: pos}}}
	if got, found := firstDefaultIndexProperty(nested); !found || got != pos {
		t.Fatal("hidden DEFAULT guard", got, found)
	}
}
