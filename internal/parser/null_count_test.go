package parser

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestNullCountRawCalls(t *testing.T) {
	for _, q := range []string{
		"SELECT num_nulls(1,NULL,'x'),num_nonnulls(FALSE,0) FROM t",
		"SELECT NUM_NULLS(1,NULL,'x'),num_nonnulls(FALSE,0)",
		`SELECT "num_nulls"(1,NULL,'x'),num_nonnulls(FALSE,0)`,
	} {
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatal(q, statements, err)
		}
		selectStmt := statements[0].(*Select)
		if len(selectStmt.Targets) != 2 {
			t.Fatal(selectStmt)
		}
		call, ok := selectStmt.Targets[0].(FunctionExpr)
		if !ok || call.Name.Parts[0].Text != "num_nulls" || call.Pos.Character != 8 || call.SourcePosition() != call.Pos || call.Variadic || len(call.Args) != 3 {
			t.Fatal(call)
		}
		if call.Args[0].(LiteralExpr).Value.Integer != 1 || call.Args[1].(LiteralExpr).Value.Kind != NullValue || call.Args[2].(LiteralExpr).Value.Text != "x" {
			t.Fatal(call.Args)
		}
		if selectStmt.IsSingleton() != (selectStmt.From == nil) {
			t.Fatal("singleton admission", selectStmt)
		}
	}
	statements, err := Parse("SELECT /*é*/ num_nulls(num_nonnulls(v),NULL,$1) FROM t")
	if err != nil {
		t.Fatal(err)
	}
	call := statements[0].(*Select).Targets[0].(FunctionExpr)
	if call.Pos.Character != 14 || call.Pos.Offset != 14 || len(call.Args) != 3 {
		t.Fatal(call)
	}
	inner := call.Args[0].(FunctionExpr)
	if inner.Name.Parts[0].Text != "num_nonnulls" || inner.Args[0].(ColumnExpr).Name.Parts[0].Text != "v" || call.Args[2].(ParameterExpr).Number != 1 {
		t.Fatal(call)
	}
	for _, q := range []string{"SELECT num_nulls()", "SELECT num_nonnulls()"} {
		statements, err := Parse(q)
		if err != nil {
			t.Fatal(err)
		}
		call := statements[0].(*Select).Targets[0].(FunctionExpr)
		if len(call.Args) != 0 || !statements[0].(*Select).IsSingleton() {
			t.Fatal(call)
		}
	}
	for _, q := range []string{"SELECT num_nulls(VARIADIC 1)", "SELECT num_nulls(1,VARIADIC NULL)"} {
		statements, err := Parse(q)
		if err != nil {
			t.Fatal(err)
		}
		call := statements[0].(*Select).Targets[0].(FunctionExpr)
		if !call.Variadic || len(call.Args) == 0 || call.Args[len(call.Args)-1].SourcePosition().Character <= call.Pos.Character {
			t.Fatal(call)
		}
	}
	for _, n := range []int{100, 101} {
		q := "SELECT num_nulls(" + strings.TrimSuffix(strings.Repeat("NULL,", n), ",") + ")"
		statements, err := Parse(q)
		if err != nil || len(statements[0].(*Select).Targets[0].(FunctionExpr).Args) != n {
			t.Fatal(n, err)
		}
	}
	statements, err = Parse("SELECT num_nulls,num_nonnulls FROM t")
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"num_nulls", "num_nonnulls"} {
		if name := statements[0].(*Select).Targets[i].(ColumnExpr).Name.Parts[0].Text; name != want {
			t.Fatal(name, want)
		}
	}
}

func TestNullCountSyntaxAndBoundaries(t *testing.T) {
	for _, tc := range []struct{ q, message, pos string }{
		{"SELECT num_nulls(1,)", `syntax error at or near ")"`, "20"},
		{"SELECT num_nulls(", "syntax error at end of input", "18"},
		{"SELECT /*é*/ num_nulls(1,)", `syntax error at or near ")"`, "26"},
	} {
		statements, err := Parse(tc.q)
		var got *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: tc.message, Position: tc.pos}
		if len(statements) != 0 || !errors.As(err, &got) || *got != want {
			t.Fatal(tc.q, statements, got, err, want)
		}
	}
	for _, q := range []string{
		"SELECT 1", "SELECT num_nulls(NULL),1", "SELECT num_nulls(v)", "SELECT num_nulls(NULL) WHERE TRUE",
		"SELECT num_nulls(NULL) ORDER BY 1", "SELECT num_nulls(VARIADIC ARRAY[1,NULL])",
		"SELECT id FROM t WHERE num_nulls(v)", "INSERT INTO t VALUES(num_nulls(NULL))",
		"SELECT current_setting(num_nulls(NULL))", "SELECT abs(1) FROM t",
		"SELECT pg_catalog.num_nulls(NULL) FROM t",
		"SELECT num_nulls(NULL)::int FROM t", "SELECT num_nulls(DISTINCT 1) FROM t",
		"SELECT num_nulls(1 ORDER BY 1) FROM t", "SELECT num_nulls(*) FROM t",
		"SELECT num_nulls(1) FILTER (WHERE TRUE) FROM t", "SELECT num_nulls(1) OVER () FROM t",
		"SELECT num_nulls(a => 1) FROM t", `SELECT "NUM_NULLS"(NULL) FROM t`,
	} {
		statements, err := Parse(q)
		if err == nil || len(statements) != 0 {
			t.Fatal("excluded syntax admitted", q, statements, err)
		}
	}
	// Full-input malformed suffixes must not execute a parsed script prefix.
	statements, err := Parse("CREATE TABLE staged(v INT); SELECT num_nulls(NULL),1 WHERE")
	var diagnostic *sqlstate.Error
	if len(statements) != 0 || !errors.As(err, &diagnostic) || diagnostic.Code != "42601" {
		t.Fatal(statements, err)
	}
	selectStmt := &Select{Targets: []Expr{nullCountParserCall(ColumnExpr{Name: ObjectName{Parts: []Identifier{{Text: "v"}}}})}}
	if selectStmt.IsSingleton() {
		t.Fatal("column argument admitted to singleton")
	}
	selectStmt.Targets = []Expr{nullCountParserCall(LiteralExpr{Value: Value{Kind: NullValue}})}
	if !selectStmt.IsSingleton() {
		t.Fatal(selectStmt)
	}
	selectStmt.From = &TableReference{Name: ObjectName{Parts: []Identifier{{Text: "t"}}}}
	if selectStmt.IsSingleton() || !reflect.DeepEqual(selectStmt.From.(*TableReference).Name.Parts, []Identifier{{Text: "t"}}) {
		t.Fatal(selectStmt)
	}
}

func nullCountParserCall(arg Expr) FunctionExpr {
	return FunctionExpr{Name: ObjectName{Parts: []Identifier{{Text: "num_nulls"}}}, Args: []Expr{arg}}
}

func TestNullCountVariadicKeywordContexts(t *testing.T) {
	for _, q := range []string{
		"CREATE TABLE public.variadic (x INT)",
		"INSERT INTO public.variadic (x) VALUES (7)",
		"SELECT x FROM public.variadic WHERE x=7 ORDER BY x",
		`CREATE TABLE "variadic" ("variadic" INT)`,
	} {
		if statements, err := Parse(q); err != nil || len(statements) != 1 {
			t.Fatal(q, statements, err)
		}
	}
	// Token promotion must retain ColLabel legality without making a reserved
	// keyword eligible as an unqualified table/column name.
	keywordDiagnostic(t, "CREATE TABLE variadic (x INT)", "42601", `syntax error at or near "variadic"`, "14")
	orderLabel(t, "SELECT x variadic FROM t", "variadic")
	orderLabel(t, "SELECT x AS variadic FROM t", "variadic")
}
