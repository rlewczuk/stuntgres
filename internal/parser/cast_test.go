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
