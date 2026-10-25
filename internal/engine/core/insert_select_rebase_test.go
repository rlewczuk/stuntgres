package core_test

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestInsertSelectIntegratedExpressions(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE s(i INT,b BOOL,d DATE,v BYTEA)")
	enginetest.Run(t, e, "INSERT INTO s VALUES(1,TRUE,DATE '2026-10-06','\\x41'),(2,NULL,NULL,NULL)")
	enginetest.Run(t, e, "CREATE TABLE t(i INT,b BOOL,d DATE,v BYTEA)")
	result := enginetest.Run(t, e, "INSERT INTO t SELECT CAST(i AS INT),b IS TRUE,d,v FROM s ORDER BY i")
	if result.Count != 2 || result.Rows != nil {
		t.Fatal(result)
	}
	result = enginetest.Run(t, e, "SELECT i,b,d,v FROM t ORDER BY i")
	defer result.Rows.Close()
	wantTypes := []sqltype.ID{sqltype.Int4, sqltype.Bool, sqltype.Date, sqltype.Bytea}
	if len(result.Rows.Schema()) != len(wantTypes) {
		t.Fatal("destination width", result.Rows.Schema())
	}
	for i, column := range result.Rows.Schema() {
		if column.Usage.Type.ID() != wantTypes[i] {
			t.Fatal("destination type", i, column)
		}
	}
	want := [][]any{{"1", "t", "2026-10-06", `\x41`}, {"2", "f", nil, nil}}
	got := make([][]any, 0, len(want))
	values := make([]sqltype.Value, len(wantTypes))
	for {
		present, err := result.Rows.Next(context.Background(), values)
		if err != nil {
			t.Fatal(err)
		}
		if !present {
			break
		}
		row := make([]any, len(values))
		for i, value := range values {
			if value.IsNull() {
				continue
			}
			text, err := value.Type().Output(context.Background(), value)
			if err != nil {
				t.Fatal(err)
			}
			row[i] = text
		}
		got = append(got, row)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal(got, want)
	}
	result = enginetest.Run(t, e, "INSERT INTO t(i) VALUES(int4('3'::text)),(CAST('4' AS INT)) ORDER BY 1")
	if result.Count != 2 || result.Rows != nil {
		t.Fatal(result)
	}
}

func TestInsertSelectValuesPreflightPrivate(t *testing.T) {
	expression := preflightCast(t, "SELECT int4('42'::text) FROM t")
	schema := []catalog.Column{{Name: "column1", Usage: expression.ResultUsage()}}
	original := &plan.ValuesNode{QuerySchema: schema, Rows: [][]expr.Expr{{expression}, {expression}}}
	for range 2 {
		root, err := view.PreflightQuery(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}, original)
		if err != nil || root == original {
			t.Fatal(root, err)
		}
		folded := root.(*plan.ValuesNode)
		for i, row := range folded.Rows {
			literal, ok := row[0].(plan.LiteralExpr)
			if !ok || &row[0] == &original.Rows[i][0] {
				t.Fatal("preflight did not produce a private folded occurrence", i)
			}
			value, err := literal.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
			if err != nil || value.Type().ID() != sqltype.Int4 {
				t.Fatal(value, err)
			}
			if got, err := value.Integer(); err != nil || got != 42 {
				t.Fatal(got, err)
			}
			if _, ok := original.Rows[i][0].(plan.CoercionExpr); !ok {
				t.Fatal("analysis expression mutated")
			}
		}
	}
	bad := &plan.ValuesNode{Rows: [][]expr.Expr{{preflightCast(t, "SELECT int4('bad'::text) FROM t")}}}
	_, err := view.PreflightQuery(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}, bad)
	enginetest.State(t, err, "22P02")
}

func preflightCast(t *testing.T, q string) expr.Expr {
	t.Helper()
	b := enginetest.ExpressionBinder(t, context.Background(), engine.New(), sqltype.InputEnvironment{}, nil)
	expression, err := b.Bind(castTarget(t, q), analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
	if err != nil {
		t.Fatal(err)
	}
	return expression
}

func castTarget(t *testing.T, q string) parser.Expr {
	t.Helper()
	return enginetest.Statement(t, q).(*parser.Select).Targets[0]
}
