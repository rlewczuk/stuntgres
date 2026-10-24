package sqltype

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestJSONPathArithmeticValues(t *testing.T) {
	types := testTypes()
	for _, tc := range []struct {
		doc, path, digits string
		exponent, scale   int64
		negative          bool
	}{
		{`1.20`, `$ + 2.300`, `35`, -1, 3, false},
		{`1.20`, `$ - $`, ``, 0, 2, false},
		{`1.20`, `$ * 2.300`, `276`, -2, 5, false},
		{`-7.00`, `$ / 2`, `35`, -1, 16, true},
		{`-7.00`, `$ % 2`, `1`, 0, 2, true},
		{`9007199254740993`, `$ + 1`, `9007199254740994`, 0, 0, false},
		{`-0.00`, `-$`, ``, 0, 2, false},
		{`1`, `1 / 3`, `33333333333333333333`, -20, 20, false},
		{`1`, `5e-8192 * 1e-8192`, `1`, -16383, 16383, false},
	} {
		values := navigateValues(t, types, tc.doc, tc.path, `{}`, false)
		var got jsonbDecimal
		count := 0
		status, err := executeJSONPath(context.Background(), values[0], values[1], values[2], JSONPathEnvironment{}, func(item jsonpathItem) (bool, error) {
			count++
			if item.itemKind() != jsonbNumber {
				t.Fatal("nonnumeric arithmetic result")
			}
			got = item.number
			return true, nil
		})
		want := jsonbDecimal{digits: tc.digits, exponent: tc.exponent, scale: tc.scale, negative: tc.negative}
		if err != nil || status.kind != jsonpathComplete || count != 1 || got != want {
			t.Fatalf("%s: got=%+v count=%d status=%v err=%v want=%+v", tc.path, got, count, status, err, want)
		}
	}
}

func TestJSONPathArithmeticOperands(t *testing.T) {
	types := testTypes()
	values := navigateValues(t, types, `[1.20,-2.00]`, `+$`, `{}`, false)
	index := 0
	status, err := executeJSONPath(context.Background(), values[0], values[1], values[2], JSONPathEnvironment{}, func(item jsonpathItem) (bool, error) {
		if item.jsonbNode != &values[0].jsonb.root.items[index] {
			t.Fatal("unary plus copied numeric input")
		}
		index++
		return true, nil
	})
	if err != nil || status.kind != jsonpathComplete || index != 2 {
		t.Fatal(status, err, index)
	}
	// Temporal items are not JSON numbers and must not be coerced, even when
	// their descriptor can participate in ordinary SQL arithmetic.
	root := &jsonbNode{kind: jsonbNull}
	e := jsonpathExecutor{ctx: context.Background(), root: root, bindings: []jsonpathBinding{{name: "v", item: jsonpathItem{temporal: &Value{}}}}}
	for _, path := range []string{`$v + 1`, `+$v`} {
		p := jsonbSetUnitInput(t, types[JSONPath], path)
		status, err := e.execute(p, func(jsonpathItem) (bool, error) { t.Fatal("temporal arithmetic emitted a value"); return true, nil })
		var diagnostic *sqlstate.Error
		if err != nil || status.kind != jsonpathSuppressibleStop || !errors.As(status.diagnostic, &diagnostic) {
			t.Fatal(status, err)
		}
		code := "22038"
		if path == `+$v` {
			code = "2203B"
		}
		if diagnostic.Code != code {
			t.Fatal(diagnostic)
		}
	}
}

func TestJSONPathArithmeticDemand(t *testing.T) {
	types := testTypes()
	for _, tc := range []struct {
		doc, path, code string
		count           int
		structural      bool
	}{
		{`[1,2]`, `$[*] + $v`, "42704", 0, false},
		{`[1,2]`, `$[*] + (1 / 0)`, "22012", 0, true},
		{`[1,2]`, `(1 / 0) + $v`, "22012", 0, true},
		{`[1]`, `+ $[0,$v]`, "42704", 0, false},
		{`[1]`, `strict + $[0,2]`, "22033", 0, true},
		{`[1,"x",3]`, `+$`, "2203B", 1, true},
		{`[1,2]`, `strict (-$[*])[0]`, "22039", 0, true},
		{`1`, `1e131071 * 10`, "22003", 0, true},
	} {
		values := navigateValues(t, types, tc.doc, tc.path, `{}`, true)
		count := 0
		status, err := executeJSONPath(context.Background(), values[0], values[1], values[2], JSONPathEnvironment{}, func(jsonpathItem) (bool, error) { count++; return true, nil })
		diagnosticErr := err
		if tc.structural {
			if err != nil || status.kind != jsonpathSuppressibleStop {
				t.Fatal(tc.path, status, err)
			}
			diagnosticErr = status.diagnostic
		}
		var diagnostic *sqlstate.Error
		if !errors.As(diagnosticErr, &diagnostic) || diagnostic.Code != tc.code || count != tc.count {
			t.Fatal(tc.path, status, err, count)
		}
	}
	// A consumer stop or ordinary continuation failure must propagate without
	// processing the next unary item or converting it into a silent stop.
	values := navigateValues(t, types, `[1,"x"]`, `-$`, `{}`, true)
	status, err := executeJSONPath(context.Background(), values[0], values[1], values[2], JSONPathEnvironment{}, func(jsonpathItem) (bool, error) { return false, nil })
	if err != nil || status.kind != jsonpathConsumerStop {
		t.Fatal(status, err)
	}
	consumerErr := errors.New("consumer failure")
	status, err = executeJSONPath(context.Background(), values[0], values[1], values[2], JSONPathEnvironment{}, func(jsonpathItem) (bool, error) { return true, consumerErr })
	if !errors.Is(err, consumerErr) || status.kind == jsonpathSuppressibleStop {
		t.Fatal(status, err)
	}
}

func TestJSONPathArithmeticStateIsolation(t *testing.T) {
	types := testTypes()
	plan := navigatePlan(t, types)
	// Filter the restored positive values, not the inner unary-minus operand.
	values := navigateValues(t, types, `[1.20,2.30]`, `(-(-$[*]))?(@ + $n > 3)`, `{"n":2}`, false)
	root, path, vars := values[0].jsonb, values[1].jsonpath, values[2].jsonb
	original := root.root.items[0].number
	var workers sync.WaitGroup
	for range 12 {
		workers.Go(func() {
			for range 25 {
				v, err := plan.Evaluate(context.Background(), values, JSONPathEnvironment{})
				if err != nil || v.text != `[1.20, 2.30]` {
					t.Errorf("arithmetic state leak: %s %v", v.text, err)
					return
				}
			}
		})
	}
	workers.Wait()
	if values[0].jsonb != root || values[1].jsonpath != path || values[2].jsonb != vars || root.root.items[0].number != original {
		t.Fatal("mutated shared arithmetic input")
	}
	text, err := printJSONPath(context.Background(), path)
	if err != nil || text != values[1].text {
		t.Fatal("mutated arithmetic AST", text, err)
	}
}

func TestJSONPathArithmeticCancellation(t *testing.T) {
	types := testTypes()
	plan := navigatePlan(t, types)
	for _, path := range []string{`+$`, `-$`, `$[*]?(@ * @ > 0)`, `$[*] + 1`} {
		values := navigateValues(t, types, `[`+strings.Repeat(`1,`, 511)+`1]`, path, `{}`, true)
		ctx := &groupingCancelContext{Context: context.Background(), at: 100}
		if _, err := plan.Evaluate(ctx, values, JSONPathEnvironment{}); !errors.Is(err, context.Canceled) {
			t.Fatal(path, err)
		}
	}
	values := navigateValues(t, types, `[1,2]`, `-$`, `{}`, true)
	ctx, cancel := context.WithCancel(context.Background())
	status, err := executeJSONPath(ctx, values[0], values[1], values[2], JSONPathEnvironment{}, func(jsonpathItem) (bool, error) { cancel(); return true, nil })
	if !errors.Is(err, context.Canceled) || status.kind == jsonpathSuppressibleStop {
		t.Fatal("cancellation was suppressed", status, err)
	}
	if v, err := plan.Evaluate(context.Background(), values, JSONPathEnvironment{}); err != nil || v.text != `[-1, -2]` {
		t.Fatal("cancellation leaked into next execution", v.text, err)
	}
}
