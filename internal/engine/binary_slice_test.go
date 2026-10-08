package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestBinarySliceHeterogeneousTraversal(t *testing.T) {
	var bytea, integer *sqltype.Descriptor
	for _, d := range sqltype.Builtins() {
		if d.ID() == sqltype.Bytea {
			bytea = d
		}
		if d.ID() == sqltype.Int4 {
			integer = d
		}
	}
	plan, err := sqltype.BindBinary("overlay", bytea.Usage(), bytea.Usage(), bytea.Usage(), integer.Usage(), integer.Usage())
	if err != nil {
		t.Fatal(err)
	}
	a, _ := sqltype.Binary(bytea, "abcdef")
	r, _ := sqltype.Binary(bytea, "XY")
	s, _ := sqltype.Integer(integer, 3)
	n, _ := sqltype.Integer(integer, 2)
	var order []int
	expr := binaryExpression{expressionInfo: expressionInfo{usage: bytea.Usage()}, plan: plan}
	for i, v := range []sqltype.Value{a, r, s, n} {
		expr.args = append(expr.args, mathObservedArgument{expressionInfo: expressionInfo{usage: v.Type().Usage()}, value: v, order: &order, index: i})
	}
	got, err := expr.Eval(&EvalContext{Context: context.Background()})
	raw, _ := got.Binary()
	if err != nil || raw != "abXYef" || !reflect.DeepEqual(order, []int{0, 1, 2, 3}) {
		t.Fatal(raw, order, err)
	}
	failure := errors.New("fourth child failure")
	expr.args[0] = mathObservedArgument{expressionInfo: expressionInfo{usage: bytea.Usage()}, value: sqltype.Null(bytea), order: &order, index: 0}
	expr.args[3] = mathObservedArgument{expressionInfo: expressionInfo{usage: integer.Usage()}, order: &order, index: 3, err: failure}
	order = nil
	if _, err := expr.Eval(&EvalContext{Context: context.Background()}); !errors.Is(err, failure) || !reflect.DeepEqual(order, []int{0, 1, 2, 3}) {
		t.Fatal(order, err)
	}
}

func TestBinarySliceTypedMetadata(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(v BYTEA,s BYTEA,i INTEGER,n INTEGER)")
	for _, q := range []string{"SELECT overlay(v,s,i,n),substring(v FROM i FOR n),position(s IN v) FROM t", "SELECT overlay(v,s,i,n),substring(v FROM i FOR n),position(s IN v) FROM t WHERE n<0"} {
		result := run(t, e, q)
		cursor := independentFinalRows(result.Rows)
		for i, col := range cursor.Schema() {
			id := sqltype.Bytea
			if i == 2 {
				id = sqltype.Int4
			}
			if col.Usage.Type.ID() != id || col.Usage.Typmod != -1 || col.Usage.Collation != (sqltype.CollationRef{}) {
				t.Fatal(i, col)
			}
		}
		var frame [3]sqltype.Value
		present, err := cursor.Next(context.Background(), frame[:])
		cursor.Close()
		if present || err != nil {
			t.Fatal(present, err)
		}
	}
}
