package sqltype

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestJSONBExistsProvider(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	for _, tc := range []struct {
		document, operator, request string
		want                        bool
	}{
		{`{"a":null,"b":"value"}`, "?", "a", true},
		{`{"a":null,"b":"value"}`, "?", "value", false},
		{`{"outer":{"a":1}}`, "?", "a", false},
		{`["a",1,true,null,["nested"],{"k":"v"}]`, "?", "a", true},
		{`[1,true,null,["nested"],{"k":"v"}]`, "?", "nested", false},
		{`"a"`, "?", "a", true}, {`1`, "?", "1", false},
		{`{"a":1}`, "?|", "[-1:0][3:4]={{a,NULL},{a,b}}", true},
		{`{"a":1}`, "?&", "[-1:0][3:4]={{a,NULL},{a,b}}", false},
		{`{"a":1}`, "?&", "{a,a,NULL}", true},
		{`{}`, "?|", "{}", false}, {`{}`, "?&", "{}", true},
		{`{}`, "?|", "{NULL}", false}, {`{}`, "?&", "{NULL}", true},
	} {
		t.Run(tc.operator+tc.document+tc.request, func(t *testing.T) {
			right := types[Text]
			if tc.operator != "?" {
				right = types[1009]
			}
			plan, err := BindBinary(tc.operator, types[Bool].Usage(), types[JSONB].Usage(), right.Usage())
			if err != nil {
				t.Fatal(err)
			}
			doc, err := types[JSONB].Input(ctx, tc.document, -1)
			if err != nil {
				t.Fatal(err)
			}
			key, err := right.Input(ctx, tc.request, -1)
			if err != nil {
				t.Fatal(err)
			}
			before, err := types[JSONB].Output(ctx, doc)
			if err != nil {
				t.Fatal(err)
			}
			got, err := plan.Evaluate(ctx, doc, key)
			if err != nil || got != Boolean(types[Bool], tc.want) {
				t.Fatal(got, err)
			}
			for _, pair := range [][2]Value{{Null(types[JSONB]), key}, {doc, Null(right)}} {
				got, err = plan.Evaluate(ctx, pair[0], pair[1])
				if err != nil || got != Null(types[Bool]) {
					t.Fatal("strict NULL", got, err)
				}
			}
			after, err := types[JSONB].Output(ctx, doc)
			if err != nil || before != after {
				t.Fatal("mutated document", err)
			}
			fresh := testTypes()
			foreign, err := doc.Retag(fresh[JSONB])
			if err != nil {
				t.Fatal(err)
			}
			if _, err := plan.Evaluate(ctx, foreign, key); err == nil {
				t.Fatal("foreign owner accepted")
			}
			if _, err := BindBinary(tc.operator, types[Text].Usage(), types[JSONB].Usage(), right.Usage()); err == nil {
				t.Fatal("non-BOOL result")
			}
			if _, err := BindBinary(tc.operator, types[Bool].Usage(), types[JSONB].Usage()); err == nil {
				t.Fatal("wrong arity")
			}
		})
	}
}

func TestJSONBExistsCancellation(t *testing.T) {
	ctx := context.Background()
	prefix := strings.Repeat("x", 8*jsonbWorkChunk)
	for _, root := range []jsonbNode{
		{kind: jsonbString, text: prefix + "a"},
		{kind: jsonbObject, fields: []jsonbEntry{{key: prefix + "a"}}},
		{kind: jsonbArray, items: []jsonbNode{{kind: jsonbString, text: prefix + "a"}}},
	} {
		cancel := &groupingCancelContext{Context: ctx, at: 4}
		if found, err := jsonbExists(cancel, &root, prefix+"b"); found || !errors.Is(err, context.Canceled) || cancel.calls != 4 {
			t.Fatal(found, err, cancel.calls)
		}
		if found, err := jsonbExists(ctx, &root, prefix+"a"); !found || err != nil {
			t.Fatal(found, err)
		}
	}
	root := jsonbNode{kind: jsonbArray, items: make([]jsonbNode, 4*jsonbWorkChunk)}
	cancel := &groupingCancelContext{Context: ctx, at: 4}
	if found, err := jsonbExists(cancel, &root, "missing"); found || !errors.Is(err, context.Canceled) {
		t.Fatal(found, err)
	}
	types := testTypes()
	plan, err := BindBinary("?|", types[Bool].Usage(), types[JSONB].Usage(), types[1009].Usage())
	if err != nil {
		t.Fatal(err)
	}
	doc, err := types[JSONB].Input(ctx, `{}`, -1)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := types[1009].Input(ctx, "{"+strings.Repeat("NULL,", 4095)+"NULL}", -1)
	if err != nil {
		t.Fatal(err)
	}
	cancel = &groupingCancelContext{Context: ctx, at: 4}
	if got, err := plan.Evaluate(cancel, doc, keys); got.Type() != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(got, err)
	}
	canceled, stop := context.WithCancel(ctx)
	stop()
	if _, err := plan.Evaluate(canceled, Null(types[JSONB]), keys); !errors.Is(err, context.Canceled) {
		t.Fatal("NULL bypassed cancellation", err)
	}
}
