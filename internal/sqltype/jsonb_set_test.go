package sqltype

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

func jsonbSetUnitInput(t *testing.T, d *Descriptor, text string) Value {
	t.Helper()
	v, err := d.Input(context.Background(), text, -1)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func jsonbSetUnitPlan(t *testing.T, types map[ID]*Descriptor, name string) JSONBSetPlan {
	t.Helper()
	args := []Usage{types[JSONB].Usage(), types[1009].Usage(), types[JSONB].Usage(), types[Bool].Usage()}
	if name == "jsonb_set_lax" {
		args = append(args, types[Text].Usage())
	}
	p, err := BindJSONBSet(name, types[JSONB].Usage(), args...)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestJSONBSetTypedContracts(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	for _, tc := range []struct {
		target, path, replacement, mode, want, code, msg string
		create                                           bool
	}{
		{`{"a":1}`, "{a}", "2", "", "{\"a\": 2}", "", "", true},
		{`{}`, "{a,b}", "2", "", "{}", "", "", true},
		{`{"a":1}`, "{a,x}", "2", "", `{"a": 1}`, "", "", true},
		{`[1,2,3]`, "{-4}", "9", "", "[9, 1, 2, 3]", "", "", true},
		{`[1,2,3]`, "{2147483647}", "9", "", "[1, 2, 3, 9]", "", "", true},
		{`[1,2,3]`, "{-2147483648}", "9", "", "[1, 2, 3]", "", "", false},
		{`{"a":{"b":1}}`, "[-2:-1]={a,b}", "2", "", `{"a": {"b": 2}}`, "", "", true},
		{`{"a":1}`, "{a,NULL}", "2", "", "", "22004", "path element at position 2 is null", true},
		{`{"a":1}`, "{a,x,NULL}", "2", "", `{"a": 1}`, "", "", true},
		{`{}`, "{missing,NULL}", "2", "", "{}", "", "", true},
		{`1`, "{{a}}", "2", "", "", "2202E", "wrong number of array subscripts", true},
		{`null`, "{}", "2", "", "", "22023", "cannot set path in scalar", true},
		{`{"a":1,"b":2}`, "{a}", "", "use_json_null", `{"a": null, "b": 2}`, "", "", true},
		{`{"a":1,"b":2}`, "{a}", "", "delete_key", `{"b": 2}`, "", "", false},
		{`[1,2,3]`, "{-1}", "", "delete_key", "[1, 2]", "", "", true},
		{`1`, "{{a}}", "", "return_target", "1", "", "", true},
		{`1`, "{}", "", "delete_key", "", "22023", "cannot delete path in scalar", true},
	} {
		t.Run(tc.target+tc.path+tc.mode, func(t *testing.T) {
			name := "jsonb_set"
			if tc.mode != "" {
				name = "jsonb_set_lax"
			}
			p := jsonbSetUnitPlan(t, types, name)
			values := []Value{jsonbSetUnitInput(t, types[JSONB], tc.target), jsonbSetUnitInput(t, types[1009], tc.path), Null(types[JSONB]), Boolean(types[Bool], tc.create)}
			if tc.replacement != "" {
				values[2] = jsonbSetUnitInput(t, types[JSONB], tc.replacement)
			}
			if tc.mode != "" {
				values = append(values, String(types[Text], tc.mode))
			}
			source := values[0]
			got, err := p.Evaluate(ctx, values)
			if tc.code != "" {
				var d *sqlstate.Error
				want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.msg}
				if !errors.As(err, &d) || *d != want || got != (Value{}) {
					t.Fatal(got, err, want)
				}
				return
			}
			if err != nil || got.text != tc.want || got.typ != types[JSONB] || got.jsonb == nil {
				t.Fatal(got, err, tc.want)
			}
			out, err := types[JSONB].Output(ctx, source)
			if err != nil || out != source.text {
				t.Fatal("mutated source", out, err)
			}
		})
	}
	for _, tc := range []struct {
		text    string
		want    int64
		invalid bool
	}{
		{"+1", 1, false}, {" \t\n1", 1, false}, {"-2147483648", -2147483648, false}, {"2147483647", 2147483647, false}, {"2147483648", 0, true}, {"-2147483649", 0, true}, {"1 ", 0, true}, {"", 0, true}, {"+", 0, true}, {"1x", 0, true},
	} {
		got, err := jsonbSetIndex(ctx, tc.text, 2)
		if tc.invalid {
			var d *sqlstate.Error
			if !errors.As(err, &d) || d.Code != "22P02" || d.Message != `path element at position 2 is not an integer: "`+tc.text+`"` {
				t.Fatal(got, err)
			}
		} else if err != nil || got != tc.want {
			t.Fatal(got, err, tc)
		}
	}
}

func TestJSONBSetImmutableSharing(t *testing.T) {
	types := testTypes()
	p := jsonbSetUnitPlan(t, types, "jsonb_set")
	source := jsonbSetUnitInput(t, types[JSONB], `{"a":{"x":[1]},"b":[2]}`)
	replacement := jsonbSetUnitInput(t, types[JSONB], `{"q":[3.00]}`)
	path := jsonbSetUnitInput(t, types[1009], "{a,x}")
	got, err := p.Evaluate(context.Background(), []Value{source, path, replacement, Boolean(types[Bool], true)})
	if err != nil || got.text != `{"a": {"x": {"q": [3.00]}}, "b": [2]}` {
		t.Fatal(got.text, err)
	}
	old, new := source.jsonb.root, got.jsonb.root
	if &old.fields[0] == &new.fields[0] || &old.fields[0].value.fields[0] == &new.fields[0].value.fields[0] {
		t.Fatal("changed ancestor storage borrowed")
	}
	if &old.fields[1].value.items[0] != &new.fields[1].value.items[0] {
		t.Fatal("untouched subtree cloned")
	}
	if &replacement.jsonb.root.fields[0] != &new.fields[0].value.fields[0].value.fields[0] {
		t.Fatal("replacement subtree cloned")
	}
	if source.text != `{"a": {"x": [1]}, "b": [2]}` || replacement.text != `{"q": [3.00]}` {
		t.Fatal("input changed")
	}
	unchanged, err := p.Evaluate(context.Background(), []Value{source, jsonbSetUnitInput(t, types[1009], "{missing,x}"), replacement, Boolean(types[Bool], true)})
	if err != nil || unchanged != source {
		t.Fatal("unchanged path republished", unchanged, err)
	}
}

func TestJSONBSetDescriptorAuthority(t *testing.T) {
	types, fresh := testTypes(), testTypes()
	p := jsonbSetUnitPlan(t, types, "jsonb_set_lax")
	values := []Value{Null(types[JSONB]), Null(types[1009]), Null(types[JSONB]), Null(types[Bool]), Null(types[Text])}
	for slot := range 5 {
		bad := append([]Value(nil), values...)
		bad[slot] = Null(fresh[values[slot].typ.ID()])
		if got, err := p.Evaluate(context.Background(), bad); err == nil || got != (Value{}) {
			t.Fatal("NULL bypassed descriptor check", slot, got, err)
		}
	}
	args := []Usage{types[JSONB].Usage(), types[1009].Usage(), types[JSONB].Usage(), types[Bool].Usage(), types[Text].Usage()}
	for _, slot := range []int{0, 2, 4} {
		bad := append([]Usage(nil), args...)
		bad[slot] = fresh[args[slot].Type.ID()].Usage()
		if _, err := BindJSONBSet("jsonb_set_lax", types[JSONB].Usage(), bad...); err == nil {
			t.Fatal("accepted foreign signature", slot)
		}
	}
	if _, err := BindJSONBSet("jsonb_set_lax", fresh[JSONB].Usage(), args...); err == nil {
		t.Fatal("accepted foreign result")
	}
	args[1].Typmod = 1
	if _, err := BindJSONBSet("jsonb_set_lax", types[JSONB].Usage(), args...); err == nil {
		t.Fatal("accepted typmod")
	}
}

func TestJSONBSetIterativeDepthAndCancellation(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	p := jsonbSetUnitPlan(t, types, "jsonb_set")
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
	replacement := jsonbSetUnitInput(t, types[JSONB], "2")
	got, err := p.Evaluate(ctx, []Value{source, path, replacement, Boolean(types[Bool], true)})
	if err != nil || got.text != strings.Repeat(`{"a": `, depth)+"2"+strings.Repeat("}", depth) {
		t.Fatal("deep iterative replacement", err)
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
		got, err := p.Evaluate(cancel, []Value{tc.source, path, replacement, Boolean(types[Bool], true)})
		if !errors.Is(err, context.Canceled) || got != (Value{}) {
			t.Fatal("cancellation published value", got, err, cancel.calls)
		}
		out, err := jsonbOutput(ctx, &tc.source.jsonb.root)
		if err != nil || out != tc.source.text {
			t.Fatal("cancellation mutated source", err)
		}
	}
}
