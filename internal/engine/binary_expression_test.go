package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestBinaryBasicEvaluationTraversal(t *testing.T) {
	var d *sqltype.Descriptor
	for _, v := range sqltype.Builtins() {
		if v.ID() == sqltype.Bytea {
			d = v
		}
	}
	plan, err := sqltype.BindBinary("btrim", d.Usage(), d.Usage(), d.Usage())
	if err != nil {
		t.Fatal(err)
	}
	var order []int
	set, _ := sqltype.Binary(d, "\x00")
	expr := binaryExpression{expressionInfo: expressionInfo{usage: d.Usage()}, plan: plan, args: []BoundExpr{
		mathObservedArgument{expressionInfo: expressionInfo{usage: d.Usage()}, value: sqltype.Null(d), order: &order, index: 0},
		mathObservedArgument{expressionInfo: expressionInfo{usage: d.Usage()}, value: set, order: &order, index: 1},
	}}
	v, err := expr.Eval(&EvalContext{Context: context.Background()})
	if err != nil || !v.IsNull() || !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatal(v, order, err)
	}
	failure := errors.New("demanded binary sibling")
	expr.args[1] = mathObservedArgument{expressionInfo: expressionInfo{usage: d.Usage()}, order: &order, index: 1, err: failure}
	order = nil
	if _, err := expr.Eval(&EvalContext{Context: context.Background()}); !errors.Is(err, failure) || !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatal(order, err)
	}
}

func TestBinaryBasicTypedMetadataAndConsumers(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(v BYTEA,s BYTEA)")
	run(t, e, `INSERT INTO t VALUES('\x00ff8041008042ff00','\xff8000')`)
	result := run(t, e, "SELECT v||s AS cat,bit_length(v) AS bits,octet_length(v) AS octets,length(v) AS len,btrim(v,s) AS bt,ltrim(v,s) AS lt,rtrim(v,s) AS rt FROM t")
	cursor := independentFinalRows(result.Rows)
	defer cursor.Close()
	for i, col := range cursor.Schema() {
		id := sqltype.Bytea
		if i >= 1 && i <= 3 {
			id = sqltype.Int4
		}
		if col.Usage.Type.ID() != id || col.Usage.Typmod != -1 || col.Usage.Collation != (sqltype.CollationRef{}) {
			t.Fatal(i, col)
		}
	}
	values := make([]sqltype.Value, 7)
	present, err := cursor.Next(context.Background(), values)
	if err != nil || !present {
		t.Fatal(present, err)
	}
	want := []string{"\x00\xff\x80A\x00\x80B\xff\x00\xff\x80\x00", "", "", "", "A\x00\x80B", "A\x00\x80B\xff\x00", "\x00\xff\x80A\x00\x80B"}
	for i, v := range values {
		if i >= 1 && i <= 3 {
			n, _ := v.Integer()
			if n != []int64{72, 9, 9}[i-1] {
				t.Fatal(i, n)
			}
		} else {
			raw, err := v.Binary()
			if err != nil || raw != want[i] {
				t.Fatal(i, raw, err)
			}
		}
	}
	for _, q := range []string{"SELECT length(v),count(*) FROM t GROUP BY length(v)", "SELECT length(v) FROM t ORDER BY length(v)", "SELECT CASE WHEN length(v)=9 THEN bit_length(v) ELSE 0 END FROM t"} {
		rows := mathTuples(t, run(t, e, q))
		if len(rows) != 1 || rows[0][0] != (map[bool]int64{true: 72, false: 9}[q == "SELECT CASE WHEN length(v)=9 THEN bit_length(v) ELSE 0 END FROM t"]) {
			t.Fatal(q, rows)
		}
	}
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	raw := statement(t, "SELECT length(1)").(*parser.Select).Targets[0]
	if _, err := b.bind(raw, expressionPolicy{context: projectionContext}); err == nil {
		t.Fatal("integer signature accepted")
	}
}
