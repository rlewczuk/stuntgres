package parser

import (
	"testing"
)

func TestXMLPredicateGrammar(t *testing.T) {
	for _, before := range []string{"", " BY REF", " BY VALUE"} {
		for _, after := range []string{"", " BY REF", " BY VALUE"} {
			source := `/*é*/ SELECT XMLEXISTS($1 PASSING` + before + ` $2` + after + `) IS NOT NULL`
			statements, err := Parse(source)
			if err != nil {
				t.Fatal(source, err)
			}
			predicate := statements[0].(*Select).Targets[0].(PredicateExpr)
			exists, ok := predicate.Left.(FunctionExpr)
			if !ok || exists.Pos.Character != 14 || exists.Syntax != OrdinaryFunctionSyntax || len(exists.Name.Parts) != 2 || exists.Name.Parts[0].Text != "pg_catalog" || exists.Name.Parts[1].Text != "xmlexists" || len(exists.Args) != 2 {
				t.Fatal(source, exists)
			}
			parameters := []int{}
			if err := VisitQueryExpressions(statements[0], func(e Expr) error {
				if p, ok := e.(ParameterExpr); ok {
					parameters = append(parameters, p.Number)
				}
				return nil
			}); err != nil || len(parameters) != 2 || parameters[0] != 1 || parameters[1] != 2 {
				t.Fatal(parameters, err)
			}
		}
	}
	statements, err := Parse(`SELECT NOT NULL::xml IS DOCUMENT OR NULL::xml IS NOT DOCUMENT`)
	if err != nil {
		t.Fatal(err)
	}
	logical := statements[0].(*Select).Targets[0].(LogicalExpr)
	if logical.Op != LogicalOr {
		t.Fatal(logical)
	}
	negation, ok := logical.Left.(LogicalExpr)
	if !ok || negation.Op != LogicalNot {
		t.Fatal(logical)
	}
	if document, ok := negation.Left.(XMLDocumentPredicateExpr); !ok || document.Negated {
		t.Fatal(negation)
	}
	document := negation.Left.(XMLDocumentPredicateExpr)
	if document.Pos == ExpressionStart(document) || ExpressionStart(document) != ExpressionStart(document.Child) {
		t.Fatal("predicate opener replaced written left edge", document)
	}
	for _, q := range []string{`SELECT document,passing,value,ref FROM t`, `SELECT NOT document FROM t`, `SELECT XMLEXISTS(passing PASSING document) FROM t`, `SELECT "xmlexists"('/r','<r/>'::xml)`, `SELECT pg_catalog.xmlexists('/r','<r/>'::xml)`} {
		if _, err := Parse(q); err != nil {
			t.Fatal(q, err)
		}
	}
	for _, q := range []string{
		`SELECT XMLEXISTS(('/r'::text) PASSING ('<r/>'::xml))`,
		`SELECT XMLEXISTS(CAST('/r' AS text) PASSING CAST('<r/>' AS xml))`,
		`SELECT XMLEXISTS(('/' || 'r') PASSING XMLPARSE(CONTENT '<r/>'))`,
		`SELECT XMLEXISTS('/r' PASSING XMLPARSE(CONTENT '<r/>'))`,
		`SELECT XMLEXISTS(XMLSERIALIZE(CONTENT XMLPARSE(CONTENT '/r') AS text) PASSING XMLPARSE(CONTENT '<r/>'))`,
		`SELECT XMLEXISTS(JSON_SERIALIZE('"/r"' RETURNING text) PASSING XMLPARSE(CONTENT '<r/>'))`,
		`SELECT XMLEXISTS(JSON_VALUE('{"p":"/r"}','$.p' RETURNING text) PASSING XMLPARSE(CONTENT '<r/>'))`,
		`SELECT XMLEXISTS(GROUPING(x) PASSING XMLPARSE(CONTENT '<r/>')) FROM t GROUP BY x`,
	} {
		if _, err := Parse(q); err != nil {
			t.Fatal("valid c-expression operand rejected", q, err)
		}
	}
	for _, q := range []string{
		`SELECT XMLEXISTS('/r'::text PASSING XMLPARSE(CONTENT '<r/>'))`,
		`SELECT XMLEXISTS('/r' PASSING '<r/>'::xml)`,
		`SELECT XMLEXISTS('/' || 'r' PASSING XMLPARSE(CONTENT '<r/>'))`,
	} {
		if _, err := Parse(q); err == nil {
			t.Fatal("unparenthesized non-c-expression admitted", q)
		}
	}
	for _, q := range []string{`SELECT XMLEXISTS('/r' '<r/>')`, `SELECT XMLEXISTS('/r' PASSING BY '<r/>')`, `SELECT XMLEXISTS('/r' PASSING '<r/>' BY)`, `SELECT XMLEXISTS('/r' PASSING '<r/>','x')`, `SELECT NULL::xml IS NOT DOCUMENT x junk`} {
		if _, err := Parse(q); err == nil {
			t.Fatal("invalid full input admitted", q)
		}
	}
}
func TestXMLPredicateTraversal(t *testing.T) {
	statements, err := Parse(`SELECT XMLEXISTS((SELECT p FROM path_source) PASSING (SELECT x FROM xml_source)) IS DOCUMENT`)
	if err != nil {
		t.Fatal(err)
	}
	tables, queries := 0, 0
	if err := VisitQuerySources(statements[0], func(*Select) error { queries++; return nil }, func(*TableReference, *Select) error { tables++; return nil }); err != nil || tables != 2 || queries != 3 {
		t.Fatal(tables, queries, err)
	}
	pos := Position{Offset: 17, Character: 18}
	hidden := FunctionExpr{Name: ObjectName{Parts: []Identifier{{Text: "pg_index_has_property", Pos: pos}}}, Pos: pos}
	for _, raw := range []Expr{XMLDocumentPredicateExpr{Child: hidden}, FunctionExpr{Args: []Expr{hidden, LiteralExpr{Value: Value{Kind: NullValue}}}}, FunctionExpr{Args: []Expr{LiteralExpr{Value: Value{Kind: NullValue}}, hidden}}} {
		if got, found := firstDefaultIndexProperty(raw); !found || got != pos {
			t.Fatal(raw, got, found)
		}
	}
}
