package parser

import (
	"errors"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestCastRecursiveSyntaxAndLocations(t *testing.T) {
	statements, err := Parse("SELECT CAST(int8(i)::text AS bool), (1::int4)::int8 FROM t")
	if err != nil {
		t.Fatal(err)
	}
	targets := statements[0].(*Select).Targets
	outer := targets[0].(CastExpr)
	postfix := outer.Child.(CastExpr)
	function := postfix.Child.(CastExpr)
	if outer.Syntax != StandardCast || outer.Pos.Character != 8 || outer.TypeName.Name != "boolean" || postfix.Syntax != PostfixCast || postfix.TypeName.Name != "text" || function.Syntax != FunctionCast || function.Callee != "int8" {
		t.Fatal(outer, postfix, function)
	}
	if _, ok := function.Child.(ColumnExpr); !ok {
		t.Fatal(function.Child)
	}
	chain := targets[1].(CastExpr)
	if chain.TypeName.Name != "bigint" || chain.Child.(CastExpr).TypeName.Name != "integer" {
		t.Fatal(chain)
	}
	for _, q := range []string{
		"INSERT INTO t VALUES(CAST('42' AS integer),bool(NULL))",
		"SELECT i FROM t WHERE i=int4('42')",
		"SELECT i FROM t WHERE CAST(i AS bool)",
		"SELECT i FROM t WHERE i=int4(i)",
		"SELECT i::int8>1,NOT i::bool,(i::bool AND TRUE) OR FALSE FROM t",
		"SELECT num_nulls(NULL::int4),num_nonnulls(int4('42'::text))",
		`SELECT "int4"('42'),CAST(NULL AS "bool") FROM t`,
	} {
		if _, err := Parse(q); err != nil {
			t.Fatal(q, err)
		}
	}
}

func TestCastParserBoundaries(t *testing.T) {
	for _, q := range []string{"INSERT INTO t VALUES(CAST(i AS int4))", "INSERT INTO t VALUES(int4(i))"} {
		_, err := Parse(q)
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "0A000" {
			t.Fatal(q, err)
		}
	}
	for _, q := range []string{"SELECT integer('42') FROM t", "SELECT bigint('42') FROM t", "SELECT boolean('yes') FROM t", "SELECT int('42') FROM t", "SELECT i:: FROM t", "SELECT CAST(i AS) FROM t", "SELECT i::int4 garbage more FROM t"} {
		if _, err := Parse(q); err == nil {
			t.Fatal(q)
		}
	}
	_, err := Parse("SELECT i:int4 FROM t")
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42601" || diagnostic.Position != "9" || diagnostic.Message != `syntax error at or near ":"` {
		t.Fatal(err)
	}
	if _, err := Parse(`SELECT "integer"('42') FROM t`); err != nil {
		t.Fatal(err)
	}
}

func TestCastDefinitionUtilitySyntax(t *testing.T) {
	for _, tc := range []struct {
		suffix, method, context string
		specified               bool
		arity                   int
	}{
		{"WITH FUNCTION s.convert", "f", "e", false, 0},
		{"WITH FUNCTION s.convert()", "f", "e", true, 0},
		{"WITH FUNCTION s.convert(integer,integer,boolean) AS IMPLICIT", "f", "i", true, 3},
		{"WITH INOUT AS ASSIGNMENT", "i", "a", false, 0},
		{"WITHOUT FUNCTION", "b", "e", false, 0},
	} {
		query := `CREATE CAST (s."Source"[] AS integer[]) ` + tc.suffix
		statements, err := Parse(query)
		if err != nil || len(statements) != 1 {
			t.Fatal(query, statements, err)
		}
		cast := statements[0].(*CreateCast)
		if cast.Source.Schema == nil || cast.Source.Schema.Text != "s" || cast.Source.Name != "Source" || !cast.Source.Quoted || len(cast.Source.ArrayBounds) != 1 {
			t.Fatal("source spelling lost", cast)
		}
		if !cast.Target.System || cast.Target.Name != "int4" || len(cast.Target.ArrayBounds) != 1 || cast.Method != tc.method || cast.Context != tc.context {
			t.Fatal("target alias or method/context lost", cast)
		}
		if cast.Function.SignatureSpecified != tc.specified || len(cast.Function.Arguments) != tc.arity {
			t.Fatal("omitted and explicit signatures confused", cast)
		}
		if tc.arity != 0 && (!cast.Function.Arguments[0].Type.System || cast.Function.Arguments[0].Type.Name != "int4" || cast.Function.Arguments[2].Type.Name != "bool") {
			t.Fatal("signature aliases not normalized", cast.Function)
		}
	}
	for _, tc := range []struct {
		query    string
		ifExists bool
		mode     DropMode
	}{
		{`DROP CAST (s."Source" AS integer)`, false, DropRestrict},
		{`DROP CAST IF EXISTS (s."Source" AS integer) RESTRICT`, true, DropRestrict},
		{`DROP CAST IF EXISTS (s."Source" AS integer) CASCADE`, true, DropCascade},
	} {
		statements, err := Parse(tc.query)
		if err != nil || len(statements) != 1 {
			t.Fatal(tc.query, statements, err)
		}
		cast := statements[0].(*DropCast)
		if cast.IfExists != tc.ifExists || cast.Mode != tc.mode || !cast.Target.System || cast.Target.Name != "int4" {
			t.Fatal(cast)
		}
	}
	for _, query := range []string{
		`CREATE CAST (s."Source" AS integer) WITH FUNCTION f(integer,)`,
		`CREATE CAST (s."Source" AS integer) WITH FUNCTION f(integer) AS IMPLICIT trailing`,
		`CREATE CAST (s."Source" AS integer) WITH INOUT; SELECT )`,
		`DROP CAST (s."Source" AS integer) CASCADE trailing`,
	} {
		if statements, err := Parse(query); err == nil || statements != nil {
			t.Fatal("accepted malformed whole CAST input", query, statements, err)
		}
	}
}
