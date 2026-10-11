package parser

import "testing"

func TestMathTrigAdmission(t *testing.T) {
	for _, query := range []string{
		"SELECT acos(-1),acosd('-0.5'),asin(1),asind('0.5'),atan(1),atand(1),atan2(1,-1),atan2d(1,-1)",
		"SELECT x,asind(x),atan2d(y,x) FROM t",
		"SELECT atan2(NULL,asin(d)) FROM t",
		`SELECT pg_catalog.asind('0.5'),ASIND('0.5'),"asind"('0.5'),"ASIND"('0.5')`,
		"SELECT acos,acosd,asin,asind,atan,atand,atan2,atan2d FROM t",
		"SELECT asind(asind),atan2d(atan2d,atan2) FROM t",
		"INSERT INTO t SELECT id,asind(d) FROM source ORDER BY id",
		"UPDATE t SET d=acosd(d)",
		"CREATE VIEW v AS SELECT asind(d),atan2d(y,x) FROM t",
		"SELECT acos(x => 0)", "SELECT atan2(y => 1,x => 1)", "SELECT acos(VARIADIC ARRAY[1])",
		"SELECT sum(asind(d)),count(atan2d(d,1)) FROM t",
		"SELECT cos(0),cosd(60),cot('0.5'),cotd(45),sin(1),sind(30),tan(1),tand(45)",
		"SELECT cos,cosd,cot,cotd,sin,sind,tan,tand FROM t",
		"SELECT sind(sind),cosd(cosd),atan2(NULL,sin(d)) FROM t",
		`SELECT pg_catalog.sind(30),SIND(30),"sind"(30),"SIND"(30)`,
		"INSERT INTO t SELECT id,sind(d) FROM source ORDER BY id",
		"UPDATE t SET d=cosd(d)",
		"CREATE VIEW v AS SELECT cos(d),cosd(d),cot(d),cotd(d),sin(d),sind(d),tan(d),tand(d) FROM t",
		"SELECT sind(x => 30)", "SELECT sin(VARIADIC ARRAY[1])",
		"SELECT sum(sind(d)),count(cosd(d)) FROM t",
	} {
		statements, err := Parse(query)
		if err != nil || len(statements) != 1 {
			t.Fatal(query, statements, err)
		}
	}
}

func TestMathTrigProvenance(t *testing.T) {
	statements, err := Parse("/*é*/ SELECT atan2d(y => $1,x => $2)")
	if err != nil || len(statements) != 1 {
		t.Fatal(statements, err)
	}
	call, ok := statements[0].(*Select).Targets[0].(FunctionExpr)
	if !ok || call.Pos.Character != 14 || call.Pos.Offset != 14 || len(call.Args) != 2 || len(call.ArgNames) != 2 || call.ArgNames[0].Text != "y" || call.ArgNames[1].Text != "x" {
		t.Fatal("function/character/named provenance", call)
	}
	for i, arg := range call.Args {
		parameter, ok := arg.(ParameterExpr)
		if !ok || parameter.Number != i+1 {
			t.Fatal("parameter order", call.Args)
		}
	}
	statements, err = Parse(`SELECT pg_catalog."ATAN2D"(1,1)`)
	if err != nil {
		t.Fatal(err)
	}
	call, ok = statements[0].(*Select).Targets[0].(FunctionExpr)
	if !ok || len(call.Name.Parts) != 2 || call.Name.Parts[0].Text != "pg_catalog" || call.Name.Parts[1].Text != "ATAN2D" {
		t.Fatal("quoted spelling", call)
	}
	statements, err = Parse("SELECT acos(VARIADIC ARRAY[1])")
	if err != nil {
		t.Fatal(err)
	}
	call, ok = statements[0].(*Select).Targets[0].(FunctionExpr)
	if !ok || !call.Variadic || len(call.Args) != 1 {
		t.Fatal("variadic provenance", call)
	}
	for _, query := range []string{"/*é*/ SELECT sind(x => $1)", `/*é*/ SELECT pg_catalog."SIND"($1)`} {
		statements, err := Parse(query)
		if err != nil || len(statements) != 1 {
			t.Fatal(query, statements, err)
		}
		call, ok := statements[0].(*Select).Targets[0].(FunctionExpr)
		if !ok || call.Pos.Character != 14 || call.Pos.Offset != 14 || len(call.Args) != 1 {
			t.Fatal("direct character provenance", call)
		}
		parameter, ok := call.Args[0].(ParameterExpr)
		if !ok || parameter.Number != 1 {
			t.Fatal("direct parameter provenance", call)
		}
		if len(call.Name.Parts) == 1 {
			if len(call.ArgNames) != 1 || call.ArgNames[0].Text != "x" {
				t.Fatal("direct named provenance", call)
			}
		} else if len(call.Name.Parts) != 2 || call.Name.Parts[0].Text != "pg_catalog" || call.Name.Parts[1].Text != "SIND" {
			t.Fatal("direct quoted spelling", call)
		}
	}
}

func TestMathTrigBoundaries(t *testing.T) {
	for _, query := range []string{
		"SELECT n FROM t WHERE asind(n)", "INSERT INTO t VALUES(asind(1))",
		"CREATE TABLE t(n DOUBLE PRECISION DEFAULT asind(1))",
		"SELECT atan2d(1,)", "SELECT asind(1) garbage garbage",
		"INSERT INTO t VALUES(1); SELECT atan2d(1,)",
		"SELECT n FROM t WHERE sind(n)", "INSERT INTO t VALUES(sind(30))",
		"CREATE TABLE t(n DOUBLE PRECISION DEFAULT sind(30))",
		"SELECT sind(30,)", "SELECT sind(30) garbage garbage",
		"INSERT INTO t VALUES(1); SELECT sind(30,)",
	} {
		statements, err := Parse(query)
		if err == nil || len(statements) != 0 {
			t.Fatal("widened/partial grammar", query, statements, err)
		}
	}
}
