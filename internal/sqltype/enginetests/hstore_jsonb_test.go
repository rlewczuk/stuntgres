package enginetests

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func hstoreJSONBUnitTypes() map[sqltype.ID]*sqltype.Descriptor {
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, d := range sqltype.Builtins() { types[d.ID()] = d }
	return types
}

func TestHstoreJSONBConversion(t *testing.T) {
	ctx := context.Background()
	source := hstoreUnitType(t, 65000)
	result := hstoreJSONBUnitTypes()[sqltype.JSONB]
	for _, tc := range []struct { input, strict, loose string }{
		{"", `{}`, `{}`},
		{"a=>NULL", `{"a": null}`, `{"a": null}`},
		{`a=>"NULL"`, `{"a": "NULL"}`, `{"a": "NULL"}`},
		{"a=>012345", `{"a": "012345"}`, `{"a": "012345"}`},
		{"a=>1.00", `{"a": "1.00"}`, `{"a": 1.00}`},
		{"a=>-0", `{"a": "-0"}`, `{"a": 0}`},
		{"a=>1E+02", `{"a": "1E+02"}`, `{"a": 100}`},
		{"a=>1.230e-5", `{"a": "1.230e-5"}`, `{"a": 0.00001230}`},
		{"a=>2.345e+4", `{"a": "2.345e+4"}`, `{"a": 23450}`},
		{"a=>0e999999999", `{"a": "0e999999999"}`, `{"a": 0}`},
		{"a=>-0e999999999", `{"a": "-0e999999999"}`, `{"a": 0}`},
		{"a=>92233720368547758081234567890", `{"a": "92233720368547758081234567890"}`, `{"a": 92233720368547758081234567890}`},
		{"a=>t", `{"a": "t"}`, `{"a": true}`},
		{"a=>f", `{"a": "f"}`, `{"a": false}`},
		{"a=>true", `{"a": "true"}`, `{"a": "true"}`},
		{"a=>1e+", `{"a": "1e+"}`, `{"a": "1e+"}`},
		{"a=>-01", `{"a": "-01"}`, `{"a": "-01"}`},
		{`"a\"b"=>"c\\d"`, `{"a\"b": "c\\d"}`, `{"a\"b": "c\\d"}`},
		{"a=>\"\n\t\x01<>&\"", `{"a": "\n\t\u0001<>&"}`, `{"a": "\n\t\u0001<>&"}`},
		{`a=>"\\u0041"`, `{"a": "\\u0041"}`, `{"a": "\\u0041"}`},
		{"雪=>3,aa=>2,z=>1,é=>4", `{"z": "1", "aa": "2", "é": "4", "雪": "3"}`, `{"z": 1, "aa": 2, "é": 4, "雪": 3}`},
	} {
		original, err := source.Input(ctx, tc.input, -1)
		if err != nil { t.Fatal(err) }
		before, err := source.Output(ctx, original)
		if err != nil { t.Fatal(err) }
		for _, loose := range []bool{false, true} {
			got, err := original.HstoreJSONB(ctx, source, result, loose)
			if err != nil || got.Type() != result || !got.ValidResult(result.Usage()) || got.IsNull() { t.Fatal("conversion", got, err) }
			want := tc.strict
			if loose { want = tc.loose }
			output, err := result.Output(ctx, got)
			if err != nil || output != want { t.Fatalf("JSONB %q/%v want %q", output, err, want) }
			expected, err := result.Input(ctx, want, -1)
			if err != nil { t.Fatal(err) }
			group, err := sqltype.BindGrouping(result.Usage())
			if err != nil { t.Fatal(err) }
			if equal, err := group.Equal(ctx, got, expected); err != nil || !equal { t.Fatal("not a genuine comparable JSONB datum", equal, err) }
			a, err := group.Hash(ctx, got)
			if err != nil { t.Fatal(err) }
			if b, err := group.Hash(ctx, expected); err != nil || a != b { t.Fatal("structural JSONB hash", a, b, err) }
			if after, err := source.Output(ctx, original); err != nil || after != before { t.Fatal("source mutated", err) }
		}
	}
	for _, number := range []string{"1e131072", "1e-16384", "0e-999999999", "-0e-999999999"} {
		value, err := source.Input(ctx, "a=>"+number, -1)
		if err != nil { t.Fatal(err) }
		got, err := value.HstoreJSONB(ctx, source, result, true)
		var diagnostic *sqlstate.Error
		if got.Type() != nil || !errors.As(err, &diagnostic) || diagnostic.Code != "22003" || diagnostic.Message != "value overflows numeric format" || diagnostic.Detail != "" || diagnostic.Hint != "" || diagnostic.Position != "" || diagnostic.Context != "" { t.Fatal("loose range", got, err) }
		strict, err := value.HstoreJSONB(ctx, source, result, false)
		if err != nil { t.Fatal(err) }
		if output, err := result.Output(ctx, strict); err != nil || output != `{"a": "`+number+`"}` { t.Fatal("strict numeric-looking string", output, err) }
	}
	for _, tc := range []struct{ number, output string }{{"1e131071", "1"+strings.Repeat("0",131071)}, {"1e-16383", "0."+strings.Repeat("0",16382)+"1"}} {
		value, err := source.Input(ctx, "a=>"+tc.number, -1)
		if err != nil { t.Fatal(err) }
		got, err := value.HstoreJSONB(ctx, source, result, true)
		if err != nil { t.Fatal(err) }
		if output, err := result.Output(ctx, got); err != nil || output != `{"a": `+tc.output+`}` { t.Fatal("numeric accepted boundary", err) }
	}
}

func TestHstoreJSONBConversionOwnership(t *testing.T) {
	ctx := context.Background()
	types := hstoreJSONBUnitTypes()
	source, foreign := hstoreUnitType(t,65000), hstoreUnitType(t,65000)
	result := types[sqltype.JSONB]
	value, err := source.Input(ctx, "z=>1.00,a=>NULL,b=>t", -1)
	if err != nil { t.Fatal(err) }
	for _, loose := range []bool{false,true} {
		got, err := sqltype.Null(source).HstoreJSONB(ctx, source, result, loose)
		if err != nil || !got.IsNull() || got.Type() != result { t.Fatal("typed SQL NULL", err) }
		for _, bad := range []sqltype.Value{{}, sqltype.String(types[sqltype.Text],"a=>1"), sqltype.Null(foreign)} {
			if got, err := bad.HstoreJSONB(ctx,source,result,loose); err == nil || got.Type() != nil { t.Fatal("foreign source admitted",err) }
		}
		for _, bad := range []*sqltype.Descriptor{nil, types[sqltype.Text], types[sqltype.JSON], types[3807], source} {
			for _, input := range []sqltype.Value{value,sqltype.Null(source)} {
				if got, err := input.HstoreJSONB(ctx,source,bad,loose); err == nil || got.Type() != nil { t.Fatal("wrong result admitted, including NULL",err) }
			}
		}
		for _, bad := range []*sqltype.Descriptor{nil,types[sqltype.Text],foreign} {
			if got, err := value.HstoreJSONB(ctx,bad,result,loose); err == nil || got.Type() != nil { t.Fatal("foreign formal admitted",err) }
		}
	}
	if _, err := value.Retag(foreign); err == nil { t.Fatal("equal OID granted source ownership") }
	mapped, err := value.RemapHstore(foreign)
	if err != nil { t.Fatal(err) }
	if same, err := value.SamePayload(ctx,mapped); err != nil || !same { t.Fatal("authorized remap copied or changed frozen source",err) }
	converted, err := mapped.HstoreJSONB(ctx,foreign,result,true)
	if err != nil { t.Fatal(err) }
	if text, err := result.Output(ctx,converted); err != nil || text != `{"a": null, "b": true, "z": 1.00}` { t.Fatal("remapped conversion",text,err) }
	if _, err := mapped.HstoreJSONB(ctx,source,result,true); err == nil { t.Fatal("old generation authorized remapped datum") }
}

func TestHstoreJSONBConversionCancellation(t *testing.T) {
	source := hstoreUnitType(t,65000)
	result := hstoreJSONBUnitTypes()[sqltype.JSONB]
	ctx := context.Background()
	for _, input := range []string{"a=>"+strings.Repeat("1",32768), "a=>\""+strings.Repeat("x",32768)+"\"", "\""+strings.Repeat("k",32768)+"\"=>t"} {
		value, err := source.Input(ctx,input,-1)
		if err != nil { t.Fatal(err) }
		for _, remaining := range []int{1,5,8} {
			base,cancel := context.WithCancel(ctx)
			reached := &hstoreCancelContext{Context:base,cancel:cancel,remaining:remaining}
			got, err := value.HstoreJSONB(reached,source,result,true)
			cancel()
			if !errors.Is(err,context.Canceled) || got.Type() != nil { t.Fatal("partial JSONB publication on cancellation",got,err) }
		}
	}
}
