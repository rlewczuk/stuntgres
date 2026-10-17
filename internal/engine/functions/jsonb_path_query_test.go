package functions

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestJSONBPathQueryReconstruct(t *testing.T) {
	t.Run("Exists", func(t *testing.T) {
		d, fresh := temporalTestTypes(), temporalTestTypes()
		ids := [4]sqltype.ID{sqltype.JSONB, sqltype.JSONPath, sqltype.JSONB, sqltype.Bool}
		e := JSONBPathExists[temporalTestExpression, *temporalTestContext, uint64]{
			Info:         Info[uint64]{Usage: d[sqltype.Bool].Usage(), Property: 7},
			WrittenCount: 3, WrittenSlots: [4]uint8{1, 0, 3},
			Labels: [4]string{"path", "target", "silent"},
		}
		var usages [4]sqltype.Usage
		var args [4]temporalTestExpression
		for i, id := range ids {
			usages[i] = d[id].Usage()
			e.Args[i] = temporalTestLiteral(sqltype.Null(d[id]))
			args[i] = temporalTestLiteral(sqltype.Null(fresh[id]))
		}
		var err error
		e.Plan, err = sqltype.BindJSONBPathExists(e.Usage, usages[:]...)
		if err != nil {
			t.Fatal(err)
		}
		info := Info[uint64]{Usage: fresh[sqltype.Bool].Usage(), Property: 19,
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
		if err != nil || !value.IsNull() || value.Type() != fresh[sqltype.Bool] {
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
	})
	t.Run("Match", func(t *testing.T) {
		d, fresh := temporalTestTypes(), temporalTestTypes()
		ids := [4]sqltype.ID{sqltype.JSONB, sqltype.JSONPath, sqltype.JSONB, sqltype.Bool}
		e := JSONBPathMatch[temporalTestExpression, *temporalTestContext, uint64]{
			Info:         Info[uint64]{Usage: d[sqltype.Bool].Usage(), Property: 7},
			WrittenCount: 3, WrittenSlots: [4]uint8{1, 0, 3},
			Labels: [4]string{"path", "target", "silent"},
		}
		var usages [4]sqltype.Usage
		var args [4]temporalTestExpression
		for i, id := range ids {
			usages[i] = d[id].Usage()
			e.Args[i] = temporalTestLiteral(sqltype.Null(d[id]))
			args[i] = temporalTestLiteral(sqltype.Null(fresh[id]))
		}
		var err error
		e.Plan, err = sqltype.BindJSONBPathMatch(e.Usage, usages[:]...)
		if err != nil {
			t.Fatal(err)
		}
		info := Info[uint64]{Usage: fresh[sqltype.Bool].Usage(), Property: 19,
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
		if err != nil || !value.IsNull() || value.Type() != fresh[sqltype.Bool] {
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
	})
	t.Run("QueryFirst", func(t *testing.T) {
		d, fresh := temporalTestTypes(), temporalTestTypes()
		ids := [4]sqltype.ID{sqltype.JSONB, sqltype.JSONPath, sqltype.JSONB, sqltype.Bool}
		e := JSONBPathQueryFirst[temporalTestExpression, *temporalTestContext, uint64]{
			Info:         Info[uint64]{Usage: d[sqltype.JSONB].Usage(), Property: 7},
			WrittenCount: 3, WrittenSlots: [4]uint8{1, 0, 3},
			Labels: [4]string{"path", "target", "silent"},
		}
		var usages [4]sqltype.Usage
		var args [4]temporalTestExpression
		for i, id := range ids {
			usages[i] = d[id].Usage()
			e.Args[i] = temporalTestLiteral(sqltype.Null(d[id]))
			args[i] = temporalTestLiteral(sqltype.Null(fresh[id]))
		}
		var err error
		e.Plan, err = sqltype.BindJSONBPathQueryFirst(e.Usage, usages[:]...)
		if err != nil {
			t.Fatal(err)
		}
		info := Info[uint64]{Usage: fresh[sqltype.JSONB].Usage(), Property: 19,
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
	})
	t.Run("Query", func(t *testing.T) {
		d, fresh := temporalTestTypes(), temporalTestTypes()
		ids := [4]sqltype.ID{sqltype.JSONB, sqltype.JSONPath, sqltype.JSONB, sqltype.Bool}
		e := JSONBPathQuery[temporalTestExpression, *temporalTestContext, uint64]{
			Info:         Info[uint64]{Usage: d[sqltype.JSONB].Usage(), Property: 7},
			WrittenCount: 3, WrittenSlots: [4]uint8{1, 0, 3},
			Labels: [4]string{"path", "target", "silent"},
		}
		var usages [4]sqltype.Usage
		var args [4]temporalTestExpression
		for i, id := range ids {
			usages[i] = d[id].Usage()
			e.Args[i] = temporalTestLiteral(sqltype.Null(d[id]))
			args[i] = temporalTestLiteral(sqltype.Null(fresh[id]))
		}
		var err error
		e.Plan, err = sqltype.BindJSONBPathQuery(e.Usage, usages[:]...)
		if err != nil {
			t.Fatal(err)
		}
		info := Info[uint64]{Usage: fresh[sqltype.JSONB].Usage(), Property: 19,
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
		value, err := got.EvalSet(ctx)
		if err != nil || value != nil {
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
	})

	// A strict NULL input must not skip ordinary argument evaluation. The shared
	// helper evaluates written slots once, in presentation order, then defaults.
	d := temporalTestTypes()
	var order []int
	var args [4]temporalTestExpression
	ids := [4]sqltype.ID{sqltype.JSONB, sqltype.JSONPath, sqltype.JSONB, sqltype.Bool}
	for i, id := range ids {
		args[i] = queryOrderArgument{value: sqltype.Null(d[id]), slot: i, order: &order}
	}
	ctx := &temporalTestContext{Context: context.Background()}
	if _, err := jsonbPathArguments(ctx, args, 3, [4]uint8{1, 0, 3}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(order, []int{1, 0, 3, 2}) {
		t.Fatal(order)
	}
	order = nil
	failure := errors.New("argument failure")
	args[0] = queryOrderArgument{value: sqltype.Null(d[sqltype.JSONB]), slot: 0, order: &order, err: failure}
	if _, err := jsonbPathArguments(ctx, args, 3, [4]uint8{1, 0, 3}); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(order, []int{1, 0}) {
		t.Fatal("evaluation continued after error", order)
	}
}

type queryOrderArgument struct {
	value sqltype.Value
	slot  int
	order *[]int
	err   error
}

func (a queryOrderArgument) ResultUsage() sqltype.Usage { return a.value.Type().Usage() }
func (a queryOrderArgument) Eval(*temporalTestContext) (sqltype.Value, error) {
	*a.order = append(*a.order, a.slot)
	return a.value, a.err
}
