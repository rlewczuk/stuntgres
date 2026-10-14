package sqltype

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestJSONBObjectProviderShapesAndValues(t *testing.T) {
	types := testTypes()
	array, result := types[1009], types[JSONB]
	for _, tc := range []struct {
		inputs              []string
		want, code, message string
	}{
		{[]string{"{{},{}}"}, `{}`, "", ""},
		{[]string{"{}"}, `{}`, "", ""},
		{[]string{"{}", "{}"}, `{}`, "", ""},
		{[]string{"[-2:1]={a,1,b,2}"}, `{"a": "1", "b": "2"}`, "", ""},
		{[]string{"[-1:0][3:4]={{a,1},{b,2}}"}, `{"a": "1", "b": "2"}`, "", ""},
		{[]string{"[-2:-1]={a,b}", "[7:8]={1,2}"}, `{"a": "1", "b": "2"}`, "", ""},
		{[]string{`{a,NULL,a,two,"",""}`}, `{"": "", "a": "two"}`, "", ""},
		{[]string{`{z,a,n,j}`, `{true,"null","NULL","{\"x\":1}"}`}, `{"a": "null", "j": "{\"x\":1}", "n": "NULL", "z": "true"}`, "", ""},
		{[]string{"{é,one,z,two,aa,three,a,first,a,NULL}"}, `{"a": null, "z": "two", "aa": "three", "é": "one"}`, "", ""},
		{[]string{"{a,a}", "{old,NULL}"}, `{"a": null}`, "", ""},
		{[]string{"{a,1e131072,b,3.5}"}, `{"a": "1e131072", "b": "3.5"}`, "", ""},
		{[]string{"{NULL,x,y}"}, "", "2202E", "array must have even number of elements"},
		{[]string{"{{a},{b}}"}, "", "2202E", "array must have two columns"},
		{[]string{"{{a,b,c},{d,e,f}}"}, "", "2202E", "array must have two columns"},
		{[]string{"{{{NULL,b}}}"}, "", "2202E", "wrong number of array subscripts"},
		{[]string{"{{a,b}}", "{{1,2}}"}, "", "2202E", "wrong number of array subscripts"},
		{[]string{"{}", "{x}"}, "", "2202E", "wrong number of array subscripts"},
		{[]string{"{x}", "{}"}, "", "2202E", "wrong number of array subscripts"},
		{[]string{"{NULL,a}", "{x}"}, "", "2202E", "mismatched array dimensions"},
		{[]string{"{NULL,v}"}, "", "22004", "null value not allowed for object key"},
		{[]string{"{a,NULL}", "{x,y}"}, "", "22004", "null value not allowed for object key"},
	} {
		t.Run(strings.Join(tc.inputs, ";"), func(t *testing.T) {
			var values [2]Value
			var usages [2]Usage
			var sources [2]string
			for i, text := range tc.inputs {
				v, err := array.Input(context.Background(), text, -1)
				if err != nil {
					t.Fatal(err)
				}
				values[i], usages[i] = v, array.Usage()
				sources[i], err = array.Output(context.Background(), v)
				if err != nil {
					t.Fatal(err)
				}
			}
			p, err := BindBinary("jsonb_object", result.Usage(), usages[:len(tc.inputs)]...)
			if err != nil {
				t.Fatal(err)
			}
			got, err := p.Evaluate(context.Background(), values[:len(tc.inputs)]...)
			if tc.code != "" {
				var record *sqlstate.Error
				want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message}
				if !errors.As(err, &record) || *record != want || got != (Value{}) {
					t.Fatalf("got %+v / %v want %+v", record, got, want)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			text, err := result.Output(context.Background(), got)
			if err != nil || text != tc.want || got.Type() != result {
				t.Fatal(text, tc.want, err)
			}
			if got.jsonb == nil || got.jsonb.root.kind != jsonbObject {
				t.Fatal("missing structural object payload")
			}
			independent, err := result.Input(context.Background(), tc.want, -1)
			if err != nil {
				t.Fatal(err)
			}
			group, err := BindGrouping(result.Usage())
			if err != nil {
				t.Fatal(err)
			}
			equal, err := group.Equal(context.Background(), got, independent)
			if err != nil || !equal {
				t.Fatal("structural equality", equal, err)
			}
			hash, err := group.Hash(context.Background(), got)
			if err != nil {
				t.Fatal(err)
			}
			wantHash, err := group.Hash(context.Background(), independent)
			if err != nil || hash != wantHash {
				t.Fatal("structural hash", hash, wantHash, err)
			}
			for i := range tc.inputs {
				text, err := array.Output(context.Background(), values[i])
				if err != nil || text != sources[i] {
					t.Fatal("source changed", text, err)
				}
			}
		})
	}
}

func TestJSONBObjectProviderEscapingStrictnessAndCancellation(t *testing.T) {
	types := testTypes()
	array, text, result := types[1009], types[Text], types[JSONB]
	v, err := ArrayValue(array, []ArrayDimension{{Lower: -1, Length: 4}}, []Value{
		String(text, "\"\\"), String(text, "\b\f\n\r\t\x01"), String(text, "é😀"), String(text, "<>&/"),
	})
	if err != nil {
		t.Fatal(err)
	}
	one, err := BindBinary("jsonb_object", result.Usage(), array.Usage())
	if err != nil {
		t.Fatal(err)
	}
	got, err := one.Evaluate(context.Background(), v)
	if err != nil {
		t.Fatal(err)
	}
	out, err := result.Output(context.Background(), got)
	if err != nil || out != `{"\"\\": "\b\f\n\r\t\u0001", "é😀": "<>&/"}` {
		t.Fatal(out, err)
	}
	two, err := BindBinary("jsonb_object", result.Usage(), array.Usage(), array.Usage())
	if err != nil {
		t.Fatal(err)
	}
	for _, inputs := range [][]Value{{Null(array), v}, {v, Null(array)}} {
		got, err := two.Evaluate(context.Background(), inputs...)
		if err != nil || !got.IsNull() || got.Type() != result {
			t.Fatal(got, err)
		}
	}
	long, err := ArrayValue(array, []ArrayDimension{{Lower: 1, Length: 2}}, []Value{String(text, "k"), String(text, strings.Repeat("\n", 8*jsonbWorkChunk))})
	if err != nil {
		t.Fatal(err)
	}
	cancel := &groupingCancelContext{Context: context.Background(), at: 6}
	got, err = one.Evaluate(cancel, long)
	if !errors.Is(err, context.Canceled) || got != (Value{}) || cancel.calls != cancel.at {
		t.Fatal("partial result", got, err, cancel.calls)
	}
}

func TestJSONBObjectProviderDescriptorContracts(t *testing.T) {
	types, replacement := testTypes(), testTypes()
	for _, args := range [][]Usage{nil, {types[1009].Usage(), types[1009].Usage(), types[1009].Usage()}, {types[Text].Usage()}, {types[1007].Usage()}, {types[1009].Usage(), replacement[1009].Usage()}} {
		if _, err := BindBinary("jsonb_object", types[JSONB].Usage(), args...); err == nil {
			t.Fatal("accepted invalid signature", args)
		}
	}
	if _, err := BindBinary("jsonb_object", types[JSON].Usage(), types[1009].Usage()); err == nil {
		t.Fatal("accepted JSON result")
	}
	for _, usage := range []Usage{types[1009].Usage(), types[JSONB].Usage()} {
		usage.Typmod = 1
		result, arg := types[JSONB].Usage(), types[1009].Usage()
		if usage.Type.ID() == JSONB {
			result = usage
		} else {
			arg = usage
		}
		if _, err := BindBinary("jsonb_object", result, arg); err == nil {
			t.Fatal("accepted typmod", usage)
		}
	}
	p, err := BindBinary("jsonb_object", types[JSONB].Usage(), types[1009].Usage())
	if err != nil {
		t.Fatal(err)
	}
	v, err := replacement[1009].Input(context.Background(), "{}", -1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Evaluate(context.Background(), v); err == nil {
		t.Fatal("accepted foreign argument generation")
	}
	if _, err := p.Evaluate(context.Background(), Null(replacement[1009])); err == nil {
		t.Fatal("strict NULL bypassed descriptor validation")
	}
	if _, err := p.Evaluate(context.Background(), Value{typ: types[1009]}); err == nil {
		t.Fatal("accepted missing array payload")
	}
	null, err := p.Evaluate(context.Background(), Null(types[1009]))
	if err != nil || !null.IsNull() || null.Type() != types[JSONB] {
		t.Fatal(null, err)
	}
	if !p.Immutable() || p.ResultUsage() != types[JSONB].Usage() {
		t.Fatal(p)
	}
}

func TestJSONBObjectProviderCancellationAndReuse(t *testing.T) {
	types := testTypes()
	array, text, result := types[1009], types[Text], types[JSONB]
	ctx := context.Background()
	p, err := BindBinary("jsonb_object", result.Usage(), array.Usage())
	if err != nil {
		t.Fatal(err)
	}
	prefix := strings.Repeat("x", 4*jsonbWorkChunk)
	value, err := ArrayValue(array, []ArrayDimension{{Lower: 1, Length: 8}}, []Value{
		String(text, prefix+"b"), String(text, strings.Repeat("\n", 4*jsonbWorkChunk)),
		String(text, prefix+"a"), String(text, "old"),
		String(text, prefix+"a"), Null(text),
		String(text, "a"), String(text, "true"),
	})
	if err != nil {
		t.Fatal(err)
	}
	source, err := array.Output(ctx, value)
	if err != nil {
		t.Fatal(err)
	}
	count := &groupingCancelContext{Context: ctx, at: int(^uint(0) >> 1)}
	retained, err := p.Evaluate(count, value)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"a": "true", "` + prefix + `a": null, "` + prefix + `b": "` + strings.Repeat(`\n`, 4*jsonbWorkChunk) + `"}`
	if retained.text != want {
		t.Fatal("normalization changed expected string/null nodes")
	}
	// Exercise every cancellation poll, including pair traversal, long shared-key
	// comparisons, duplicate compaction and rendering. No partial Value escapes.
	for at := 1; at <= count.calls; at++ {
		cancel := &groupingCancelContext{Context: ctx, at: at}
		got, err := p.Evaluate(cancel, value)
		if !errors.Is(err, context.Canceled) || got != (Value{}) || cancel.calls != at {
			t.Fatal("partial result", at, got, err, cancel.calls)
		}
	}
	empty, err := array.Input(ctx, "{}", -1)
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.Evaluate(ctx, empty)
	if err != nil || got.text != "{}" || got.jsonb == nil || got.jsonb.root.kind != jsonbObject {
		t.Fatal("empty payload/recovery", got, err)
	}
	after, err := array.Output(ctx, value)
	if err != nil || after != source {
		t.Fatal("mutated input", err)
	}
	if out, err := result.Output(ctx, retained); err != nil || out != want {
		t.Fatal("mutated retained output", err)
	}
}
