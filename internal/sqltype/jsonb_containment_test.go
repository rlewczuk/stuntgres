package sqltype

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestJSONBContainmentProvider(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	parse := func(text string) Value {
		t.Helper()
		v, err := types[JSONB].Input(ctx, text, -1)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{`{"a":1,"b":2}`, `{"b":2}`, true},
		{`{"a":null}`, `{"missing":null}`, false},
		{`{"a":null}`, `{"a":null}`, true},
		{`{"a":{"b":1,"c":2}}`, `{"a":{"b":1}}`, true},
		{`{"a":{"b":1}}`, `{"b":1}`, false},
		{`{"a":{"b":1}}`, `{"a":{}}`, true},
		{`[1,2,3]`, `[3,1,1]`, true}, {`[1]`, `[1,1,1]`, true},
		{`[1,2,[1,3]]`, `[1,3]`, false}, {`[1,2,[1,3]]`, `[[3,1,1]]`, true},
		{`[{"a":1,"b":2}]`, `[{"a":1},{"b":2}]`, true},
		{`[{"a":1},{"b":2}]`, `[{"a":1,"b":2}]`, false},
		{`["foo","bar"]`, `"bar"`, true}, {`"bar"`, `["bar"]`, false},
		{`[[1]]`, `1`, false}, {`{"a":[1]}`, `{"a":1}`, false},
		{`1.00`, `1`, true}, {`"1"`, `1`, false},
		{`null`, `null`, true}, {`[null]`, `null`, true},
		{`true`, `true`, true}, {`false`, `true`, false},
		{`{}`, `{}`, true}, {`[]`, `[]`, true}, {`[1]`, `[]`, true},
		{`[]`, `{}`, false}, {`{}`, `[]`, false}, {`1`, `[]`, false},
		{`{"a":1,"a":2}`, `{"a":2}`, true}, {`{"a":1,"a":2}`, `{"a":1}`, false},
		{`{"\u0061":1}`, `{"a":1}`, true},
		{`9007199254740993`, `9007199254740992`, false},
		{`9007199254740993.00`, `9007199254740993`, true},
	} {
		t.Run(tc.a+" contains "+tc.b, func(t *testing.T) {
			a, b := parse(tc.a), parse(tc.b)
			beforeA, beforeB := parse(tc.a), parse(tc.b)
			// Neither output cache nor pointer identity is the containment input.
			a.text, b.text = "not JSON", "not JSON either"
			for _, op := range []string{"@>", "<@"} {
				plan, err := BindBinary(op, types[Bool].Usage(), types[JSONB].Usage(), types[JSONB].Usage())
				if err != nil {
					t.Fatal(err)
				}
				left, right := a, b
				if op == "<@" {
					left, right = b, a
				}
				for range 3 {
					got, err := plan.Evaluate(ctx, left, right)
					if err != nil || got != Boolean(types[Bool], tc.want) {
						t.Fatal(op, got, err)
					}
				}
				for _, pair := range [][2]Value{{Null(types[JSONB]), right}, {left, Null(types[JSONB])}, {Null(types[JSONB]), Null(types[JSONB])}} {
					got, err := plan.Evaluate(ctx, pair[0], pair[1])
					if err != nil || got != Null(types[Bool]) {
						t.Fatal("strict typed NULL", got, err)
					}
				}
				foreign := testTypes()[JSONB]
				if _, err := plan.Evaluate(ctx, Value{typ: foreign, jsonb: a.jsonb}, right); err == nil {
					t.Fatal("foreign runtime owner accepted")
				}
				if _, err := plan.Evaluate(ctx, Value{typ: types[JSONB]}, right); err == nil {
					t.Fatal("missing payload accepted")
				}
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				if got, err := plan.Evaluate(canceled, Null(types[JSONB]), right); got.Type() != nil || !errors.Is(err, context.Canceled) {
					t.Fatal("NULL bypassed cancellation", got, err)
				}
			}
			if !reflect.DeepEqual(a.jsonb, beforeA.jsonb) || !reflect.DeepEqual(b.jsonb, beforeB.jsonb) {
				t.Fatal("containment mutated inputs")
			}
		})
	}
	for _, op := range []string{"@>", "<@"} {
		for _, args := range [][]Usage{
			nil, {types[JSONB].Usage()}, {types[JSONB].Usage(), types[JSONB].Usage(), types[JSONB].Usage()},
			{types[Text].Usage(), types[JSONB].Usage()}, {types[JSONB].Usage(), types[Text].Usage()},
			{types[JSONB].Usage(), testTypes()[JSONB].Usage()},
		} {
			if _, err := BindBinary(op, types[Bool].Usage(), args...); err == nil {
				t.Fatal("invalid signature accepted", op, args)
			}
		}
		if _, err := BindBinary(op, types[JSONB].Usage(), types[JSONB].Usage(), types[JSONB].Usage()); err == nil {
			t.Fatal("non-BOOL result accepted")
		}
		badInput := types[JSONB].Usage()
		badInput.Typmod = 0
		if _, err := BindBinary(op, types[Bool].Usage(), badInput, types[JSONB].Usage()); err == nil {
			t.Fatal("invalid input typmod accepted")
		}
		badResult := types[Bool].Usage()
		badResult.Typmod = 0
		if _, err := BindBinary(op, badResult, types[JSONB].Usage(), types[JSONB].Usage()); err == nil {
			t.Fatal("invalid result typmod accepted")
		}
	}
}

func TestJSONBContainmentDeepTraversal(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	for _, shape := range [][2]string{{`{"a":`, `}`}, {`[`, `]`}} {
		left := strings.Repeat(shape[0], 2048) + `1.00` + strings.Repeat(shape[1], 2048)
		for _, leaf := range []string{"1", "2"} {
			right := strings.Repeat(shape[0], 2048) + leaf + strings.Repeat(shape[1], 2048)
			a, err := types[JSONB].Input(ctx, left, -1)
			if err != nil {
				t.Fatal(err)
			}
			b, err := types[JSONB].Input(ctx, right, -1)
			if err != nil {
				t.Fatal(err)
			}
			for _, op := range []string{"@>", "<@"} {
				plan, err := BindBinary(op, types[Bool].Usage(), types[JSONB].Usage(), types[JSONB].Usage())
				if err != nil {
					t.Fatal(err)
				}
				x, y := a, b
				if op == "<@" {
					x, y = y, x
				}
				got, err := plan.Evaluate(ctx, x, y)
				if err != nil || got != Boolean(types[Bool], leaf == "1") {
					t.Fatal(op, leaf, got, err)
				}
			}
		}
	}
}

func TestJSONBContainmentCancellation(t *testing.T) {
	ctx := context.Background()
	prefix := strings.Repeat("x", 8*jsonbWorkChunk)
	shapes := [][2]jsonbNode{
		{{kind: jsonbString, text: prefix + "a"}, {kind: jsonbString, text: prefix + "b"}},
		{{kind: jsonbObject, fields: []jsonbEntry{{key: prefix + "a"}}}, {kind: jsonbObject, fields: []jsonbEntry{{key: prefix + "b"}}}},
		{{kind: jsonbArray, items: make([]jsonbNode, 4096)}, {kind: jsonbArray, items: []jsonbNode{{kind: jsonbBoolean, boolean: true}}}},
	}
	deepA, deepB := jsonbNode{kind: jsonbNull}, jsonbNode{kind: jsonbNull}
	for range 128 {
		deepA = jsonbNode{kind: jsonbArray, items: []jsonbNode{deepA}}
		deepB = jsonbNode{kind: jsonbArray, items: []jsonbNode{deepB}}
	}
	shapes = append(shapes, [2]jsonbNode{deepA, deepB})
	types := testTypes()
	plan, err := BindBinary("@>", types[Bool].Usage(), types[JSONB].Usage(), types[JSONB].Usage())
	if err != nil {
		t.Fatal(err)
	}
	for _, shape := range shapes {
		a := Value{typ: types[JSONB], jsonb: &jsonbPayload{root: shape[0]}}
		b := Value{typ: types[JSONB], jsonb: &jsonbPayload{root: shape[1]}}
		beforeA, err := types[JSONB].Output(ctx, a)
		if err != nil {
			t.Fatal(err)
		}
		beforeB, err := types[JSONB].Output(ctx, b)
		if err != nil {
			t.Fatal(err)
		}
		cancel := &groupingCancelContext{Context: ctx, at: 4}
		if got, err := plan.Evaluate(cancel, a, b); got.Type() != nil || !errors.Is(err, context.Canceled) || cancel.calls != 4 {
			t.Fatal(got, err, cancel.calls)
		}
		afterA, err := types[JSONB].Output(ctx, a)
		if err != nil || beforeA != afterA {
			t.Fatal("mutated canceled lhs", err)
		}
		afterB, err := types[JSONB].Output(ctx, b)
		if err != nil || beforeB != afterB {
			t.Fatal("mutated canceled rhs", err)
		}
	}
}
