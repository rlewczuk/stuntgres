package sqltype

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"stuntgres/internal/sqlstate"
)

func navigatePlan(t *testing.T, types map[ID]*Descriptor) JSONBPathQueryArrayPlan {
	t.Helper()
	p, err := BindJSONBPathQueryArray(types[JSONB].Usage(), types[JSONB].Usage(), types[JSONPath].Usage(), types[JSONB].Usage(), types[Bool].Usage())
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func navigateValues(t *testing.T, types map[ID]*Descriptor, doc, path, vars string, silent bool) []Value {
	t.Helper()
	return []Value{jsonbSetUnitInput(t, types[JSONB], doc), jsonbSetUnitInput(t, types[JSONPath], path), jsonbSetUnitInput(t, types[JSONB], vars), Boolean(types[Bool], silent)}
}
func TestJSONPathNavigateTraversal(t *testing.T) {
	types := testTypes()
	p := navigatePlan(t, types)
	for _, tc := range []struct{ doc, path, want string }{
		{`[10,20,30]`, `$[2,0 to 1,last]`, `[30, 10, 20, 30]`},
		{`[10,20]`, `$[-0.9,0.9,-1]`, `[10, 10]`},
		{`[10,20,30]`, `$[-2 to 1,2 to 0]`, `[10, 20]`},
		{`[{"x":1},[{"x":2}]]`, `$.x`, `[1]`},
		{`{"bb":2,"a":1}`, `$.*`, `[1, 2]`},
		{`{"a":[{"HR":73},{"HR":135}]}`, `strict $.**.HR`, `[73, 135]`},
		{`{"a":[{"HR":73},{"HR":135}]}`, `$.**.HR`, `[73, 135, 73, 135]`},
		{`{"a":{"b":1},"c":2}`, `strict $.**{1 to 2}`, `[{"b": 1}, 1, 2]`},
		{`{"a":{"b":1},"c":2}`, `strict $.**{last}`, `[1, 2]`},
		{`[10,20]`, `strict $.**{0}[-1 to 0]`, `[10]`},
		{`[10,20]`, `strict $.**{0}[1 to 5]`, `[20]`},
		{`[10,20]`, `strict $.**{0}[-3 to -1]`, `[]`},
		{`[10,20]`, `strict $.**{0}[2 to 5]`, `[]`},
		{`[10,20]`, `strict $.**{0}[1 to 0]`, `[]`},
		{`[[0],[1]]`, `$[$[last][0],last]`, `[[1], [1]]`},
		{`[[1,2],[3]]`, `$[*][last]`, `[2, 3]`},
		{`[{"x":1,"c":[{"y":2}]},{"x":2,"c":[{"y":0}]}]`, `$[*]?(exists(@.c[*]?(@.y > 1))).x`, `[1]`},
	} {
		v, err := p.Evaluate(context.Background(), navigateValues(t, types, tc.doc, tc.path, `{}`, false))
		if err != nil || v.text != tc.want {
			t.Fatalf("%s: %s %v; want %s", tc.path, v.text, err, tc.want)
		}
	}
	for _, mode := range []string{"strict ", "lax "} {
		for _, scalar := range []string{`1`, `null`, `"s"`, `true`} {
			for _, tc := range []struct{ doc, path, want string }{
				{scalar, `$.**{last}`, `[]`},
				{`{"a":` + scalar + `}`, `$.a.**{last}`, `[]`},
				{scalar, `$.**{0}`, `[` + scalar + `]`},
				{scalar, `$.**{0 to last}`, `[` + scalar + `]`},
				{scalar, `$.**{last}.size()`, `[]`},
				{scalar, `$.**{last}.double()`, `[]`},
				{scalar, `$.**{last}[$v]`, `[]`},
				{`{"a":` + scalar + `}`, `$.a.**{last}[$v]`, `[]`},
				{`{"a":` + scalar + `}`, `$.**{last}`, `[` + scalar + `]`},
			} {
				v, err := p.Evaluate(context.Background(), navigateValues(t, types, tc.doc, mode+tc.path, `{}`, false))
				if err != nil || v.text != tc.want {
					t.Fatal(mode, tc.doc, tc.path, v.text, err, tc.want)
				}
			}
		}
	}
	// Very deep navigation and output use work frames, not recursive document walks.
	depth := 12000
	doc := strings.Repeat(`[`, depth) + `1` + strings.Repeat(`]`, depth)
	values := navigateValues(t, types, doc, `$`+strings.Repeat(`[0]`, depth), `{}`, false)
	got, err := p.Evaluate(context.Background(), values)
	if err != nil || got.text != `[1]` {
		t.Fatal(got.text, err)
	}
	values[1] = jsonbSetUnitInput(t, types[JSONPath], `$`)
	got, err = p.Evaluate(context.Background(), values)
	if err != nil || got.text != `[`+doc+`]` {
		t.Fatal("deep output", err)
	}
}

func TestJSONPathNavigateConditions(t *testing.T) {
	types := testTypes()
	p := navigatePlan(t, types)
	conditions := []string{`(1==1)`, `(1==2)`, `("x"==1)`}
	and := [3][3]string{{"true", "false", "null"}, {"false", "false", "false"}, {"null", "false", "null"}}
	or := [3][3]string{{"true", "true", "true"}, {"true", "false", "null"}, {"true", "null", "null"}}
	for i, a := range conditions {
		for j, b := range conditions {
			for _, op := range []string{"&&", "||"} {
				want := and[i][j]
				if op == "||" {
					want = or[i][j]
				}
				v, err := p.Evaluate(context.Background(), navigateValues(t, types, `0`, a+op+b, `{}`, false))
				if err != nil || v.text != `[`+want+`]` {
					t.Fatal(a, op, b, v.text, err)
				}
			}
		}
	}
	for i, a := range conditions {
		for _, tc := range []struct{ path, want string }{
			{"!" + a, []string{"[false]", "[true]", "[null]"}[i]},
			{a + " is unknown", []string{"[false]", "[false]", "[true]"}[i]},
		} {
			v, err := p.Evaluate(context.Background(), navigateValues(t, types, `0`, tc.path, `{}`, false))
			if err != nil || v.text != tc.want {
				t.Fatal(tc.path, v.text, err)
			}
		}
	}
	for _, tc := range []struct{ doc, path, want string }{
		{`{"a":[1,2,3],"b":[3,4,"5"]}`, `strict $.a[*]>= $.b[*]`, `[null]`},
		{`{"a":[1,2,3],"b":[3,4,"5"]}`, `$.a[*]>= $.b[*]`, `[true]`},
		{`[9007199254740992,9007199254740993]`, `$[*]?(@>9007199254740992)`, `[9007199254740993]`},
		{`0`, `"z">"aa"`, `[true]`}, {`0`, `null<1`, `[false]`},
		{`{"g":[{"x":2},{"y":3}]}`, `strict $.g?((exists(@[*].x)) is unknown)`, `[[{"x": 2}, {"y": 3}]]`},
		{`"a\nb"`, `$ like_regex "a.b"`, `[false]`},
		{`"a\nb"`, `$ like_regex "a.b" flag "s"`, `[true]`},
		{`"a\nb"`, `$ like_regex "^b$" flag "m"`, `[true]`},
		{`["A.B","axb"]`, `$[*]?(@ like_regex "a.b" flag "iqsmx")`, `["A.B"]`},
	} {
		v, err := p.Evaluate(context.Background(), navigateValues(t, types, tc.doc, tc.path, `{}`, false))
		if err != nil || v.text != tc.want {
			t.Fatal(tc.path, v.text, err, tc.want)
		}
	}
}

func TestJSONPathNavigateSuppression(t *testing.T) {
	types := testTypes()
	p := navigatePlan(t, types)
	values := navigateValues(t, types, `[{"x":1},{},{"x":3}]`, `strict $[*].x`, `{}`, true)
	var nodes []*jsonbNode
	status, err := executeJSONPath(context.Background(), values[0], values[1], values[2], func(n *jsonbNode) (bool, error) { nodes = append(nodes, n); return true, nil })
	if err != nil || status.kind != jsonpathSuppressibleStop || status.diagnostic == nil || len(nodes) != 1 || nodes[0] != &values[0].jsonb.root.items[0].fields[0].value {
		t.Fatal(status, err, nodes)
	}
	v, err := p.Evaluate(context.Background(), values)
	if err != nil || v.text != `[1]` {
		t.Fatal(v.text, err)
	}
	status, err = executeJSONPath(context.Background(), values[0], jsonbSetUnitInput(t, types[JSONPath], `$[*]`), values[2], func(*jsonbNode) (bool, error) { return false, nil })
	if err != nil || status.kind != jsonpathConsumerStop {
		t.Fatal(status, err)
	}
	for _, tc := range []struct{ path, code string }{
		{`$v`, `42704`}, {`exists($v)`, `42704`}, {`$v==1`, `42704`},
		{`exists($.double())`, `0A000`}, {`$.double()>0`, `0A000`}, {`exists(1+2)`, `0A000`},
		{`$[2147483648]`, `22033`}, {`$[$v]`, `42704`},
	} {
		for _, silent := range []bool{false, true} {
			vals := navigateValues(t, types, `[1]`, tc.path, `{}`, silent)
			_, err := p.Evaluate(context.Background(), vals)
			if tc.code == "22033" && silent {
				if err != nil {
					t.Fatal(err)
				}
				continue
			}
			var d *sqlstate.Error
			if !errors.As(err, &d) || d.Code != tc.code {
				t.Fatal(tc.path, silent, err)
			}
		}
	}
	// Subscript cardinality does not stop operand execution or replace a later
	// ordinary/structural error. Inspect the executor's distinct error channels.
	for _, tc := range []struct {
		doc, path, code, message string
		structural               bool
	}{
		{`[0,0,1]`, `$[$[*]?(@ == 0 || $v == 1)]`, "42704", `could not find jsonpath variable "v"`, false},
		{`[{"i":0},{"i":1},{}]`, `strict $[$[*].i]`, "2203A", `JSON object does not contain key "i"`, true},
	} {
		vals := navigateValues(t, types, tc.doc, tc.path, `{}`, true)
		emitted := 0
		status, err := executeJSONPath(context.Background(), vals[0], vals[1], vals[2], func(*jsonbNode) (bool, error) { emitted++; return true, nil })
		if emitted != 0 {
			t.Fatal("subscript emitted before operand completed", emitted)
		}
		if tc.structural {
			if err != nil || status.kind != jsonpathSuppressibleStop {
				t.Fatal("lost structural stop", status, err)
			}
			err = status.diagnostic
		} else if status.kind == jsonpathSuppressibleStop {
			t.Fatal("ordinary error became suppressible", status)
		}
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code || diagnostic.Message != tc.message {
			t.Fatal(tc.path, status, err)
		}
	}
}

func TestJSONPathNavigateStateIsolation(t *testing.T) {
	types := testTypes()
	p := navigatePlan(t, types)
	values := navigateValues(t, types, `[["a","A"],["b"]]`, `$[*][last]?(@ like_regex "a" flag "i")`, `{}`, false)
	root, path := values[0].jsonb, values[1].jsonpath
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			for range 25 {
				v, err := p.Evaluate(context.Background(), values)
				if err != nil || v.text != `["A"]` {
					t.Errorf("state leak: %s %v", v.text, err)
					return
				}
			}
		})
	}
	wg.Wait()
	if values[0].jsonb != root || values[1].jsonpath != path {
		t.Fatal("replaced shared payload")
	}
	text, err := printJSONPath(context.Background(), path)
	if err != nil || text != values[1].text {
		t.Fatal("mutated path", text, err)
	}
}

func TestJSONPathNavigateCancellation(t *testing.T) {
	types := testTypes()
	p := navigatePlan(t, types)
	doc := `[` + strings.Repeat(`"`+strings.Repeat("a", 2048)+`",`, 2048) + `"last"]`
	for _, path := range []string{`$[*]`, `$.**`, `$[*]?(@>"` + strings.Repeat("a", 2048) + `")`, `$[*]?(@ like_regex "a+")`, `$`} {
		values := navigateValues(t, types, doc, path, `{}`, true)
		ctx := &groupingCancelContext{Context: context.Background(), at: 100}
		if _, err := p.Evaluate(ctx, values); !errors.Is(err, context.Canceled) {
			t.Fatal(path, err)
		}
		values[0] = jsonbSetUnitInput(t, types[JSONB], `[]`)
		if _, err := p.Evaluate(context.Background(), values); err != nil {
			t.Fatal("recovery", err)
		}
	}
	long := strings.Repeat("1", 20000)
	executor := jsonpathExecutor{ctx: &groupingCancelContext{Context: context.Background(), at: 3}}
	if _, err := executor.stringCompare(long, long); !errors.Is(err, context.Canceled) {
		t.Fatal("long string comparison", err)
	}
	executor.ctx = &groupingCancelContext{Context: context.Background(), at: 3}
	decimal := jsonbDecimal{digits: long}
	if _, err := executor.numberCompare(decimal, decimal); !errors.Is(err, context.Canceled) {
		t.Fatal("exact number comparison", err)
	}
	number := jsonbNode{kind: jsonbNumber, number: decimal}
	if _, err := jsonbOutput(&groupingCancelContext{Context: context.Background(), at: 3}, &number); !errors.Is(err, context.Canceled) {
		t.Fatal("numeric output", err)
	}
}

func TestJSONBPathQueryArrayPlan(t *testing.T) {
	types, fresh := testTypes(), testTypes()
	p := navigatePlan(t, types)
	values := navigateValues(t, types, `null`, `$`, `{}`, false)
	for slot := range 4 {
		copy := append([]Value(nil), values...)
		copy[slot] = Null(copy[slot].typ)
		v, err := p.Evaluate(context.Background(), copy)
		if err != nil || !v.null || v.typ != types[JSONB] {
			t.Fatal(slot, v, err)
		}
	}
	for slot := range 4 {
		copy := append([]Value(nil), values...)
		v, err := copy[slot].Retag(fresh[copy[slot].typ.ID()])
		if err != nil {
			t.Fatal(err)
		}
		copy[slot] = v
		if _, err := p.Evaluate(context.Background(), copy); err == nil {
			t.Fatal("foreign slot", slot)
		}
	}
	for _, slot := range []int{0, 1, 2} {
		copy := append([]Value(nil), values...)
		copy[slot] = Value{typ: copy[slot].typ}
		if _, err := p.Evaluate(context.Background(), copy); err == nil {
			t.Fatal("missing payload", slot)
		}
	}
	for count := range 6 {
		usages := make([]Usage, count)
		for i := range usages {
			usages[i] = types[JSONB].Usage()
		}
		if _, err := BindJSONBPathQueryArray(types[JSONB].Usage(), usages...); err == nil {
			t.Fatal("accepted signature", count)
		}
	}
	for _, vars := range []string{`[]`, `null`, `1`, `"x"`} {
		vals := navigateValues(t, types, `1`, `$`, vars, true)
		if _, err := p.Evaluate(context.Background(), vals); err == nil {
			t.Fatal("accepted variables", vars)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Evaluate(ctx, values); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	result, err := p.Evaluate(context.Background(), values)
	if err != nil || result.typ != types[JSONB] || result.text != `[null]` {
		t.Fatal(fmt.Sprint(result), err)
	}
}
