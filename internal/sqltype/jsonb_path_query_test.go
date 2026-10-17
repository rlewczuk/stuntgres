package sqltype

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/sqlstate"
)

type jsonpathScalarUnitPlan interface {
	Evaluate(context.Context, []Value) (Value, error)
}

func queryUnitPlans(t *testing.T, types map[ID]*Descriptor) ([]jsonpathScalarUnitPlan, JSONBPathQueryPlan) {
	t.Helper()
	args := []Usage{types[JSONB].Usage(), types[JSONPath].Usage(), types[JSONB].Usage(), types[Bool].Usage()}
	exists, err := BindJSONBPathExists(types[Bool].Usage(), args...)
	if err != nil {
		t.Fatal(err)
	}
	match, err := BindJSONBPathMatch(types[Bool].Usage(), args...)
	if err != nil {
		t.Fatal(err)
	}
	first, err := BindJSONBPathQueryFirst(types[JSONB].Usage(), args...)
	if err != nil {
		t.Fatal(err)
	}
	query, err := BindJSONBPathQuery(types[JSONB].Usage(), args...)
	if err != nil {
		t.Fatal(err)
	}
	return []jsonpathScalarUnitPlan{exists, match, first}, query
}

func TestJSONBPathQuerySignatures(t *testing.T) {
	types, fresh := testTypes(), testTypes()
	plans, query := queryUnitPlans(t, types)
	ctx := context.Background()
	values := navigateValues(t, types, `true`, `$`, `{}`, false)
	args := []Usage{types[JSONB].Usage(), types[JSONPath].Usage(), types[JSONB].Usage(), types[Bool].Usage()}
	for _, id := range []ID{Bool, JSONB} {
		for count := range 6 {
			bad := make([]Usage, count)
			copy(bad, args)
			if count == 4 {
				bad[1] = types[JSONB].Usage()
			}
			if _, err := bindJSONBPathConsumer(types[id].Usage(), id, bad); err == nil {
				t.Fatal("accepted invalid signature", id, count)
			}
		}
		for slot := range 4 {
			bad := append([]Usage(nil), args...)
			bad[slot].Typmod = 1
			if _, err := bindJSONBPathConsumer(types[id].Usage(), id, bad); err == nil {
				t.Fatal("accepted typmod", slot)
			}
		}
		bad := append([]Usage(nil), args...)
		bad[2] = fresh[JSONB].Usage()
		if _, err := bindJSONBPathConsumer(types[id].Usage(), id, bad); err == nil {
			t.Fatal("accepted foreign vars descriptor", id)
		}
		if _, err := bindJSONBPathConsumer(fresh[id].Usage(), id, args); err == nil {
			t.Fatal("accepted foreign result descriptor", id)
		}
	}
	for slot := range 4 {
		nulls := append([]Value(nil), values...)
		nulls[slot] = Null(nulls[slot].typ)
		for i, plan := range plans {
			v, err := plan.Evaluate(ctx, nulls)
			want := types[Bool]
			if i == 2 {
				want = types[JSONB]
			}
			if err != nil || !v.null || v.typ != want {
				t.Fatal("strict scalar NULL", i, slot, v, err)
			}
		}
		if rows, err := query.Evaluate(ctx, nulls); err != nil || rows != nil {
			t.Fatal("strict set NULL", rows, err)
		}
		foreign := append([]Value(nil), nulls...)
		foreign[slot] = Null(fresh[values[slot].typ.ID()])
		for _, plan := range plans {
			if _, err := plan.Evaluate(ctx, foreign); err == nil {
				t.Fatal("foreign value before strict NULL", slot)
			}
		}
		if _, err := query.Evaluate(ctx, foreign); err == nil {
			t.Fatal("foreign set value", slot)
		}
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		for _, plan := range plans {
			if _, err := plan.Evaluate(canceled, nulls); !errors.Is(err, context.Canceled) {
				t.Fatal("context before NULL", err)
			}
		}
		if rows, err := query.Evaluate(canceled, nulls); !errors.Is(err, context.Canceled) || rows != nil {
			t.Fatal("set context before NULL", rows, err)
		}
	}
	for _, slot := range []int{0, 1, 2} {
		bad := append([]Value(nil), values...)
		bad[slot] = Value{typ: bad[slot].typ}
		for _, plan := range plans {
			if _, err := plan.Evaluate(ctx, bad); err == nil {
				t.Fatal("missing payload", slot)
			}
		}
		if _, err := query.Evaluate(ctx, bad); err == nil {
			t.Fatal("missing set payload", slot)
		}
	}
}

func TestJSONBPathQueryConsumers(t *testing.T) {
	types := testTypes()
	plans, query := queryUnitPlans(t, types)
	ctx := context.Background()
	state := func(err error, want string) {
		t.Helper()
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != want {
			t.Fatalf("got %v; want %s", err, want)
		}
	}
	// Existence can stop only in lax mode. First and match must demand an
	// ordinary tail error, even when cardinality has already failed.
	for _, path := range []string{`$[0,$missing]`, `strict $[0,$missing]`, `strict $[0,0,$missing]`} {
		values := navigateValues(t, types, `[true]`, path, `{}`, true)
		for i, plan := range plans {
			v, err := plan.Evaluate(ctx, values)
			if i == 0 && path[0] == '$' {
				if err != nil || v.null || !v.boolean {
					t.Fatal(v, err)
				}
			} else {
				state(err, "42704")
			}
		}
		rows, err := query.Evaluate(ctx, values)
		state(err, "42704")
		if rows != nil {
			t.Fatal("ordinary failure published prefix", rows)
		}
	}
	for _, prefix := range []string{`true`, `false`, `null`, `1`} {
		values := navigateValues(t, types, `[{"x":`+prefix+`},{},{"x":false}]`, `strict $[*].x`, `{}`, false)
		for _, plan := range plans {
			v, err := plan.Evaluate(ctx, values)
			state(err, "2203A")
			if v.typ != nil {
				t.Fatal("scalar error published value", v)
			}
		}
		if rows, err := query.Evaluate(ctx, values); rows != nil {
			t.Fatal("structural failure published prefix", rows, err)
		} else {
			state(err, "2203A")
		}
		values[3] = Boolean(types[Bool], true)
		exists, err := plans[0].Evaluate(ctx, values)
		if err != nil || !exists.null {
			t.Fatal("strict silent existence", exists, err)
		}
		match, err := plans[1].Evaluate(ctx, values)
		if err != nil {
			t.Fatal(err)
		}
		if prefix == "true" || prefix == "false" {
			if match.null || match.boolean != (prefix == "true") {
				t.Fatal("retained boolean prefix", match)
			}
		} else if !match.null {
			t.Fatal("nonboolean silent prefix", match)
		}
		first, err := plans[2].Evaluate(ctx, values)
		if err != nil || first.null || first.text != prefix {
			t.Fatal("first retained prefix", first, err)
		}
		rows, err := query.Evaluate(ctx, values)
		if err != nil || len(rows) != 1 || rows[0].text != prefix {
			t.Fatal("query retained prefix", rows, err)
		}
	}
	// Returned slice mutation and later invocations cannot replace retained data.
	values := navigateValues(t, types, `[1,2,3]`, `$[*]`, `{}`, false)
	old, err := query.Evaluate(ctx, values)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := query.Evaluate(ctx, navigateValues(t, types, `[4]`, `$[*]`, `{}`, false))
	if err != nil || len(fresh) != 1 || fresh[0].text != "4" {
		t.Fatal(fresh, err)
	}
	fresh[0] = Null(types[JSONB])
	if len(old) != 3 || old[0].text != "1" || old[2].text != "3" {
		t.Fatal("retained slice changed", old)
	}
	// Saturation tracks many results without turning match into first-value truth.
	for _, silent := range []bool{false, true} {
		v, err := plans[1].Evaluate(ctx, navigateValues(t, types, `[true,true,true]`, `$[*]`, `{}`, silent))
		if silent {
			if err != nil || !v.null {
				t.Fatal(v, err)
			}
		} else {
			state(err, "22038")
		}
	}
}
