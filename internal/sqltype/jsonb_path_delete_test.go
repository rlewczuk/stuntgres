package sqltype

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

func jsonbPathDeleteUnitPlan(t *testing.T, types map[ID]*Descriptor) BinaryPlan {
	t.Helper()
	p, err := BindBinary("#-", types[JSONB].Usage(), types[JSONB].Usage(), types[1009].Usage())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestJSONBPathDeleteDescriptorAuthority(t *testing.T) {
	types, fresh := testTypes(), testTypes()
	ctx := context.Background()
	p := jsonbPathDeleteUnitPlan(t, types)
	if p.Name() != "#-" || p.ResultUsage() != types[JSONB].Usage() || !p.Immutable() {
		t.Fatal(p)
	}
	args := []Usage{types[JSONB].Usage(), types[1009].Usage()}
	for _, tc := range []struct {
		result Usage
		args   []Usage
	}{
		{fresh[JSONB].Usage(), args},
		{types[Text].Usage(), args},
		{types[JSONB].Usage(), args[:1]},
		{types[JSONB].Usage(), append(append([]Usage(nil), args...), types[Text].Usage())},
		{types[JSONB].Usage(), []Usage{fresh[JSONB].Usage(), args[1]}},
		{types[JSONB].Usage(), []Usage{types[JSON].Usage(), args[1]}},
		{types[JSONB].Usage(), []Usage{args[0], types[Text].Usage()}},
		{types[JSONB].Usage(), []Usage{{Type: types[JSONB], Typmod: 1}, args[1]}},
		{types[JSONB].Usage(), []Usage{args[0], {Type: types[1009], Typmod: 1}}},
	} {
		if _, err := BindBinary("#-", tc.result, tc.args...); err == nil {
			t.Fatal("accepted invalid signature", tc)
		}
	}
	for _, element := range []*Descriptor{nil, types[Int4]} {
		bad := *types[1009]
		bad.element = element
		if _, err := BindBinary("#-", types[JSONB].Usage(), args[0], bad.Usage()); err == nil {
			t.Fatal("accepted invalid path element", element)
		}
	}
	for _, values := range [][]Value{
		{Null(fresh[JSONB]), Null(types[1009])}, {Null(types[JSONB]), Null(fresh[1009])},
		{Null(types[JSONB])}, {Value{typ: types[JSONB]}, Value{typ: types[1009]}},
	} {
		if got, err := p.Evaluate(ctx, values...); err == nil || got != (Value{}) {
			t.Fatal("accepted invalid values", got, err)
		}
	}
	for _, values := range [][]Value{
		{Null(types[JSONB]), jsonbSetUnitInput(t, types[1009], "{NULL}")},
		{jsonbSetUnitInput(t, types[JSONB], "1"), Null(types[1009])},
		{Null(types[JSONB]), Null(types[1009])},
	} {
		got, err := p.Evaluate(ctx, values...)
		if err != nil || got != Null(types[JSONB]) {
			t.Fatal(got, err)
		}
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if got, err := p.Evaluate(canceled, values...); !errors.Is(err, context.Canceled) || got != (Value{}) {
			t.Fatal("strictness hid cancellation", got, err)
		}
	}
}

func TestJSONBPathDeleteTypedContracts(t *testing.T) {
	types := testTypes()
	p := jsonbPathDeleteUnitPlan(t, types)
	for _, tc := range []struct{ doc, path, want, code, msg string }{
		{`["a",{"b":1}]`, "{1,b}", `["a", {}]`, "", ""},
		{`{"a":[{"x":1},{"x":2}],"keep":3}`, "{a,-1,x}", `{"a": [{"x": 1}, {}], "keep": 3}`, "", ""},
		{`{"a":[{"x":1},{"x":2}],"keep":3}`, "{a,-1}", `{"a": [{"x": 1}], "keep": 3}`, "", ""},
		{`[[1,2],[3,4]]`, "{-1,-2}", `[[1, 2], [4]]`, "", ""},
		{`{"-1":1,"0":2,"a":3}`, "{-1}", `{"0": 2, "a": 3}`, "", ""},
		{`{"a":null,"b":1.00}`, "{a}", `{"b": 1.00}`, "", ""},
		{`[1]`, "{0}", `[]`, "", ""}, {`[1]`, "{-1}", `[]`, "", ""}, {`{"a":1}`, "{a}", `{}`, "", ""},
		{`{"":1,"keep":2}`, `{""}`, `{"keep": 2}`, "", ""},
		{`{"a,b":1,"keep":2}`, `{"a,b"}`, `{"keep": 2}`, "", ""},
		{`{"\u00e9":1,"keep":2}`, "{é}", `{"keep": 2}`, "", ""},
		{`{"a":1,"a":2,"b":3}`, "{b}", `{"a": 2}`, "", ""},
		{`{"a\"b":1,"keep":2}`, `{"a\"b"}`, `{"keep": 2}`, "", ""},
		{`{"a":{"b":1}}`, "{missing,b}", `{"a": {"b": 1}}`, "", ""},
		{`{"a":{"b":1}}`, "{a,missing}", `{"a": {"b": 1}}`, "", ""},
		{`{"a":1}`, "{a,x,NULL}", `{"a": 1}`, "", ""},
		{`{"a":1}`, "{missing,NULL}", `{"a": 1}`, "", ""},
		{`{"a":1}`, "{}", `{"a": 1}`, "", ""}, {`[1,2]`, "{}", `[1, 2]`, "", ""},
		{`{}`, "{NULL}", `{}`, "", ""}, {`[]`, "{NULL}", `[]`, "", ""},
		{`[1,2]`, "{2}", `[1, 2]`, "", ""}, {`[1,2]`, "{-3}", `[1, 2]`, "", ""},
		{`[1,2]`, "{-2}", `[2]`, "", ""}, {`[1,2]`, "{-2147483648}", `[1, 2]`, "", ""},
		{`[1,2]`, "[-2:-2]={-1}", `[1]`, "", ""},
		{`[1,2]`, `{" +01"}`, `[1]`, "", ""}, {`[1,2]`, "{\"\t-01\"}", `[1]`, "", ""},
		{`{}`, "{{a,b}}", "", "2202E", "wrong number of array subscripts"},
		{`1`, "{{a,b}}", "", "2202E", "wrong number of array subscripts"},
		{`1`, "{}", "", "22023", "cannot delete path in scalar"},
		{`"s"`, "{a}", "", "22023", "cannot delete path in scalar"},
		{`true`, "{a}", "", "22023", "cannot delete path in scalar"},
		{`null`, "{}", "", "22023", "cannot delete path in scalar"},
		{`{"a":1}`, "{a,NULL}", "", "22004", "path element at position 2 is null"},
		{`{"a":1}`, "[-2:-1]={a,NULL}", "", "22004", "path element at position 2 is null"},
		{`[1]`, "{x}", "", "22P02", `path element at position 1 is not an integer: "x"`},
		{`{"a":[]}`, "{a,bad}", "", "22P02", `path element at position 2 is not an integer: "bad"`},
	} {
		t.Run(tc.doc+tc.path, func(t *testing.T) {
			source := jsonbSetUnitInput(t, types[JSONB], tc.doc)
			path := jsonbSetUnitInput(t, types[1009], tc.path)
			got, err := p.Evaluate(context.Background(), source, path)
			if tc.code != "" {
				var d *sqlstate.Error
				want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.msg}
				if !errors.As(err, &d) || *d != want || got != (Value{}) {
					t.Fatal(got, err, want)
				}
			} else if err != nil || got.text != tc.want || got.typ != types[JSONB] || got.jsonb == nil {
				t.Fatal(got, err, tc.want)
			}
			out, err := jsonbOutput(context.Background(), &source.jsonb.root)
			if err != nil || out != source.text {
				t.Fatal("source mutated", out, err)
			}
		})
	}
	for _, index := range []string{"", "1 ", "1x", "2147483648", "-2147483649"} {
		path, err := ArrayValue(types[1009], []ArrayDimension{{Lower: 1, Length: 1}}, []Value{String(types[Text], index)})
		if err != nil {
			t.Fatal(err)
		}
		got, err := p.Evaluate(context.Background(), jsonbSetUnitInput(t, types[JSONB], "[1,2]"), path)
		var d *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: `path element at position 1 is not an integer: "` + index + `"`}
		if !errors.As(err, &d) || *d != want || got != (Value{}) {
			t.Fatal(got, err, want)
		}
	}
}

func TestJSONBPathDeleteImmutableSharing(t *testing.T) {
	types := testTypes()
	p := jsonbPathDeleteUnitPlan(t, types)
	source := jsonbSetUnitInput(t, types[JSONB], `{"a":{"x":[1,2]},"b":[3]}`)
	got, err := p.Evaluate(context.Background(), source, jsonbSetUnitInput(t, types[1009], "{a,x,0}"))
	if err != nil || got.text != `{"a": {"x": [2]}, "b": [3]}` {
		t.Fatal(got.text, err)
	}
	old, changed := source.jsonb.root, got.jsonb.root
	if &old.fields[0] == &changed.fields[0] || &old.fields[0].value.fields[0] == &changed.fields[0].value.fields[0] || &old.fields[0].value.fields[0].value.items[1] == &changed.fields[0].value.fields[0].value.items[0] {
		t.Fatal("changed ancestors borrowed")
	}
	if &old.fields[1].value.items[0] != &changed.fields[1].value.items[0] {
		t.Fatal("untouched subtree copied")
	}
	for _, path := range []string{"{}", "{missing,x}", "{a,x,9}"} {
		got, err := p.Evaluate(context.Background(), source, jsonbSetUnitInput(t, types[1009], path))
		if err != nil || got != source {
			t.Fatal("no-op republished", got, err)
		}
	}
	if source.text != `{"a": {"x": [1, 2]}, "b": [3]}` {
		t.Fatal("source changed")
	}
}

func TestJSONBPathDeleteIterativeDepthAndCancellation(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	p := jsonbPathDeleteUnitPlan(t, types)
	depth := 4096
	root := jsonbNode{kind: jsonbNumber, number: jsonbDecimal{digits: "1"}}
	steps := make([]Value, depth)
	for i := range depth {
		root = jsonbNode{kind: jsonbObject, fields: []jsonbEntry{{key: "a", value: root}}}
		steps[i] = String(types[Text], "a")
	}
	text, err := jsonbOutput(ctx, &root)
	if err != nil {
		t.Fatal(err)
	}
	source := Value{typ: types[JSONB], text: text, jsonb: &jsonbPayload{root: root}}
	path, err := OwnedArrayValue(types[1009], []ArrayDimension{{Lower: -4, Length: int32(depth)}}, steps)
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.Evaluate(ctx, source, path)
	if err != nil || got.text != strings.Repeat(`{"a": `, depth-1)+"{}"+strings.Repeat("}", depth-1) {
		t.Fatal("deep iterative deletion", err)
	}
	longKey := strings.Repeat("k", 16*jsonbWorkChunk)
	wide := jsonbSetUnitInput(t, types[JSONB], "["+strings.Repeat("0,", 16*jsonbWorkChunk)+"0]")
	longObject := jsonbSetUnitInput(t, types[JSONB], `{"`+longKey+`":1}`)
	largeOutput := jsonbSetUnitInput(t, types[JSONB], `[0,"`+strings.Repeat("x", 32*jsonbWorkChunk)+`"]`)
	for _, tc := range []struct {
		source Value
		steps  []Value
		at     int
	}{
		{source, steps, 10}, {wide, []Value{String(types[Text], "0")}, 10},
		{longObject, []Value{String(types[Text], longKey)}, 10},
		{wide, []Value{String(types[Text], strings.Repeat(" ", 16*jsonbWorkChunk)+"0")}, 10},
		{largeOutput, []Value{String(types[Text], "0")}, 20},
	} {
		path, err := ArrayValue(types[1009], []ArrayDimension{{Lower: 1, Length: int32(len(tc.steps))}}, tc.steps)
		if err != nil {
			t.Fatal(err)
		}
		cancel := &groupingCancelContext{Context: ctx, at: tc.at}
		got, err := p.Evaluate(cancel, tc.source, path)
		if !errors.Is(err, context.Canceled) || got != (Value{}) {
			t.Fatal("cancellation published value", got, err, cancel.calls)
		}
		out, err := jsonbOutput(ctx, &tc.source.jsonb.root)
		if err != nil || out != tc.source.text {
			t.Fatal("cancellation mutated source", err)
		}
	}
}
