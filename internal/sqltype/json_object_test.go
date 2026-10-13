package sqltype

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestJSONObjectProviderShapesAndValues(t *testing.T) {
	types := testTypes()
	array, result := types[1009], types[JSON]
	for _, tc := range []struct {
		inputs              []string
		want, code, message string
	}{
		{[]string{"{}"}, `{}`, "", ""},
		{[]string{"{}", "{}"}, `{}`, "", ""},
		{[]string{"[-2:1]={a,1,b,2}"}, `{"a" : "1", "b" : "2"}`, "", ""},
		{[]string{"[-1:0][3:4]={{a,1},{b,2}}"}, `{"a" : "1", "b" : "2"}`, "", ""},
		{[]string{"[-2:-1]={a,b}", "[7:8]={1,2}"}, `{"a" : "1", "b" : "2"}`, "", ""},
		{[]string{`{a,NULL,a,two,"",""}`}, `{"a" : null, "a" : "two", "" : ""}`, "", ""},
		{[]string{`{z,a,n,j}`, `{true,"null","NULL","{\"x\":1}"}`}, `{"z" : "true", "a" : "null", "n" : "NULL", "j" : "{\"x\":1}"}`, "", ""},
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
			p, err := BindBinary("json_object", result.Usage(), usages[:len(tc.inputs)]...)
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
			text, err := got.JSONText()
			if err != nil || text != tc.want || got.Type() != result {
				t.Fatal(text, tc.want, err)
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

func TestJSONObjectProviderEscapingStrictnessAndCancellation(t *testing.T) {
	types := testTypes()
	array, text, result := types[1009], types[Text], types[JSON]
	v, err := ArrayValue(array, []ArrayDimension{{Lower: -1, Length: 4}}, []Value{
		String(text, "\"\\"), String(text, "\b\f\n\r\t\x01"), String(text, "é😀"), String(text, "<>&/"),
	})
	if err != nil {
		t.Fatal(err)
	}
	one, err := BindBinary("json_object", result.Usage(), array.Usage())
	if err != nil {
		t.Fatal(err)
	}
	got, err := one.Evaluate(context.Background(), v)
	if err != nil {
		t.Fatal(err)
	}
	out, err := got.JSONText()
	if err != nil || out != `{"\"\\" : "\b\f\n\r\t\u0001", "é😀" : "<>&/"}` {
		t.Fatal(out, err)
	}
	two, err := BindBinary("json_object", result.Usage(), array.Usage(), array.Usage())
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

func TestJSONObjectProviderDescriptorContracts(t *testing.T) {
	types, replacement := testTypes(), testTypes()
	for _, args := range [][]Usage{nil, {types[1009].Usage(), types[1009].Usage(), types[1009].Usage()}, {types[Text].Usage()}, {types[1007].Usage()}, {types[1009].Usage(), replacement[1009].Usage()}} {
		if _, err := BindBinary("json_object", types[JSON].Usage(), args...); err == nil {
			t.Fatal("accepted invalid signature", args)
		}
	}
	if _, err := BindBinary("json_object", types[JSONB].Usage(), types[1009].Usage()); err == nil {
		t.Fatal("accepted JSONB result")
	}
	p, err := BindBinary("json_object", types[JSON].Usage(), types[1009].Usage())
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
	if !p.Immutable() || p.ResultUsage() != types[JSON].Usage() {
		t.Fatal(p)
	}
}
