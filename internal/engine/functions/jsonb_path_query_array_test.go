package functions

import (
	"context"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

type jsonpathArrayUnitReference uint32

func (r jsonpathArrayUnitReference) RoutineOID() uint32       { return uint32(r) }
func (r jsonpathArrayUnitReference) RoutineRecipeOID() uint32 { return uint32(r) }

func TestJSONBPathQueryArrayReconstruct(t *testing.T) {
	d, fresh := temporalTestTypes(), temporalTestTypes()
	ids := [4]sqltype.ID{sqltype.JSONB, sqltype.JSONPath, sqltype.JSONB, sqltype.Bool}
	e := JSONBPathQueryArray{
		Info:         expr.Info{Usage: d[sqltype.JSONB].Usage(), Property: expr.Properties{Volatility: expr.Stable, RowDependent: true}},
		WrittenCount: 3, WrittenSlots: [4]uint8{1, 0, 3},
		Labels: [4]string{"path", "target", "silent"},
	}
	var usages [4]sqltype.Usage
	var args [4]expr.Expr
	for i, id := range ids {
		usages[i] = d[id].Usage()
		e.Args[i] = temporalTestLiteral(sqltype.Null(d[id]))
		args[i] = temporalTestLiteral(sqltype.Null(fresh[id]))
	}
	var err error
	e.Plan, err = sqltype.BindJSONBPathQueryArray(e.Usage, usages[:]...)
	if err != nil {
		t.Fatal(err)
	}
	info := expr.Info{Usage: fresh[sqltype.JSONB].Usage(), Property: expr.Properties{Volatility: expr.Volatile, ParameterDependent: true},
		RoutineReference: jsonpathArrayUnitReference(4007), RoutineRecipeReference: jsonpathArrayUnitReference(123)}
	got, err := e.Reconstruct(info, args)
	if err != nil {
		t.Fatal(err)
	}
	if got.Info != info || got.WrittenCount != 3 || got.WrittenSlots != e.WrittenSlots || got.Labels != e.Labels || got.Variadic != e.Variadic {
		t.Fatal("reconstruction lost metadata or written presentation", got)
	}
	for i, id := range ids {
		if got.Args[i].ResultUsage().Type != fresh[id] || e.Args[i].ResultUsage().Type != d[id] {
			t.Fatal("reconstruction mutated source", i)
		}
	}
	ctx := &temporalTestContext{Context: context.Background()}
	value, err := got.Eval(ctx)
	if err != nil || !value.IsNull() || value.Type() != fresh[sqltype.JSONB] {
		t.Fatal("fresh plan did not evaluate with fresh descriptor authority", value, err)
	}
	bad := args
	bad[2] = e.Args[2]
	if _, err := e.Reconstruct(info, bad); err == nil {
		t.Fatal("foreign JSONB variable descriptor accepted")
	}
	if _, err := e.Reconstruct(e.Info, args); err == nil {
		t.Fatal("foreign result descriptor accepted")
	}
	var values [4]sqltype.Value
	for i, id := range ids {
		values[i] = sqltype.Null(fresh[id])
	}
	for _, slot := range []int{1, 3} {
		foreign := values
		foreign[slot] = sqltype.Null(d[ids[slot]])
		if _, err := got.Plan.Evaluate(ctx.Context, foreign[:]); err == nil {
			t.Fatal("foreign value accepted before strict NULL", slot)
		}
	}
}
