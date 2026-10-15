package sqltype

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

func jsonbConcatDeleteUnitPlan(t *testing.T, types map[ID]*Descriptor, name string, rhs ID) BinaryPlan {
	t.Helper()
	p, err := BindBinary(name, types[JSONB].Usage(), types[JSONB].Usage(), types[rhs].Usage())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestJSONBConcatDeleteDescriptorAuthority(t *testing.T) {
	types, fresh := testTypes(), testTypes()
	for _, tc := range []struct {
		name string
		rhs  ID
	}{{"||", JSONB}, {"-", Text}, {"-", Int4}} {
		p := jsonbConcatDeleteUnitPlan(t, types, tc.name, tc.rhs)
		if !p.Immutable() || p.Name() != tc.name || p.ResultUsage() != types[JSONB].Usage() {
			t.Fatal(p)
		}
		args := []Usage{types[JSONB].Usage(), types[tc.rhs].Usage()}
		for _, bad := range []struct {
			result Usage
			args   []Usage
		}{
			{fresh[JSONB].Usage(), args}, {types[Text].Usage(), args},
			{types[JSONB].Usage(), args[:1]},
			{types[JSONB].Usage(), []Usage{fresh[JSONB].Usage(), args[1]}},
			{types[JSONB].Usage(), []Usage{types[JSON].Usage(), args[1]}},
			{types[JSONB].Usage(), []Usage{args[0], types[Bool].Usage()}},
			{types[JSONB].Usage(), []Usage{{Type: types[JSONB], Typmod: 1}, args[1]}},
			{types[JSONB].Usage(), []Usage{args[0], {Type: types[tc.rhs], Typmod: 1}}},
		} {
			if _, err := BindBinary(tc.name, bad.result, bad.args...); err == nil {
				t.Fatal("accepted invalid signature", tc, bad)
			}
		}
		for _, values := range [][]Value{
			{Null(fresh[JSONB]), Null(types[tc.rhs])},
			{Null(types[JSONB]), Null(fresh[tc.rhs])},
			{Null(types[JSONB])},
			{Value{typ: types[JSONB]}, Value{typ: types[tc.rhs]}},
		} {
			if got, err := p.Evaluate(context.Background(), values...); err == nil || got != (Value{}) {
				t.Fatal("accepted invalid values", got, err)
			}
		}
		for _, values := range [][]Value{
			{Null(types[JSONB]), Null(types[tc.rhs])},
			{jsonbSetUnitInput(t, types[JSONB], "null"), Null(types[tc.rhs])},
		} {
			got, err := p.Evaluate(context.Background(), values...)
			if err != nil || got != Null(types[JSONB]) {
				t.Fatal(got, err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if got, err := p.Evaluate(ctx, values...); !errors.Is(err, context.Canceled) || got != (Value{}) {
				t.Fatal("strictness hid cancellation", got, err)
			}
		}
	}
}

func TestJSONBConcatDeleteTypedContracts(t *testing.T) {
	types := testTypes()
	for _, tc := range []struct {
		name                       string
		rhs                        ID
		left, right, want, message string
	}{
		{"||", JSONB, `{"a":{"x":1},"b":2}`, `{"a":{"y":3}}`, `{"a": {"y": 3}, "b": 2}`, ""},
		{"||", JSONB, `[1,[2]]`, `[[3],4]`, `[1, [2], [3], 4]`, ""},
		{"||", JSONB, `3`, `4`, `[3, 4]`, ""},
		{"||", JSONB, `{}`, `[]`, `[{}]`, ""},
		{"||", JSONB, `[]`, `{}`, `[{}]`, ""},
		{"||", JSONB, `null`, `[1]`, `[null, 1]`, ""},
		{"-", Text, `{"a":null,"b":1.00}`, `a`, `{"b": 1.00}`, ""},
		{"-", Text, `["b",1,"b",["b"],{"b":2},null]`, `b`, `[1, ["b"], {"b": 2}, null]`, ""},
		{"-", Text, `["é","é"]`, `é`, `[]`, ""},
		{"-", Int4, `[1,2,3]`, `-1`, `[1, 2]`, ""},
		{"-", Int4, `[1,2,3]`, `-3`, `[2, 3]`, ""},
		{"-", Int4, `[1,2,3]`, `-4`, `[1, 2, 3]`, ""},
		{"-", Int4, `[1,2,3]`, `-2147483648`, `[1, 2, 3]`, ""},
		{"-", Int4, `[1,2,3]`, `2147483647`, `[1, 2, 3]`, ""},
		{"-", Int4, `[]`, `0`, `[]`, ""},
		{"-", Int4, `{}`, `0`, "", "cannot delete from object using integer index"},
		{"-", Text, `null`, `a`, "", "cannot delete from scalar"},
		{"-", Int4, `true`, `0`, "", "cannot delete from scalar"},
		{"-", Text, `"s"`, `s`, "", "cannot delete from scalar"},
		{"-", Int4, `1`, `0`, "", "cannot delete from scalar"},
	} {
		t.Run(tc.name+tc.left+tc.right, func(t *testing.T) {
			p := jsonbConcatDeleteUnitPlan(t, types, tc.name, tc.rhs)
			left, right := jsonbSetUnitInput(t, types[JSONB], tc.left), jsonbSetUnitInput(t, types[tc.rhs], tc.right)
			got, err := p.Evaluate(context.Background(), left, right)
			if tc.message != "" {
				var d *sqlstate.Error
				want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: tc.message}
				if !errors.As(err, &d) || *d != want || got != (Value{}) {
					t.Fatal(got, err, want)
				}
			} else if err != nil || got.text != tc.want || got.typ != types[JSONB] || got.jsonb == nil {
				t.Fatal(got, err, tc.want)
			}
			for _, v := range []Value{left, right} {
				if v.jsonb == nil {
					continue
				}
				text, err := jsonbOutput(context.Background(), &v.jsonb.root)
				if err != nil || text != v.text {
					t.Fatal("input mutated", text, err)
				}
			}
		})
	}
}

func TestJSONBConcatDeleteImmutableSharing(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	left := jsonbSetUnitInput(t, types[JSONB], `{"a":[1],"b":[2]}`)
	right := jsonbSetUnitInput(t, types[JSONB], `{"a":[3]}`)
	concat := jsonbConcatDeleteUnitPlan(t, types, "||", JSONB)
	got, err := concat.Evaluate(ctx, left, right)
	if err != nil || got.text != `{"a": [3], "b": [2]}` {
		t.Fatal(got, err)
	}
	if &got.jsonb.root.fields[0] == &right.jsonb.root.fields[0] {
		t.Fatal("top level storage borrowed")
	}
	if &got.jsonb.root.fields[0].value.items[0] != &right.jsonb.root.fields[0].value.items[0] || &got.jsonb.root.fields[1].value.items[0] != &left.jsonb.root.fields[1].value.items[0] {
		t.Fatal("immutable children copied")
	}
	deletion := jsonbConcatDeleteUnitPlan(t, types, "-", Text)
	got, err = deletion.Evaluate(ctx, left, String(types[Text], "a"))
	if err != nil || got.text != `{"b": [2]}` || &got.jsonb.root.fields[0].value.items[0] != &left.jsonb.root.fields[1].value.items[0] {
		t.Fatal("deletion subtree sharing", got, err)
	}
	for _, tc := range []struct {
		name        string
		rhs         ID
		source, arg string
	}{
		{"-", Text, `{"a":1}`, "missing"}, {"-", Text, `[1,"a"]`, "missing"},
		{"-", Int4, `[1]`, "-2147483648"}, {"-", Int4, `[]`, "0"},
		{"||", JSONB, `{"a":1}`, `{}`}, {"||", JSONB, `[1]`, `[]`},
	} {
		p := jsonbConcatDeleteUnitPlan(t, types, tc.name, tc.rhs)
		source := jsonbSetUnitInput(t, types[JSONB], tc.source)
		arg := jsonbSetUnitInput(t, types[tc.rhs], tc.arg)
		got, err := p.Evaluate(ctx, source, arg)
		if err != nil || got != source {
			t.Fatal("no-op republished", got, err)
		}
	}
}

func TestJSONBConcatDeletePhaseCancellation(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	long := strings.Repeat("k", 32*jsonbWorkChunk)
	wideArray := `["drop",` + strings.Repeat(`0,`, 16*jsonbWorkChunk) + `"` + long + `"]`
	wideFields := make([]jsonbEntry, 16*jsonbWorkChunk)
	for i := range wideFields {
		wideFields[i] = jsonbEntry{key: string(rune(i + 32)), value: jsonbNode{kind: jsonbNull}}
	}
	if err := sortJSONBFields(ctx, wideFields); err != nil {
		t.Fatal(err)
	}
	root := jsonbNode{kind: jsonbObject, fields: wideFields}
	text, err := jsonbOutput(ctx, &root)
	if err != nil {
		t.Fatal(err)
	}
	wideObject := Value{typ: types[JSONB], text: text, jsonb: &jsonbPayload{root: root}}
	for _, tc := range []struct {
		name   string
		rhs    ID
		source Value
		arg    string
		at     int
	}{
		{"||", JSONB, wideObject, `{"z":1}`, 20},
		{"||", JSONB, jsonbSetUnitInput(t, types[JSONB], `{"`+long+`":1}`), `{"` + long + `":2}`, 10},
		{"||", JSONB, jsonbSetUnitInput(t, types[JSONB], wideArray), `[1]`, 10},
		{"-", Text, jsonbSetUnitInput(t, types[JSONB], `["`+long+`"]`), long, 10},
		{"-", Text, jsonbSetUnitInput(t, types[JSONB], wideArray), "drop", 20},
		{"-", Int4, jsonbSetUnitInput(t, types[JSONB], wideArray), "0", 20},
		{"-", Int4, jsonbSetUnitInput(t, types[JSONB], `[0,"`+strings.Repeat("x", 64*jsonbWorkChunk)+`"]`), "0", 20},
		{"-", Text, jsonbSetUnitInput(t, types[JSONB], `{}`), "missing", 2},
		{"||", JSONB, jsonbSetUnitInput(t, types[JSONB], `{}`), `{}`, 2},
	} {
		p := jsonbConcatDeleteUnitPlan(t, types, tc.name, tc.rhs)
		arg := jsonbSetUnitInput(t, types[tc.rhs], tc.arg)
		cancel := &groupingCancelContext{Context: ctx, at: tc.at}
		got, err := p.Evaluate(cancel, tc.source, arg)
		if !errors.Is(err, context.Canceled) || got != (Value{}) {
			t.Fatal("cancellation published value", got, err, cancel.calls)
		}
		out, err := jsonbOutput(ctx, &tc.source.jsonb.root)
		if err != nil || out != tc.source.text {
			t.Fatal("cancellation mutated input", err)
		}
	}
}
