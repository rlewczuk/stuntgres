package parser

import (
	"testing"
)

func TestCompareNullParserKindsAndPositions(t *testing.T) {
	for _, tc := range []struct {
		suffix string
		kind   PredicateKind
	}{
		{"IS NULL", IsNull}, {"IS NOT NULL", IsNotNull}, {"ISNULL", IsNull}, {"NOTNULL", IsNotNull},
		{"IS TRUE", IsTrue}, {"IS NOT TRUE", IsNotTrue}, {"IS FALSE", IsFalse}, {"IS NOT FALSE", IsNotFalse},
		{"IS UNKNOWN", IsUnknown}, {"IS NOT UNKNOWN", IsNotUnknown}, {"IS DISTINCT FROM $1", IsDistinct}, {"IS NOT DISTINCT FROM $1", IsNotDistinct},
	} {
		statements, err := Parse("SELECT /*é*/ v " + tc.suffix + " FROM t WHERE v " + tc.suffix)
		if err != nil {
			t.Fatal(tc.suffix, err)
		}
		s := statements[0].(*Select)
		for i, node := range []Expr{s.Targets[0], s.Where} {
			p, ok := node.(PredicateExpr)
			if !ok || p.Kind != tc.kind {
				t.Fatalf("%s: %#v", tc.suffix, node)
			}
			if i == 0 && (p.Pos.Character != 16 || p.Left.SourcePosition().Character != 14) {
				t.Fatal(p)
			}
			if tc.kind >= IsDistinct {
				if _, ok := p.Right.(ParameterExpr); !ok {
					t.Fatal(p)
				}
			}
		}
	}
}

func TestCompareNullParserGroupingAndBoundaries(t *testing.T) {
	for _, q := range []string{
		"SELECT (v IS NULL) IS TRUE FROM t",
		"SELECT TRUE IS DISTINCT FROM (v IS NULL) FROM t",
		"SELECT (v IS NULL) IS DISTINCT FROM (f IS UNKNOWN) FROM t",
		"SELECT (NULL) IS DISTINCT FROM v FROM t",
		"SELECT (unknown) IS NULL FROM t",
		"SELECT id FROM t WHERE v=1 IS TRUE",
		"SELECT id FROM t WHERE (v=NULL) IS UNKNOWN",
		"SELECT TRUE IS NULL IS TRUE FROM t",
		"SELECT v IS NULL ISNULL FROM t",
		"SELECT v=1 FROM t", "SELECT (v=1) IS TRUE FROM t", "SELECT v FROM t WHERE 1=v",
		"SELECT v FROM t WHERE v=v", "SELECT v FROM t WHERE $1=v",
		"SELECT v IS NULL FROM t WHERE v IS NULL AND TRUE", "SELECT NOT v IS NULL FROM t",
		"SELECT v<2 IS TRUE OR NOT f IS UNKNOWN AND v IS NOT NULL FROM t",
		"SELECT (f AND TRUE) IS UNKNOWN FROM t",
	} {
		if _, err := Parse(q); err != nil {
			t.Fatal(q, err)
		}
	}
	statements, err := Parse("SELECT id FROM t WHERE v=1 IS TRUE")
	if err != nil {
		t.Fatal(err)
	}
	predicate := statements[0].(*Select).Where.(PredicateExpr)
	if _, ok := predicate.Left.(OperatorExpr); !ok {
		t.Fatal("equality must bind more tightly", predicate)
	}
	statements, err = Parse("SELECT TRUE IS NULL IS TRUE FROM t")
	if err != nil {
		t.Fatal(err)
	}
	outer := statements[0].(*Select).Targets[0].(PredicateExpr)
	inner, ok := outer.Left.(PredicateExpr)
	if !ok || outer.Kind != IsTrue || inner.Kind != IsNull {
		t.Fatal("unary postfix chains must nest left", outer)
	}
	statements, err = Parse("SELECT v<2 IS TRUE OR NOT f IS UNKNOWN AND v IS NOT NULL FROM t")
	if err != nil {
		t.Fatal(err)
	}
	logical := statements[0].(*Select).Targets[0].(LogicalExpr)
	if logical.Op != LogicalOr {
		t.Fatal("OR must bind below predicates and AND", logical)
	}
	comparisonTest := logical.Left.(PredicateExpr)
	if comparisonTest.Kind != IsTrue {
		t.Fatal(comparisonTest)
	}
	if _, ok := comparisonTest.Left.(OperatorExpr); !ok {
		t.Fatal("comparison must bind above IS", comparisonTest)
	}
	conjunction := logical.Right.(LogicalExpr)
	negation := conjunction.Left.(LogicalExpr)
	if conjunction.Op != LogicalAnd || negation.Op != LogicalNot || negation.Left.(PredicateExpr).Kind != IsUnknown {
		t.Fatal("IS must bind above NOT, then AND", conjunction)
	}
	keywordDiagnostic(t, "SELECT 1 IS DISTINCT FROM 1 IS TRUE FROM t", "42601", `syntax error at or near "IS"`, "29")
	for _, q := range []string{
		"SELECT v IS NULL", "SELECT v IS NULL AS result FROM t", "SELECT v IS NULL result FROM t",
		"SELECT abs(v) IS NULL FROM t", "SELECT v::integer IS NULL FROM t", "SELECT t.v IS NULL FROM t",
		"SELECT v IS DISTINCT FROM 1 IS NULL FROM t",
		"INSERT INTO t VALUES(1 IS NULL)", "SELECT set_config('x',TRUE IS NULL,FALSE)",
		"SELECT v IS OF (integer) FROM t",
		"SELECT v=1=1 FROM t", "SELECT (v IS NULL)=TRUE FROM t",
		"SELECT num_nulls(v) IS NULL FROM t", "SELECT num_nulls(v IS NULL) FROM t",
		"SELECT (num_nulls(v)) IS NULL FROM t", "SELECT num_nulls((v IS NULL)) FROM t",
		"SELECT num_nulls(v)=1 FROM t", "SELECT 1=num_nulls(v) FROM t",
		"SELECT num_nulls(v) AND TRUE FROM t", "SELECT TRUE OR num_nulls(v) FROM t",
		"SELECT id FROM t WHERE num_nulls(v) IS NULL",
	} {
		if statements, err := Parse(q); err == nil || len(statements) != 0 {
			t.Fatal("admitted excluded syntax", q, statements, err)
		}
	}
	keywordDiagnostic(t, "INSERT INTO t SELECT v IS NULL FROM t", "0A000", "INSERT SELECT is unsupported", "15")
	keywordDiagnostic(t, "INSERT INTO t SELECT v IS NULL FROM t VALUES (1)", "42601", `syntax error at or near "VALUES"`, "39")
	keywordDiagnostic(t, "INSERT INTO t SELECT num_nulls(v) FROM t", "0A000", "INSERT SELECT is unsupported", "15")
	keywordDiagnostic(t, "INSERT INTO t SELECT num_nulls(v) FROM t VALUES (1)", "42601", `syntax error at or near "VALUES"`, "42")
	for _, q := range []string{"SELECT isnull(1) FROM t", "SELECT notnull(1) FROM t"} {
		keywordDiagnostic(t, q, "0A000", "functions are unsupported", "8")
	}
}
