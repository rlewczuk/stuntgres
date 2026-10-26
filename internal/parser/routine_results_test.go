package parser

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/internal/sqlstate"
)

func TestRoutineResultDeclarations(t *testing.T) {
	for _, tc := range []struct {
		declaration           string
		specified, set, table bool
		outputs               int
	}{
		{"(IN x integer,OUT a integer,OUT b text)", false, false, false, 0},
		{"(IN x integer,OUT a integer,OUT b text) RETURNS record", true, false, false, 0},
		{"(integer) RETURNS SETOF integer", true, true, false, 0},
		{"(integer) RETURNS SETOF pg_catalog.int4[]", true, true, false, 0},
		{"() RETURNS \"setof\"", true, false, false, 0},
		{"(integer) RETURNS TABLE(a integer,b text)", true, true, true, 2},
		{"(integer) RETURNS TABLE(a integer)", true, true, true, 1},
	} {
		for _, body := range []string{" LANGUAGE SQL AS 'SELECT 1'", " LANGUAGE SQL BEGIN ATOMIC SELECT 1; END", " AS 'SELECT 1' LANGUAGE SQL"} {
			statements, err := Parse("CREATE FUNCTION result_form" + tc.declaration + body)
			if err != nil {
				t.Fatal(tc.declaration, body, err)
			}
			d := statements[0].(*CreateFunction)
			if d.ResultSpecified != tc.specified || d.Result.Setof != tc.set || (d.TableResult != nil) != tc.table || len(d.TableResult) != tc.outputs {
				t.Fatal("result declaration form lost", d)
			}
			for _, output := range d.TableResult {
				if output.Mode != "table" || output.Name.Text == "" || output.Pos.Character <= 0 || output.Type.Pos.Character <= 0 {
					t.Fatal("TABLE output syntax lost", output)
				}
			}
		}
	}
	for _, query := range []string{"SELECT returns setof", "SELECT returns AS setof", "SELECT \"setof\"", "SELECT setof integer", "SELECT setof FROM setof", "SELECT public.setof", "CREATE TABLE setof(setof integer)"} {
		if _, err := Parse(query); err != nil {
			t.Fatal("SETOF became a reserved ordinary identifier", query, err)
		}
	}
	_, err := Parse("CREATE FUNCTION rr_bad_table(OUT x integer) RETURNS TABLE(y integer) LANGUAGE SQL AS 'SELECT 1'")
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42601" || diagnostic.Message != "OUT and INOUT arguments aren't allowed in TABLE functions" || diagnostic.Position != "30" {
		t.Fatal("TABLE conflicting output diagnostic", err)
	}
	statements, err := Parse("CREATE FUNCTION result_form() RETURNS TABLE(a integer) LANGUAGE SQL AS 'SELECT 1'; invalid")
	if err == nil || len(statements) != 0 {
		t.Fatal("accepted valid declaration prefix")
	}
	for _, body := range []string{"LANGUAGE SQL AS 'SELECT 1'", "AS 'SELECT 1' LANGUAGE SQL", "BEGIN ATOMIC SELECT 1;END"} {
		statements, err := Parse("CREATE PROCEDURE result_procedure(OUT answer integer) " + body)
		if err != nil {
			t.Fatal(body, err)
		}
		declaration := statements[0].(*CreateFunction)
		if !declaration.Procedure || declaration.ResultSpecified || declaration.Arguments[0].Mode != "out" {
			t.Fatal("procedure synthesized RETURNS", declaration)
		}
	}
	t.Run("set_arguments", func(t *testing.T) {
		for _, kind := range []string{"FUNCTION", "PROCEDURE"} {
			for _, tc := range []struct {
				formal, mode, name string
				array              bool
			}{
				{"SETOF integer", "", "", false},
				{"a SETOF integer", "", "a", false},
				{"IN SETOF integer", "in", "", false},
				{"IN a SETOF integer", "in", "a", false},
				{"a IN SETOF integer", "in", "a", false},
				{"OUT SETOF integer", "out", "", false},
				{"OUT a SETOF integer", "out", "a", false},
				{"INOUT SETOF integer", "inout", "", false},
				{"INOUT a SETOF integer", "inout", "a", false},
				{"IN OUT a SETOF integer", "inout", "a", false},
				{"a IN OUT SETOF integer", "inout", "a", false},
				{"VARIADIC SETOF integer[]", "variadic", "", true},
				{"VARIADIC a SETOF integer[]", "variadic", "a", true},
			} {
				formal := tc.formal
				source := "CREATE " + kind + " p(" + formal + ") LANGUAGE SQL AS 'SELECT 1'"
				statements, err := Parse(source)
				if err != nil || len(statements) != 1 {
					t.Fatal(source, statements, err)
				}
				d := statements[0].(*CreateFunction)
				if len(d.Arguments) != 1 || !d.Arguments[0].Type.Setof || d.Arguments[0].Type.Name != "integer" || d.Procedure != (kind == "PROCEDURE") {
					t.Fatal("set argument syntax erased before declaration validation", d)
				}
				arg := d.Arguments[0]
				typeOffset := strings.Index(source, "integer")
				argOffset := strings.Index(source, "(") + 1
				if tc.mode == "" && tc.name == "" {
					argOffset = typeOffset
				}
				if arg.Mode != tc.mode || arg.Name.Text != tc.name || arg.Default != nil ||
					arg.Pos != (Position{Offset: argOffset, Character: utf8.RuneCountInString(source[:argOffset]) + 1}) ||
					arg.Type.Pos != (Position{Offset: typeOffset, Character: utf8.RuneCountInString(source[:typeOffset]) + 1}) ||
					(len(arg.Type.ArrayBounds) == 1) != tc.array {
					t.Fatal("set argument positions lost", arg)
				}
				if tc.name != "" {
					nameOffset := strings.Index(source[argOffset:], "a") + argOffset
					if arg.Name.Pos != (Position{Offset: nameOffset, Character: utf8.RuneCountInString(source[:nameOffset]) + 1}) {
						t.Fatal("set argument name position lost", arg)
					}
				}
				if _, err := Parse("SELECT setof"); err != nil || !arg.Type.Setof {
					t.Fatal("set argument ownership/ordinary name", err, arg)
				}
				if prefix, err := Parse(source + "; invalid"); err == nil || len(prefix) != 0 {
					t.Fatal("set declaration accepted valid prefix")
				}
			}
		}
		for _, formal := range []string{`a "setof"`, `"setof" integer`} {
			statements, err := Parse("CREATE PROCEDURE p(" + formal + ") LANGUAGE SQL AS 'SELECT 1'")
			if err != nil || len(statements) != 1 || statements[0].(*CreateFunction).Arguments[0].Type.Setof {
				t.Fatal("quoted SETOF interpreted as modifier", formal, statements, err)
			}
		}
		for _, defaultExpr := range []string{"setof", "ARRAY[setof,setof]", "(setof)"} {
			source := "CREATE PROCEDURE p(a integer DEFAULT " + defaultExpr + ",b SETOF integer) LANGUAGE SQL AS 'SELECT 1'"
			statements, err := Parse(source)
			if err != nil || len(statements) != 1 {
				t.Fatal("SETOF default/name context", source, statements, err)
			}
			d := statements[0].(*CreateFunction)
			if len(d.Arguments) != 2 || d.Arguments[0].Type.Setof || d.Arguments[0].Default == nil || !d.Arguments[1].Type.Setof || d.Arguments[1].Name.Text != "b" {
				t.Fatal("formal context leaked into default or next argument", d)
			}
		}
	})
}

func TestRoutineProcedureCall(t *testing.T) {
	for _, source := range []string{
		"/* λ */ CALL public.p(NULL,4,note=>1/0,VARIADIC xs=>ARRAY[1,2])",
		"/* λ */ CALL public.p(NULL,4,note:=1/0,VARIADIC xs=>ARRAY[1,2])",
	} {
		statements, err := Parse(source)
		if err != nil || len(statements) != 1 {
			t.Fatal(statements, err)
		}
		call, ok := statements[0].(*Call)
		if !ok || !call.Function.ProcedureCall || call.Pos.Character != 9 || call.Function.Pos.Character != 14 || len(call.Function.Args) != 4 || !call.Function.Variadic {
			t.Fatal("positioned CALL syntax", call)
		}
		if len(call.Function.ArgNames) != 4 || call.Function.ArgNames[2].Text != "note" || call.Function.ArgNames[3].Text != "xs" {
			t.Fatal("CALL notation lost", call)
		}
		count := 0
		if err := VisitQueryExpressions(call, func(raw Expr) error {
			if _, ok := raw.(ParameterExpr); ok {
				count++
			}
			return nil
		}); err != nil || count != 0 {
			t.Fatal(err, count)
		}
		if _, err := Parse("CALL other($1)"); err != nil || call.Function.ArgNames[2].Text != "note" {
			t.Fatal("parser reused CALL backing", err)
		}
		if prefix, err := Parse(source + " invalid"); err == nil || len(prefix) != 0 {
			t.Fatal("CALL accepted valid prefix")
		}
	}
	statements, err := Parse("CREATE OR REPLACE PROCEDURE p(OUT answer integer,IN x integer DEFAULT 7) LANGUAGE SQL AS 'SELECT x'")
	if err != nil {
		t.Fatal(err)
	}
	d := statements[0].(*CreateFunction)
	if !d.Procedure || !d.Replace || d.ResultSpecified || len(d.Arguments) != 2 || d.Arguments[0].Mode != "out" || d.Arguments[1].Default == nil {
		t.Fatal("procedure declaration lost", d)
	}
	for _, source := range []string{"SELECT call", "SELECT 1 AS call", "CALL p($1)", "CALL p(a=>NULL)"} {
		if _, err := Parse(source); err != nil {
			t.Fatal(source, err)
		}
	}
}
