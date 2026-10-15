package sqltype

import (
	"context"
	"errors"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestJSONStripNullsValues(t *testing.T) {
	d := testTypes()
	p, err := BindJSONStripNulls(d[JSON].Usage(), d[JSON].Usage(), d[Bool].Usage())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ source, ordinary, arrays string }{
		{`{"a":1,"b":null,"c":[2,null,3],"d":{"e":4,"f":null}}`, `{"a":1,"c":[2,null,3],"d":{"e":4}}`, `{"a":1,"c":[2,3],"d":{"e":4}}`},
		{`[null,null,{"a":null},[],[null,null],null]`, `[null,null,{},[],[null,null],null]`, `[{},[],[]]`},
		{`{"a":{"b":null},"d":{},"e":[null]}`, `{"a":{},"d":{},"e":[null]}`, `{"a":{},"d":{},"e":[]}`},
		{`{"k":1,"k":null,"k":2}`, `{"k":1,"k":2}`, `{"k":1,"k":2}`},
		{" \n" + `{"\u0061":1.2300E+04,"a":null,"s":"\u0062\/","n":-0,"huge":1e131072}`, `{"a":1.2300E+04,"s":"b/","n":-0,"huge":1e131072}`, `{"a":1.2300E+04,"s":"b/","n":-0,"huge":1e131072}`},
		{`null`, `null`, `null`}, {`1`, `1`, `1`}, {`true`, `true`, `true`}, {`false`, `false`, `false`}, {`"a string"`, `"a string"`, `"a string"`}, {`{}`, `{}`, `{}`}, {`[]`, `[]`, `[]`},
		{`{"s":"\"\\\b\f\n\r\t","face":"\uD83D\uDE00"}`, `{"s":"\"\\\b\f\n\r\t","face":"😀"}`, `{"s":"\"\\\b\f\n\r\t","face":"😀"}`},
	} {
		source := jsonbSetUnitInput(t, d[JSON], tc.source)
		for _, arrays := range []bool{false, true} {
			want := tc.ordinary
			if arrays {
				want = tc.arrays
			}
			got, err := p.Evaluate(context.Background(), source, Boolean(d[Bool], arrays))
			if err != nil || got.Type() != d[JSON] || got.IsNull() || got.text != want || source.text != tc.source {
				t.Fatalf("source=%q flag=%v got=%q want=%q error=%v", tc.source, arrays, got.text, want, err)
			}
		}
	}
	for _, source := range []string{`{"\u0000":null}`, `["\u0000"]`, `{"x":"\uD800"}`} {
		_, err := p.Evaluate(context.Background(), jsonbSetUnitInput(t, d[JSON], source), Boolean(d[Bool], true))
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Context == "" || diagnostic.Position != "" {
			t.Fatal(source, err)
		}
		code := "22P05"
		if strings.Contains(source, "D800") {
			code = "22P02"
		}
		if diagnostic.Code != code {
			t.Fatal(diagnostic)
		}
	}
}

func TestJSONStripNullsDescriptorAuthority(t *testing.T) {
	d, fresh := testTypes(), testTypes()
	p, err := BindJSONStripNulls(d[JSON].Usage(), d[JSON].Usage(), d[Bool].Usage())
	if err != nil || p.ResultUsage() != d[JSON].Usage() {
		t.Fatal(p, err)
	}
	for _, args := range [][3]Usage{
		{d[Text].Usage(), d[JSON].Usage(), d[Bool].Usage()},
		{d[JSON].Usage(), d[JSONB].Usage(), d[Bool].Usage()},
		{d[JSON].Usage(), d[JSON].Usage(), d[Text].Usage()},
		{fresh[JSON].Usage(), d[JSON].Usage(), d[Bool].Usage()},
		{d[JSON].Usage(), fresh[JSON].Usage(), d[Bool].Usage()},
		{Usage{}, d[JSON].Usage(), d[Bool].Usage()},
	} {
		if _, err := BindJSONStripNulls(args[0], args[1], args[2]); err == nil {
			t.Fatal("accepted invalid usage", args)
		}
	}
	bad := d[JSON].Usage()
	bad.Typmod = 1
	if _, err := BindJSONStripNulls(bad, d[JSON].Usage(), d[Bool].Usage()); err == nil {
		t.Fatal("accepted typmod")
	}
	for _, values := range [][2]Value{{Null(fresh[JSON]), Null(d[Bool])}, {Null(d[JSON]), Null(fresh[Bool])}} {
		got, err := p.Evaluate(context.Background(), values[0], values[1])
		if err == nil || got != (Value{}) {
			t.Fatal("NULL bypassed validation", got, err)
		}
	}
	for _, values := range [][2]Value{{Null(d[JSON]), Boolean(d[Bool], true)}, {jsonbSetUnitInput(t, d[JSON], `{"x":"\u0000"}`), Null(d[Bool])}, {Null(d[JSON]), Null(d[Bool])}} {
		got, err := p.Evaluate(context.Background(), values[0], values[1])
		if err != nil || !got.IsNull() || got.Type() != d[JSON] {
			t.Fatal(got, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := p.Evaluate(ctx, Null(d[JSON]), Null(d[Bool])); !errors.Is(err, context.Canceled) || got != (Value{}) {
		t.Fatal(got, err)
	}
}

func TestJSONStripNullsIterativeCancellation(t *testing.T) {
	d := testTypes()
	p, err := BindJSONStripNulls(d[JSON].Usage(), d[JSON].Usage(), d[Bool].Usage())
	if err != nil {
		t.Fatal(err)
	}
	deep := strings.Repeat("[", 4096) + "null" + strings.Repeat("]", 4096)
	got, err := p.Evaluate(context.Background(), jsonbSetUnitInput(t, d[JSON], deep), Boolean(d[Bool], true))
	if err != nil || got.text != strings.Repeat("[", 4096)+strings.Repeat("]", 4096) {
		t.Fatal("deep iterative stripping", err)
	}
	for _, source := range []string{
		deep, "[" + strings.Repeat("null,", 65536) + "1]", `"` + strings.Repeat("x", 65536) + `"`, `"` + strings.Repeat(`\n`, 65536) + `"`, "1" + strings.Repeat("0", 65536),
	} {
		value := jsonbSetUnitInput(t, d[JSON], source)
		for _, at := range []int{2, 10, 20} {
			ctx := &groupingCancelContext{Context: context.Background(), at: at}
			got, err := p.Evaluate(ctx, value, Boolean(d[Bool], true))
			if !errors.Is(err, context.Canceled) || got != (Value{}) || ctx.calls != at {
				t.Fatal("cancellation published output", at, ctx.calls, err)
			}
		}
	}
	// Bounded polls also protect output writing, after lexing has completed.
	for _, text := range []string{strings.Repeat("x", 65536), strings.Repeat("\n", 65536)} {
		var out strings.Builder
		ctx := &groupingCancelContext{Context: context.Background(), at: 2}
		if err := writeJSONBString(ctx, &out, text); !errors.Is(err, context.Canceled) || out.Len() >= len(text) {
			t.Fatal("output cancellation", err, out.Len())
		}
	}
}
