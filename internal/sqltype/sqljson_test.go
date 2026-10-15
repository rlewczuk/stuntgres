package sqltype

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestSQLJSONScannerKindsAndUniqueness(t *testing.T) {
	types := testTypes()
	for _, tc := range []struct {
		source, kind string
		unique, want bool
	}{
		{`{}`, "OBJECT", false, true}, {`[]`, "ARRAY", false, true},
		{`"s"`, "SCALAR", false, true}, {`1`, "SCALAR", false, true},
		{`true`, "SCALAR", false, true}, {`false`, "SCALAR", false, true}, {`null`, "SCALAR", false, true},
		{`{"a":1,"\u0061":2}`, "VALUE", true, false},
		{`[{"a":1,"a":2}]`, "ARRAY", true, false},
		{`[{"a":1},{"a":2}]`, "ARRAY", true, true},
		{`{"a":1,"a":2} x`, "VALUE", true, false},
		{`{"a":1,"a":2}`, "VALUE", false, true},
	} {
		got, err := JSONPredicate(context.Background(), String(types[Text], tc.source), tc.kind, tc.unique)
		if err != nil || got != tc.want {
			t.Fatalf("%s kind=%s got=%t error=%v", tc.source, tc.kind, got, err)
		}
	}
	scan := jsonScanResult{classify: true, unique: true}
	_, err := types[JSON].scanJSON(context.Background(), `{"a":1,"a":2} x`, &scan)
	var d *sqlstate.Error
	if !errors.As(err, &d) || d.Code != "22P02" || !scan.duplicate {
		t.Fatalf("malformed tail precedence duplicate=%t err=%v", scan.duplicate, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := JSONPredicate(ctx, String(types[Text], `{}`), "VALUE", true); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestSQLJSONTypedProvider(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	jsonb, err := types[JSONB].Input(ctx, ` {"b":2,"a":1} `, -1)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BindSQLJSON(SQLJSONSerialize, types[JSON].Usage(), []Usage{types[JSONB].Usage()}, false, false)
	if err != nil {
		t.Fatal(err)
	}
	value, err := plan.Apply(ctx, []Value{jsonb}, OutputEnvironment{})
	if err != nil || value.text != `{"a": 1, "b": 2}` {
		t.Fatalf("canonical serialization=%q error=%v", value.text, err)
	}
	output, err := PlanJSONReturning(types[JSON].Usage(), types[Text].Usage())
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := output.Apply(ctx, value, InputEnvironment{}, OutputEnvironment{})
	if err != nil || rendered.text != `{"a": 1, "b": 2}` {
		t.Fatal(rendered, err)
	}
	value, err = plan.Apply(ctx, []Value{Null(types[JSONB])}, OutputEnvironment{})
	if err != nil || !value.null || value.typ != types[JSON] {
		t.Fatal(value, err)
	}
	other := testTypes()
	foreign, err := other[JSONB].Input(ctx, `{}`, -1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = plan.Apply(ctx, []Value{foreign}, OutputEnvironment{}); err == nil {
		t.Fatal("accepted foreign input generation")
	}
	inputs := make([]Usage, 128)
	values := make([]Value, 128)
	for i := range inputs {
		inputs[i] = types[Text].Usage()
		values[i] = Null(types[Text])
	}
	array, err := BindSQLJSON(SQLJSONArray, types[JSON].Usage(), inputs, true, false)
	if err != nil {
		t.Fatal(err)
	}
	value, err = array.Apply(ctx, values, OutputEnvironment{})
	if err != nil || value.text != `[]` {
		t.Fatal(value, err)
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	if value, err = array.Apply(ctx, values, OutputEnvironment{}); !errors.Is(err, context.Canceled) || value.typ != nil {
		t.Fatal(value, err)
	}
}

// Observe real primitive checkpoints, never elapsed time or a production hook.
// Restrict to the direct caller so nested output/lexer polls cannot satisfy a
// provider loop milestone.
type sqlJSONWorkContext struct {
	context.Context
	cancel    context.CancelFunc
	target    string
	at, calls int
	reached   func() bool
}

func (c *sqlJSONWorkContext) Err() error {
	var pcs [1]uintptr
	n := runtime.Callers(2, pcs[:])
	if n != 0 {
		frame, _ := runtime.CallersFrames(pcs[:n]).Next()
		if strings.HasSuffix(frame.Function, c.target) {
			c.calls++
			if c.reached != nil && c.reached() || c.reached == nil && c.calls == c.at {
				c.cancel()
			}
		}
	}
	return c.Context.Err()
}

func TestSQLJSONRecursiveUniquenessReachedCancellation(t *testing.T) {
	types := testTypes()
	scan := jsonScanResult{classify: true, unique: true}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The duplicate is decoded inside a nested object. The next scanner poll
	// observes that real uniqueness work before canceling the remaining source.
	probe := &sqlJSONWorkContext{Context: ctx, cancel: cancel, target: ".(*Descriptor).scanJSON", reached: func() bool { return scan.duplicate }}
	value, err := types[JSON].scanJSON(probe, `[{"a":1,"\u0061":2},{"tail":[1,2,3]}]`, &scan)
	if !errors.Is(err, context.Canceled) || value != (Value{}) || !scan.duplicate || scan.root != '[' || probe.calls <= 1 {
		t.Fatal("recursive work/cancellation not reached", value, err, scan, probe.calls)
	}
	// Reuse the descriptor and scan demand with fresh execution-local state:
	// equal decoded names in different objects must not retain canceled keys.
	scan = jsonScanResult{classify: true, unique: true}
	if _, err = types[JSON].scanJSON(context.Background(), `[{"a":1},{"\u0061":2}]`, &scan); err != nil || scan.duplicate || scan.root != '[' {
		t.Fatal("same scanner owner did not recover", err, scan)
	}
}

func TestSQLJSONProviderReachedCancellation(t *testing.T) {
	types := testTypes()
	inputs := []Usage{types[Text].Usage(), types[Text].Usage(), types[Text].Usage(), types[Text].Usage()}
	plan, err := BindSQLJSON(SQLJSONObject, types[JSON].Usage(), inputs, false, true)
	if err != nil {
		t.Fatal(err)
	}
	values := []Value{String(types[Text], "a"), String(types[Text], "first"), String(types[Text], "b"), String(types[Text], "second")}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Apply polls once at entry and once per member. At the second member,
	// the first key has been admitted to uniqueness and its value written.
	probe := &sqlJSONWorkContext{Context: ctx, cancel: cancel, target: ".SQLJSONPlan.Apply", at: 3}
	value, err := plan.Apply(probe, values, OutputEnvironment{})
	if !errors.Is(err, context.Canceled) || value != (Value{}) || probe.calls != 3 {
		t.Fatal("partial provider output escaped", value, err, probe.calls)
	}
	value, err = plan.Apply(context.Background(), values, OutputEnvironment{})
	if err != nil || value.text != `{"a" : "first", "b" : "second"}` {
		t.Fatal("same provider retained canceled uniqueness/writer state", value, err)
	}
}

func TestSQLJSONReturningOwnershipRebind(t *testing.T) {
	types, next := testTypes(), testTypes()
	ctx := context.Background()
	for _, id := range []ID{JSON, JSONB, Text} {
		t.Run(types[id].Name(), func(t *testing.T) {
			owned, err := PlanJSONReturning(types[JSON].Usage(), types[id].Usage())
			if err != nil {
				t.Fatal(err)
			}
			if !owned.IsJSONReturning() {
				t.Fatal("constructor conversion lost RETURNING ownership")
			}
			rebound, err := owned.Rebind(next[JSON].Usage(), next[id].Usage(), Assignment)
			if err != nil || !rebound.IsJSONReturning() {
				t.Fatalf("rebound ownership: %v", err)
			}
			fresh, err := PlanJSONReturning(next[JSON].Usage(), next[id].Usage())
			if err != nil || !rebound.Equal(fresh) {
				t.Fatalf("rebound conversion differs from fresh owner: %v", err)
			}
			value, err := next[JSON].Input(ctx, `{}`, -1)
			if err != nil {
				t.Fatal(err)
			}
			result, err := rebound.Apply(ctx, value, InputEnvironment{}, OutputEnvironment{})
			if err != nil || result.typ != next[id] || result.text != `{}` {
				t.Fatalf("rebound result: %#v, %v", result, err)
			}
			if id == Text {
				return // JSON/JSONB cases below also compare ordinary assignment provenance.
			}
			ordinary, err := PlanCoercion(types[JSON].Usage(), types[id].Usage(), Assignment)
			if err != nil || ordinary.IsJSONReturning() || ordinary.Equal(owned) {
				t.Fatalf("ordinary assignment acquired constructor ownership: %v", err)
			}
			ordinary, err = ordinary.Rebind(next[JSON].Usage(), next[id].Usage(), Assignment)
			if err != nil || ordinary.IsJSONReturning() {
				t.Fatalf("ordinary remap acquired constructor ownership: %v", err)
			}
			result, err = ordinary.Apply(ctx, value, InputEnvironment{}, OutputEnvironment{})
			if err != nil || result.typ != next[id] || result.text != `{}` {
				t.Fatalf("ordinary assignment result: %#v, %v", result, err)
			}
		})
	}
}
