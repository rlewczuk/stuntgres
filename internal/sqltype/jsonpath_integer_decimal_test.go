package sqltype

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
)

func TestJSONPathIntegerDecimalNumeric(t *testing.T) {
	types := testTypes()
	plan := navigatePlan(t, types)
	for _, tc := range []struct{ doc, path, want string }{
		{`[-1.5,1.5,-0.00]`, `$[*].integer()`, `[-2, 2, 0]`},
		{`[-9223372036854775808,9007199254740993]`, `$[*].bigint()`, `[-9223372036854775808, 9007199254740993]`},
		{`[-1.25,1.25]`, `$[*].decimal(3,1)`, `[-1.3, 1.3]`},
		{`[-1500,1499]`, `$[*].decimal(2,-3)`, `[-2000, 1000]`},
		{`[-0.000004,0.009994]`, `$[*].decimal(3,5)`, `[0.00000, 0.00999]`},
		{`"12345678901234567890.123400"`, `$.decimal()`, `[12345678901234567890.123400]`},
	} {
		values := navigateValues(t, types, tc.doc, tc.path, `{}`, false)
		root := values[0].jsonb
		before, err := jsonbOutput(context.Background(), &root.root)
		if err != nil {
			t.Fatal(err)
		}
		got, err := plan.Evaluate(context.Background(), values, JSONPathEnvironment{})
		if err != nil || got.text != tc.want {
			t.Fatal(tc.path, got.text, err, tc.want)
		}
		after, err := jsonbOutput(context.Background(), &root.root)
		if err != nil || before != after || values[0].jsonb != root {
			t.Fatal("operand mutated", after, err)
		}
	}
	for _, tc := range []struct{ doc, path, code string }{
		{`2147483647.5`, `$.integer()`, "22036"},
		{`-9223372036854775808.5`, `$.bigint()`, "22036"},
		{`99.95`, `$.decimal(3,1)`, "22036"},
		{`1`, `$.decimal(0)`, "22023"},
		{`1`, `$.decimal(1,2147483648)`, "22036"},
	} {
		values := navigateValues(t, types, tc.doc, tc.path, `{}`, false)
		_, err := plan.Evaluate(context.Background(), values, JSONPathEnvironment{})
		var state interface{ SQLState() string }
		if !errors.As(err, &state) || state.SQLState() != tc.code {
			t.Fatal(tc.path, err)
		}
		values[3] = Boolean(types[Bool], true)
		got, err := plan.Evaluate(context.Background(), values, JSONPathEnvironment{})
		if err != nil || got.text != `[]` {
			t.Fatal("silent", got.text, err)
		}
	}
}

func TestJSONPathIntegerDecimalOwnership(t *testing.T) {
	types := testTypes()
	plan := navigatePlan(t, types)
	values := navigateValues(t, types, `[1.25,-1.25]`, `$[*].decimal(3,1)`, `{}`, false)
	payload := values[1].jsonpath
	method := payload.root.next.next
	left, right := method.left, method.right
	leftValue, rightValue := left.number, right.number
	root := values[0].jsonb
	var nodes []*jsonbNode
	_, err := executeJSONPath(context.Background(), values[0], values[1], values[2], JSONPathEnvironment{}, func(item jsonpathItem) (bool, error) {
		nodes = append(nodes, item.jsonbNode)
		return true, nil
	})
	if err != nil || len(nodes) != 2 || nodes[0] == nodes[1] {
		t.Fatal("generated node ownership", len(nodes), err)
	}
	for range 5 {
		if _, err := plan.Evaluate(context.Background(), values, JSONPathEnvironment{}); err != nil {
			t.Fatal(err)
		}
		bad := append([]Value(nil), values...)
		bad[0] = jsonbSetUnitInput(t, types[JSONB], `[1,"bad",2]`)
		if _, err := plan.Evaluate(context.Background(), bad, JSONPathEnvironment{}); err == nil {
			t.Fatal("missing error")
		}
		bad[3] = Boolean(types[Bool], true)
		got, err := plan.Evaluate(context.Background(), bad, JSONPathEnvironment{})
		if err != nil || got.text != `[1.0]` {
			t.Fatal(got.text, err)
		}
	}
	if values[0].jsonb != root || values[1].jsonpath != payload || method.left != left || method.right != right || !reflect.DeepEqual(left.number, leftValue) || !reflect.DeepEqual(right.number, rightValue) {
		t.Fatal("accepted backing changed")
	}
	for i, want := range []string{"1.3", "-1.3"} {
		got, err := jsonbOutput(context.Background(), nodes[i])
		if err != nil || got != want {
			t.Fatal("retained result mutated", got, err)
		}
	}
}

func TestJSONPathIntegerDecimalIsolation(t *testing.T) {
	types := testTypes()
	plan := navigatePlan(t, types)
	// All workers share the actual immutable parsed method and its argument nodes.
	path := jsonbSetUnitInput(t, types[JSONPath], `$.decimal(3,1)`)
	inputs := []Value{jsonbSetUnitInput(t, types[JSONB], `1.25`), jsonbSetUnitInput(t, types[JSONB], `-2.35`), jsonbSetUnitInput(t, types[JSONB], `99.95`)}
	vars := jsonbSetUnitInput(t, types[JSONB], `{}`)
	var wg sync.WaitGroup
	for worker := range 12 {
		wg.Go(func() {
			i := worker % len(inputs)
			for range 25 {
				got, err := plan.Evaluate(context.Background(), []Value{inputs[i], path, vars, Boolean(types[Bool], false)}, JSONPathEnvironment{})
				if i == 2 {
					if err == nil {
						t.Error("overflow lost")
					}
				} else if err != nil || got.text != []string{`[1.3]`, `[-2.4]`}[i] {
					t.Error("cross-execution state", got.text, err)
				}
			}
		})
	}
	wg.Wait()
}

func TestJSONPathIntegerDecimalCancellation(t *testing.T) {
	types := testTypes()
	plan := navigatePlan(t, types)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, method := range []string{"integer", "bigint", "decimal(3,1)"} {
		path := `$.` + method
		if method != "decimal(3,1)" {
			path += `()`
		}
		values := navigateValues(t, types, `1.25`, path, `{}`, true)
		_, err := plan.Evaluate(ctx, values, JSONPathEnvironment{})
		if !errors.Is(err, context.Canceled) {
			t.Fatal(method, err)
		}
		e := jsonpathExecutor{ctx: ctx}
		_, err = e.numericMethod(values[1].jsonpath.root.next, &values[0].jsonb.root)
		if !errors.Is(err, context.Canceled) {
			t.Fatal("provider canceled", method, err)
		}
	}
}
