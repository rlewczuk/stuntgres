package sqltype

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestJSONBStripNullsValues(t *testing.T) {
	d, ctx := testTypes(), context.Background()
	p, err := BindJSONStripNulls(d[JSONB].Usage(), d[JSONB].Usage(), d[Bool].Usage())
	if err != nil || p.Name() != "jsonb_strip_nulls" {
		t.Fatal(p, err)
	}
	for _, tc := range []struct{ source, ordinary, arrays string }{
		{`{"a":1,"b":null,"c":[2,null,3],"d":{"e":4,"f":null}}`, `{"a": 1, "c": [2, null, 3], "d": {"e": 4}}`, `{"a": 1, "c": [2, 3], "d": {"e": 4}}`},
		{`[null,null,{"a":null},[],[null,null],null]`, `[null, null, {}, [], [null, null], null]`, `[{}, [], []]`},
		{`{"a":{"b":null},"d":{},"e":[null]}`, `{"a": {}, "d": {}, "e": [null]}`, `{"a": {}, "d": {}, "e": []}`},
		{`{"k":1,"k":null}`, `{}`, `{}`}, {`{"k":null,"k":2}`, `{"k": 2}`, `{"k": 2}`},
		{`{"a":1,"\u0061":null}`, `{}`, `{}`},
		{`{"é":null,"aaa":1.2300,"a":"\n","s":"a'b\\c","face":"\uD83D\uDE00"}`, `{"a": "\n", "s": "a'b\\c", "aaa": 1.2300, "face": "😀"}`, `{"a": "\n", "s": "a'b\\c", "aaa": 1.2300, "face": "😀"}`},
		{`null`, `null`, `null`}, {`"null"`, `"null"`, `"null"`}, {`1.00`, `1.00`, `1.00`},
		{`true`, `true`, `true`}, {`false`, `false`, `false`}, {`{}`, `{}`, `{}`}, {`[]`, `[]`, `[]`},
	} {
		source := jsonbSetUnitInput(t, d[JSONB], tc.source)
		original := source
		for _, arrays := range []bool{false, true} {
			want := tc.ordinary
			if arrays {
				want = tc.arrays
			}
			got, err := p.Evaluate(ctx, source, Boolean(d[Bool], arrays))
			if err != nil || got.typ != d[JSONB] || got.null || got.text != want || got.jsonb == nil {
				t.Fatalf("source=%q flag=%v got=%q want=%q error=%v", tc.source, arrays, got.text, want, err)
			}
			if source != original {
				t.Fatal("mutated original value")
			}
			if text, err := jsonbOutput(ctx, &source.jsonb.root); err != nil || text != original.text {
				t.Fatal("mutated original tree", text, err)
			}
			if want == source.text && got != source {
				t.Fatal("unchanged result lost original ownership")
			}
		}
	}
}

func TestJSONBStripNullsCopyOnWrite(t *testing.T) {
	d, ctx := testTypes(), context.Background()
	p, err := BindJSONStripNulls(d[JSONB].Usage(), d[JSONB].Usage(), d[Bool].Usage())
	if err != nil {
		t.Fatal(err)
	}
	v := jsonbSetUnitInput(t, d[JSONB], `{"drop":null,"keep":{"a":[1,2]},"edit":{"x":null,"v":1}}`)
	before := v.text
	v.text = "poisoned cache must not be reparsed"
	original := v
	for range 2 {
		got, err := p.Evaluate(ctx, v, Boolean(d[Bool], true))
		if err != nil || got.text != `{"edit": {"v": 1}, "keep": {"a": [1, 2]}}` {
			t.Fatal(got.text, err)
		}
		if got.jsonb == v.jsonb || &got.jsonb.root.fields[0] == &v.jsonb.root.fields[0] {
			t.Fatal("changed root reused mutable slice")
		}
		if &got.jsonb.root.fields[0].value.fields[0] == &v.jsonb.root.fields[1].value.fields[0] {
			t.Fatal("changed child reused mutable slice")
		}
		if &got.jsonb.root.fields[1].value.fields[0] != &v.jsonb.root.fields[2].value.fields[0] {
			t.Fatal("unchanged subtree was copied")
		}
		if v != original {
			t.Fatal("mutated input cache or ownership")
		}
		if text, err := jsonbOutput(ctx, &v.jsonb.root); err != nil || text != before {
			t.Fatal("mutated published input", text, err)
		}
	}
	deep := strings.Repeat("[", 4096) + "null" + strings.Repeat("]", 4096)
	got, err := p.Evaluate(ctx, jsonbSetUnitInput(t, d[JSONB], deep), Boolean(d[Bool], true))
	if err != nil || got.text != strings.Repeat("[", 4096)+strings.Repeat("]", 4096) {
		t.Fatal("deep traversal", err)
	}
	wide := "[" + strings.Repeat("null,", 8192) + "1]"
	got, err = p.Evaluate(ctx, jsonbSetUnitInput(t, d[JSONB], wide), Boolean(d[Bool], true))
	if err != nil || got.text != "[1]" {
		t.Fatal("wide traversal", got.text, err)
	}
}

func TestJSONBStripNullsDescriptorAuthority(t *testing.T) {
	d, fresh := testTypes(), testTypes()
	p, err := BindJSONStripNulls(d[JSONB].Usage(), d[JSONB].Usage(), d[Bool].Usage())
	if err != nil || p.ResultUsage() != d[JSONB].Usage() {
		t.Fatal(p, err)
	}
	for _, args := range [][3]Usage{
		{d[Text].Usage(), d[JSONB].Usage(), d[Bool].Usage()},
		{d[JSONB].Usage(), d[JSON].Usage(), d[Bool].Usage()},
		{d[JSON].Usage(), d[JSONB].Usage(), d[Bool].Usage()},
		{d[JSONB].Usage(), d[JSONB].Usage(), d[Text].Usage()},
		{fresh[JSONB].Usage(), d[JSONB].Usage(), d[Bool].Usage()},
		{d[JSONB].Usage(), fresh[JSONB].Usage(), d[Bool].Usage()},
		{Usage{}, d[JSONB].Usage(), d[Bool].Usage()},
	} {
		if _, err := BindJSONStripNulls(args[0], args[1], args[2]); err == nil {
			t.Fatal("accepted foreign or invalid descriptor", args)
		}
	}
	for slot := range 3 {
		args := [3]Usage{d[JSONB].Usage(), d[JSONB].Usage(), d[Bool].Usage()}
		args[slot].Typmod = 1
		if _, err := BindJSONStripNulls(args[0], args[1], args[2]); err == nil {
			t.Fatal("accepted typmod", slot)
		}
	}
	for _, args := range [][2]Value{{Null(fresh[JSONB]), Null(d[Bool])}, {Null(d[JSONB]), Null(fresh[Bool])}, {Null(d[JSON]), Null(d[Bool])}} {
		got, err := p.Evaluate(context.Background(), args[0], args[1])
		if err == nil || got != (Value{}) {
			t.Fatal("NULL bypassed descriptor validation", got, err)
		}
	}
	for _, args := range [][2]Value{{Null(d[JSONB]), Boolean(d[Bool], true)}, {jsonbSetUnitInput(t, d[JSONB], "{}"), Null(d[Bool])}, {Null(d[JSONB]), Null(d[Bool])}} {
		got, err := p.Evaluate(context.Background(), args[0], args[1])
		if err != nil || got != Null(d[JSONB]) {
			t.Fatal("unowned SQL NULL", got, err)
		}
	}
	for _, bad := range []Value{{typ: d[JSONB]}, {typ: d[JSONB], jsonb: &jsonbPayload{root: jsonbNode{kind: 255}}}} {
		got, err := p.Evaluate(context.Background(), bad, Boolean(d[Bool], true))
		if err == nil || got != (Value{}) {
			t.Fatal("invalid payload published result", got, err)
		}
	}
}

func TestJSONBStripNullsCancellationCheckpoints(t *testing.T) {
	d, ctx := testTypes(), context.Background()
	p, err := BindJSONStripNulls(d[JSONB].Usage(), d[JSONB].Usage(), d[Bool].Usage())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, source string }{
		{"null-skips", "[" + strings.Repeat("null,", 128) + "0]"},
		{"prefix-copy", "[" + strings.Repeat("0,", 2*jsonbWorkChunk) + "null]"},
		{"deep-unwind", strings.Repeat("[", 48) + "null" + strings.Repeat("]", 48)},
		{"output-key", `{"a":null,"` + strings.Repeat("z", 8*jsonbWorkChunk) + `":1}`},
		{"output-string", `[null,"` + strings.Repeat(`\n`, 8*jsonbWorkChunk) + `"]`},
		{"output-numeric", "[null,1e8192]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value := jsonbSetUnitInput(t, d[JSONB], tc.source)
			original := value
			count := &groupingCancelContext{Context: ctx, at: int(^uint(0) >> 1)}
			if _, err := p.Evaluate(count, value, Boolean(d[Bool], true)); err != nil || count.calls < 4 {
				t.Fatal("missing checkpoints", count.calls, err)
			}
			for at := 1; at <= count.calls; at++ {
				cancel := &groupingCancelContext{Context: ctx, at: at}
				got, err := p.Evaluate(cancel, value, Boolean(d[Bool], true))
				if !errors.Is(err, context.Canceled) || got != (Value{}) || value != original {
					t.Fatalf("checkpoint=%d result=%v error=%v", at, got, err)
				}
			}
			if text, err := jsonbOutput(ctx, &value.jsonb.root); err != nil || text != original.text {
				t.Fatal("cancellation mutated original tree", err)
			}
		})
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if got, err := p.Evaluate(canceled, Null(d[JSONB]), Null(d[Bool])); !errors.Is(err, context.Canceled) || got != (Value{}) {
		t.Fatal("NULL hid cancellation", got, err)
	}
}

func TestJSONBStripNullsOutputCheckpoints(t *testing.T) {
	d, ctx := testTypes(), context.Background()
	const depth = 48
	for _, tc := range []struct {
		name, source, want string
		at                 int
	}{
		{"numeric-interior", "1e8192", "1" + strings.Repeat("0", 8192), 4},
		{"numeric-scale", "1.2300", "1.2300", 4},
		{"container-unwind", strings.Repeat("[", depth) + "null" + strings.Repeat("]", depth), strings.Repeat("[", depth) + "null" + strings.Repeat("]", depth), depth + 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value := jsonbSetUnitInput(t, d[JSONB], tc.source)
			active := &groupingCancelContext{Context: ctx, at: tc.at}
			if text, err := jsonbOutput(active, &value.jsonb.root); text != "" || !errors.Is(err, context.Canceled) || active.calls != tc.at {
				t.Fatal("writer phase published output", tc.name, text, err, active.calls)
			}
			if text, err := jsonbOutput(ctx, &value.jsonb.root); err != nil || text != tc.want {
				t.Fatal("writer changed canonical output", text, err)
			}
		})
	}
}
