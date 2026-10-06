package engine

import (
	"context"
	"database/sql/driver"
	"reflect"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestInsertSelectIntegratedExpressions(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE s(i INT,b BOOL,d DATE,v BYTEA)")
	run(t, e, "INSERT INTO s VALUES(1,TRUE,DATE '2026-10-06','\\x41'),(2,NULL,NULL,NULL)")
	run(t, e, "CREATE TABLE t(i INT,b BOOL,d DATE,v BYTEA)")
	result := run(t, e, "INSERT INTO t SELECT CAST(i AS INT),b IS TRUE,d,v FROM s ORDER BY i")
	if result.Count != 2 || result.Rows != nil {
		t.Fatal(result)
	}
	result = run(t, e, "SELECT i,b,d,v FROM t ORDER BY i")
	defer result.Rows.Close()
	want := [][]driver.Value{{int64(1), true, "2026-10-06", []byte("A")}, {int64(2), false, nil, nil}}
	if got := tuples(result); !reflect.DeepEqual(got, want) {
		t.Fatal(got, want)
	}
	result = run(t, e, "INSERT INTO t(i) VALUES(int4('3'::text)),(CAST('4' AS INT)) ORDER BY 1")
	if result.Count != 2 || result.Rows != nil {
		t.Fatal(result)
	}
}

func TestInsertSelectValuesPreflightPrivate(t *testing.T) {
	expression := preflightCast(t, "SELECT int4('42'::text) FROM t")
	schema := []ColumnDescriptor{{Name: "column1", Usage: expression.ResultUsage()}}
	original := &valuesNode{querySchema: schema, rows: [][]BoundExpr{{expression}, {expression}}}
	for range 2 {
		root, err := preflightQuery(&EvalContext{Context: context.Background()}, original)
		if err != nil || root == original {
			t.Fatal(root, err)
		}
		folded := root.(*valuesNode)
		for i, row := range folded.rows {
			literal, ok := row[0].(literalExpression)
			if !ok || &row[0] == &original.rows[i][0] {
				t.Fatal("preflight did not produce a private folded occurrence", i)
			}
			value, err := literal.Eval(&EvalContext{Context: context.Background()})
			if err != nil || value.Type().ID() != sqltype.Int4 {
				t.Fatal(value, err)
			}
			if got, err := value.Integer(); err != nil || got != 42 {
				t.Fatal(got, err)
			}
			if _, ok := original.rows[i][0].(coercionExpression); !ok {
				t.Fatal("analysis expression mutated")
			}
		}
	}
	bad := &valuesNode{rows: [][]BoundExpr{{preflightCast(t, "SELECT int4('bad'::text) FROM t")}}}
	_, err := preflightQuery(&EvalContext{Context: context.Background()}, bad)
	state(t, err, "22P02")
}
