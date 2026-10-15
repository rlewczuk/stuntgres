package parser

import (
	"strings"
	"testing"
)

func TestSQLJSONPositionedAST(t *testing.T) {
	statements, err := Parse(`/*é😀*/ SELECT JSON_OBJECT('a':JSON_ARRAY($1::text FORMAT JSON NULL ON NULL) ABSENT ON NULL WITH UNIQUE KEYS RETURNING text FORMAT JSON)`)
	if err != nil {
		t.Fatal(err)
	}
	raw, ok := statements[0].(*Select).Targets[0].(SQLJSONExpr)
	if !ok || raw.Kind != "json_object" || !raw.Absent || !raw.Unique || len(raw.Args) != 2 || raw.Returning == nil || raw.Output == nil || raw.Pos.Character != 15 {
		t.Fatalf("constructor=%#v", raw)
	}
	array, ok := raw.Args[1].(SQLJSONExpr)
	if !ok || array.Kind != "json_array" || array.Absent || len(array.Formats) != 1 || array.Formats[0] == nil {
		t.Fatalf("nested array=%#v", raw.Args[1])
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
}
func TestSQLJSONQueryAndPredicateTraversal(t *testing.T) {
	statements, err := Parse(`SELECT JSON_ARRAY(WITH q AS (VALUES ($1::text)) SELECT column1 FROM q FORMAT JSON RETURNING jsonb),$2::text IS NOT JSON OBJECT WITH UNIQUE KEYS`)
	if err != nil {
		t.Fatal(err)
	}
	query := statements[0].(*Select)
	array, ok := query.Targets[0].(SQLJSONExpr)
	if !ok || array.Query == nil || !array.Absent || array.Returning == nil || len(array.Formats) != 1 || array.Formats[0] == nil {
		t.Fatal(array)
	}
	predicate, ok := query.Targets[1].(JSONPredicateExpr)
	if !ok || predicate.Kind != "object" || !predicate.Not || !predicate.Unique {
		t.Fatal(predicate)
	}
	seen := map[int]bool{}
	if err := VisitQueryExpressions(query, func(raw Expr) error {
		if p, ok := raw.(ParameterExpr); ok {
			seen[p.Number] = true
		}
		return nil
	}); err != nil || len(seen) != 2 {
		t.Fatal(seen, err)
	}
	for _, source := range []string{`SELECT JSON_SCALAR(1 RETURNING text)`, `SELECT JSON_ARRAY(1 WITH UNIQUE KEYS)`, `SELECT JSON_ARRAY(SELECT 1 NULL ON NULL)`, `SELECT JSON('null','null')`, `SELECT JSON_ARRAY(1) nonsense extra`} {
		if _, err := Parse(source); err == nil {
			t.Fatal("accepted invalid whole input", source)
		}
	}
}

func TestSQLJSONSharedObjectAndPredicateAST(t *testing.T) {
	for _, source := range []string{
		`SELECT JSON_OBJECT((1 + 1) VALUE TRUE)`,
		`SELECT JSON_OBJECT(($1::text) VALUE $2 FORMAT JSON)`,
	} {
		statements, err := Parse(source)
		if err != nil {
			t.Fatal(source, err)
		}
		raw, ok := statements[0].(*Select).Targets[0].(SQLJSONExpr)
		if !ok || !raw.Members || len(raw.Args) != 2 {
			t.Fatalf("member AST %#v", raw)
		}
	}
	statements, err := Parse(`SELECT JSON_OBJECT($1,$2),(v IS JSON WITH UNIQUE KEYS)::text FROM t`)
	if err != nil {
		t.Fatal(err)
	}
	targets := statements[0].(*Select).Targets
	call, ok := targets[0].(FunctionExpr)
	if !ok || len(call.Args) != 2 {
		t.Fatalf("legacy call AST %#v", targets[0])
	}
	cast, ok := targets[1].(CastExpr)
	if !ok {
		t.Fatalf("predicate cast AST %#v", targets[1])
	}
	predicate, ok := cast.Child.(JSONPredicateExpr)
	if !ok || !predicate.Unique {
		t.Fatalf("predicate AST %#v", cast.Child)
	}
	statements, err = Parse(`SELECT JSON_ARRAY(SELECT 1 NULL)`)
	if err != nil {
		t.Fatal(err)
	}
	array := statements[0].(*Select).Targets[0].(SQLJSONExpr)
	if array.Query == nil || len(array.Query.Labels) != 1 || array.Query.Labels[0].Text != "null" {
		t.Fatalf("query alias AST %#v", array.Query)
	}
}

func TestSQLJSONGroupedPrimaryPostfixAST(t *testing.T) {
	for _, source := range []string{
		`SELECT (JSON_SCALAR(v))::text`,
		`SELECT (1 + 1)::text`,
		`SELECT (f(v))::text`,
		`SELECT (v IS JSON WITH UNIQUE KEYS)::text`,
	} {
		statements, err := Parse(source)
		if err != nil {
			t.Fatal(source, err)
		}
		cast, ok := statements[0].(*Select).Targets[0].(CastExpr)
		if !ok || cast.TypeName.Name != "text" || cast.Syntax != PostfixCast {
			t.Fatalf("%s: cast AST %#v", source, statements[0].(*Select).Targets[0])
		}
	}
	const source = `SELECT (v IS JSON)::text COLLATE "C",(ARRAY[1,2])[1],(ROW(1,2)).f1`
	statements, err := Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	targets := statements[0].(*Select).Targets
	collate, ok := targets[0].(CollateExpr)
	if !ok {
		t.Fatalf("collation AST %#v", targets[0])
	}
	if _, ok := collate.Child.(CastExpr); !ok {
		t.Fatalf("collation child %#v", collate.Child)
	}
	subscript, ok := targets[1].(SubscriptExpr)
	if !ok || len(subscript.Subscripts) != 1 || subscript.Pos.Offset != strings.Index(source, "(ARRAY") {
		t.Fatalf("subscript AST %#v", targets[1])
	}
	field, ok := targets[2].(FieldExpr)
	if !ok || field.Field.Text != "f1" || field.OpenPos.Offset != strings.Index(source, "(ROW") {
		t.Fatalf("field AST %#v", targets[2])
	}
}

func TestSQLJSONValueKeyPrimaryBoundary(t *testing.T) {
	for _, source := range []string{
		`SELECT JSON_OBJECT((1 + 1) VALUE TRUE)`,
		`SELECT JSON_OBJECT(($1::text) VALUE $1::text)`,
		`SELECT JSON_OBJECT((v IS JSON) VALUE TRUE)`,
		`SELECT JSON_OBJECT((f(v)) VALUE TRUE)`,
		`SELECT JSON_OBJECT((ARRAY['a'])[1] VALUE TRUE)`,
		`SELECT JSON_OBJECT((ROW('a')).f1 VALUE TRUE)`,
		`SELECT JSON_OBJECTAGG((1 + 1) VALUE TRUE)`,
	} {
		statements, err := Parse(source)
		if err != nil {
			t.Fatal(source, err)
		}
		switch raw := statements[0].(*Select).Targets[0].(type) {
		case SQLJSONExpr:
			if !raw.Members || len(raw.Args) != 2 {
				t.Fatalf("%s: member AST %#v", source, raw)
			}
		case AggregateExpr:
			if raw.Name != "json_objectagg" || len(raw.Args) != 2 {
				t.Fatalf("%s: aggregate AST %#v", source, raw)
			}
		default:
			t.Fatalf("%s: unexpected AST %#v", source, raw)
		}
	}
	for _, source := range []string{
		`SELECT JSON_OBJECT('a'::text VALUE TRUE)`,
		`SELECT JSON_OBJECT(1 + 1 VALUE TRUE)`,
		`SELECT JSON_OBJECT('a' COLLATE "C" VALUE TRUE)`,
		`SELECT JSON_OBJECTAGG('a'::text VALUE TRUE)`,
	} {
		if _, err := Parse(source); err == nil {
			t.Fatal("accepted unparenthesized VALUE key postfix", source)
		}
	}
}

func TestSQLJSONObjectClauseOwners(t *testing.T) {
	for _, source := range []string{
		`SELECT JSON_OBJECT(ARRAY['a','1'])`,
		`SELECT JSON_OBJECT(ARRAY['a'],ARRAY['1'])`,
		`SELECT JSON_OBJECT(VARIADIC ARRAY['a','1'])`,
		`SELECT JSON_OBJECT(ARRAY['a'],VARIADIC ARRAY['1'])`,
	} {
		statements, err := Parse(source)
		if err != nil {
			t.Fatal(source, err)
		}
		if _, ok := statements[0].(*Select).Targets[0].(FunctionExpr); !ok {
			t.Fatalf("legacy owner lost: %#v", statements[0].(*Select).Targets[0])
		}
	}
	for _, source := range []string{
		`SELECT JSON_OBJECT(RETURNING bytea FORMAT JSON ENCODING UTF8)`,
		`SELECT JSON_OBJECT('a':1 ABSENT ON NULL WITH UNIQUE KEYS RETURNING text FORMAT JSON)`,
	} {
		statements, err := Parse(source)
		if err != nil {
			t.Fatal(source, err)
		}
		raw, ok := statements[0].(*Select).Targets[0].(SQLJSONExpr)
		if !ok || raw.Returning == nil || raw.Output == nil {
			t.Fatalf("written clauses lost: %#v", statements[0].(*Select).Targets[0])
		}
		if len(raw.Args) > 0 && (!raw.Members || !raw.Absent || !raw.Unique || raw.NullPos.Character == 0) {
			t.Fatalf("member clauses lost: %#v", raw)
		}
	}
	for _, source := range []string{
		`SELECT JSON_OBJECT(ARRAY['a','1'] FORMAT JSON)`,
		`SELECT JSON_OBJECT(ARRAY['a','1'] RETURNING text)`,
		`SELECT JSON_OBJECT(ARRAY['a','1'] WITH UNIQUE KEYS)`,
		`SELECT JSON_OBJECT(ARRAY['a','1'] ABSENT ON NULL)`,
		`SELECT JSON_OBJECT(NULL ON NULL)`,
		`SELECT JSON_OBJECT(ABSENT ON NULL)`,
		`SELECT JSON_OBJECT(WITH UNIQUE KEYS)`,
		`SELECT JSON_OBJECT(WITHOUT UNIQUE KEYS)`,
	} {
		if _, err := Parse(source); err == nil {
			t.Fatal("accepted clauses without a member owner", source)
		}
	}
}
