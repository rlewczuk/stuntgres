package sqltype

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestJSONBInsertTypedContracts(t *testing.T) {
	d := testTypes()
	p := jsonbSetUnitPlan(t, d, "jsonb_insert")
	for _, tc := range []struct {
		source, path, replacement, want, code, message string
		after                                          bool
	}{
		{`[0,1,2]`, "{1}", `"x"`, `[0, "x", 1, 2]`, "", "", false},
		{`[0,1,2]`, "{1}", `"x"`, `[0, 1, "x", 2]`, "", "", true},
		{`[0,1,2]`, "{-1}", `"x"`, `[0, 1, "x", 2]`, "", "", false},
		{`[0,1,2]`, "{-1}", `"x"`, `[0, 1, 2, "x"]`, "", "", true},
		{`[0,1,2]`, "{-3}", "9", `[9, 0, 1, 2]`, "", "", false},
		{`[0,1,2]`, "{-3}", "9", `[0, 9, 1, 2]`, "", "", true},
		{`[0]`, "{-2147483648}", "9", `[9, 0]`, "", "", true},
		{`[0]`, "{2147483647}", "9", `[0, 9]`, "", "", true},
		{`[]`, "{-99}", "9", `[9]`, "", "", true},
		{`[]`, "{99}", "9", `[9]`, "", "", false},
		{`[0,1]`, "{1}", `[7,8]`, `[0, [7, 8], 1]`, "", "", false},
		{`[0,1]`, "{1}", `{"b":2,"a":1}`, `[0, {"a": 1, "b": 2}, 1]`, "", "", false},
		{`{"a":[0,1,2]}`, "{a,1}", "9", `{"a": [0, 1, 9, 2]}`, "", "", true},
		{`{"a":[0,1,2]}`, "[-2:-1]={a,1}", "9", `{"a": [0, 9, 1, 2]}`, "", "", false},
		{`{"é":1}`, "{z}", "1.00", `{"z": 1.00, "é": 1}`, "", "", true},
		{`{}`, "{0}", "1", `{"0": 1}`, "", "", false},
		{`{}`, "{a,b}", "2", `{}`, "", "", false},
		{`{"a":1}`, "{a,x}", "2", `{"a": 1}`, "", "", false},
		{`{"a":null}`, "{a,x}", "2", `{"a": null}`, "", "", false},
		{`[[0]]`, "{99,0}", "2", `[[0]]`, "", "", false},
		{`[]`, "{}", "2", `[]`, "", "", false},
		{`{}`, "{}", "2", `{}`, "", "", false},
		{`{"a":1}`, "{a,x,NULL}", "2", `{"a": 1}`, "", "", false},
		{`{}`, "{missing,NULL}", "2", `{}`, "", "", false},
		{`[0]`, "{0}", "null", `[null, 0]`, "", "", false},
		{`1`, "{{a}}", "2", "", "2202E", "wrong number of array subscripts", false},
		{`1`, "{}", "2", "", "22023", "cannot set path in scalar", false},
		{`null`, "{a}", "2", "", "22023", "cannot set path in scalar", false},
		{`{}`, "{NULL}", "2", "", "22004", "path element at position 1 is null", false},
		{`{"a":1}`, "{a,NULL}", "2", "", "22004", "path element at position 2 is null", false},
		{`[]`, "{bad}", "2", "", "22P02", `path element at position 1 is not an integer: "bad"`, false},
	} {
		t.Run(tc.source+tc.path+tc.want, func(t *testing.T) {
			values := []Value{jsonbSetUnitInput(t, d[JSONB], tc.source), jsonbSetUnitInput(t, d[1009], tc.path), jsonbSetUnitInput(t, d[JSONB], tc.replacement), Boolean(d[Bool], tc.after)}
			got, err := p.Evaluate(context.Background(), values)
			if tc.code != "" {
				var diag *sqlstate.Error
				want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message}
				if !errors.As(err, &diag) || *diag != want || got != (Value{}) {
					t.Fatal(got, err, want)
				}
				return
			}
			if err != nil || got.text != tc.want || got.typ != d[JSONB] || got.jsonb == nil || p.result.Typmod != -1 {
				t.Fatal(got, err, tc.want)
			}
		})
	}
	for _, source := range []string{`{"a":1}`, `{"a":null}`, `{"a":0,"a":1}`} {
		for _, replacement := range []string{"1", "null"} {
			got, err := p.Evaluate(context.Background(), []Value{jsonbSetUnitInput(t, d[JSONB], source), jsonbSetUnitInput(t, d[1009], "{a}"), jsonbSetUnitInput(t, d[JSONB], replacement), Boolean(d[Bool], false)})
			var diag *sqlstate.Error
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: "cannot replace existing key", Hint: "Try using the function jsonb_set to replace key value."}
			if !errors.As(err, &diag) || *diag != want || got != (Value{}) {
				t.Fatal(got, err)
			}
		}
	}
	for _, tc := range []struct {
		text string
		want int64
		bad  bool
	}{
		{" +1", 1, false}, {"\t\n-1", -1, false}, {"2147483647", 2147483647, false}, {"-2147483648", -2147483648, false},
		{"1 ", 0, true}, {"1x", 0, true}, {"", 0, true}, {"+", 0, true}, {"2147483648", 0, true}, {"-2147483649", 0, true},
	} {
		got, err := jsonbSetIndex(context.Background(), tc.text, 2)
		if tc.bad {
			var diag *sqlstate.Error
			if !errors.As(err, &diag) || diag.Code != "22P02" || diag.Message != `path element at position 2 is not an integer: "`+tc.text+`"` {
				t.Fatal(got, err)
			}
		} else if err != nil || got != tc.want {
			t.Fatal(got, err)
		}
	}
}

func TestJSONBInsertDescriptorAndNullAuthority(t *testing.T) {
	d, fresh := testTypes(), testTypes()
	p := jsonbSetUnitPlan(t, d, "jsonb_insert")
	values := []Value{jsonbSetUnitInput(t, d[JSONB], "1"), jsonbSetUnitInput(t, d[1009], "{{a}}"), jsonbSetUnitInput(t, d[JSONB], "9"), Boolean(d[Bool], false)}
	for slot := range 4 {
		args := append([]Value(nil), values...)
		args[slot] = Null(d[args[slot].typ.ID()])
		got, err := p.Evaluate(context.Background(), args)
		if err != nil || !got.null || got.typ != d[JSONB] {
			t.Fatal(slot, got, err)
		}
		args[slot] = Null(fresh[args[slot].typ.ID()])
		if got, err := p.Evaluate(context.Background(), args); err == nil || got != (Value{}) {
			t.Fatal("NULL bypassed descriptor authority", slot, got, err)
		}
	}
	usages := []Usage{d[JSONB].Usage(), d[1009].Usage(), d[JSONB].Usage(), d[Bool].Usage()}
	for slot := range 4 {
		bad := append([]Usage(nil), usages...)
		bad[slot].Typmod = 1
		if _, err := BindJSONBSet("jsonb_insert", d[JSONB].Usage(), bad...); err == nil {
			t.Fatal("accepted typmod", slot)
		}
	}
	if _, err := BindJSONBSet("jsonb_insert", fresh[JSONB].Usage(), usages...); err == nil {
		t.Fatal("foreign result accepted")
	}
	for _, slot := range []int{0, 2} {
		bad := append([]Usage(nil), usages...)
		bad[slot] = fresh[JSONB].Usage()
		if _, err := BindJSONBSet("jsonb_insert", d[JSONB].Usage(), bad...); err == nil {
			t.Fatal("foreign JSONB accepted", slot)
		}
	}
}

func TestJSONBInsertImmutableSharing(t *testing.T) {
	d := testTypes()
	p := jsonbSetUnitPlan(t, d, "jsonb_insert")
	source := jsonbSetUnitInput(t, d[JSONB], `{"a":{"x":[1]},"b":[2]}`)
	replacement := jsonbSetUnitInput(t, d[JSONB], `{"q":[3.00]}`)
	path := jsonbSetUnitInput(t, d[1009], "{a,x,0}")
	values := []Value{source, path, replacement, Boolean(d[Bool], false)}
	got, err := p.Evaluate(context.Background(), values)
	if err != nil || got.text != `{"a": {"x": [{"q": [3.00]}, 1]}, "b": [2]}` {
		t.Fatal(got.text, err)
	}
	old, next := source.jsonb.root, got.jsonb.root
	if &old.fields[0] == &next.fields[0] || &old.fields[0].value.fields[0] == &next.fields[0].value.fields[0] || &old.fields[0].value.fields[0].value.items[0] == &next.fields[0].value.fields[0].value.items[0] {
		t.Fatal("changed path borrows mutable slices")
	}
	if &old.fields[1].value.items[0] != &next.fields[1].value.items[0] {
		t.Fatal("untouched subtree cloned")
	}
	if &replacement.jsonb.root.fields[0] != &next.fields[0].value.fields[0].value.items[0].fields[0] {
		t.Fatal("replacement subtree cloned")
	}
	values[3] = Boolean(d[Bool], true)
	second, err := p.Evaluate(context.Background(), values)
	if err != nil || second.text != `{"a": {"x": [1, {"q": [3.00]}]}, "b": [2]}` {
		t.Fatal(second.text, err)
	}
	for _, value := range []Value{source, replacement, got} {
		text, err := jsonbOutput(context.Background(), &value.jsonb.root)
		if err != nil || text != value.text {
			t.Fatal("input/prior result mutated", text, err)
		}
	}
	values[1] = jsonbSetUnitInput(t, d[1009], "{missing,x}")
	if unchanged, err := p.Evaluate(context.Background(), values); err != nil || unchanged != source {
		t.Fatal("no-op republished", unchanged, err)
	}
}

func TestJSONBInsertIterativeDepthAndCancellation(t *testing.T) {
	d := testTypes()
	ctx := context.Background()
	p := jsonbSetUnitPlan(t, d, "jsonb_insert")
	const depth = 4096
	root := jsonbNode{kind: jsonbArray, items: []jsonbNode{{kind: jsonbNumber, number: jsonbDecimal{digits: "1"}}}}
	steps := make([]Value, depth+1)
	for i := range depth {
		root = jsonbNode{kind: jsonbObject, fields: []jsonbEntry{{key: "a", value: root}}}
		steps[i] = String(d[Text], "a")
	}
	steps[depth] = String(d[Text], "0")
	text, err := jsonbOutput(ctx, &root)
	if err != nil {
		t.Fatal(err)
	}
	source := Value{typ: d[JSONB], text: text, jsonb: &jsonbPayload{root: root}}
	path, err := OwnedArrayValue(d[1009], []ArrayDimension{{Lower: -4, Length: depth + 1}}, steps)
	if err != nil {
		t.Fatal(err)
	}
	replacement := jsonbSetUnitInput(t, d[JSONB], "2")
	got, err := p.Evaluate(ctx, []Value{source, path, replacement, Boolean(d[Bool], false)})
	if err != nil || got.text != strings.Repeat(`{"a": `, depth)+"[2, 1]"+strings.Repeat("}", depth) {
		t.Fatal("deep iterative insertion", err)
	}
	longKey := strings.Repeat("k", 16*jsonbWorkChunk)
	wide := jsonbSetUnitInput(t, d[JSONB], "["+strings.Repeat("0,", 16*jsonbWorkChunk)+"0]")
	longObject := jsonbSetUnitInput(t, d[JSONB], `{"`+longKey+`":1}`)
	largeOutput := jsonbSetUnitInput(t, d[JSONB], `[0,"`+strings.Repeat("x", 32*jsonbWorkChunk)+`"]`)
	for _, tc := range []struct {
		source Value
		steps  []Value
		at     int
	}{
		{source, steps, 10}, {wide, []Value{String(d[Text], "0")}, 10},
		{longObject, []Value{String(d[Text], longKey[:len(longKey)-1]+"z")}, 10},
		{wide, []Value{String(d[Text], strings.Repeat(" ", 16*jsonbWorkChunk)+"0")}, 10},
		{largeOutput, []Value{String(d[Text], "0")}, 20},
	} {
		path, err := ArrayValue(d[1009], []ArrayDimension{{Lower: 1, Length: int32(len(tc.steps))}}, tc.steps)
		if err != nil {
			t.Fatal(err)
		}
		cancel := &groupingCancelContext{Context: ctx, at: tc.at}
		got, err := p.Evaluate(cancel, []Value{tc.source, path, replacement, Boolean(d[Bool], false)})
		if !errors.Is(err, context.Canceled) || got != (Value{}) {
			t.Fatal("cancellation published value", got, err, cancel.calls)
		}
		text, err := jsonbOutput(ctx, &tc.source.jsonb.root)
		if err != nil || text != tc.source.text {
			t.Fatal("cancellation mutated source", err)
		}
	}
}
