package enginetests

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/sqltype"
)

func TestHstoreJSONConversion(t *testing.T) {
	ctx := context.Background()
	source := hstoreUnitType(t, 65000)
	var result, text, binary *sqltype.Descriptor
	for _, d := range sqltype.Builtins() {
		switch d.ID() {
		case sqltype.JSON:
			result = d
		case sqltype.Text:
			text = d
		case sqltype.JSONB:
			binary = d
		}
	}
	for _, tc := range []struct{ input, strict, loose string }{
		{"", `{}`, `{}`},
		{"a=>NULL", `{"a": null}`, `{"a": null}`},
		{`a=>"NULL"`, `{"a": "NULL"}`, `{"a": "NULL"}`},
		{"a=>012345", `{"a": "012345"}`, `{"a": "012345"}`},
		{"a=>1.00", `{"a": "1.00"}`, `{"a": 1.00}`},
		{"a=>1e999999", `{"a": "1e999999"}`, `{"a": 1e999999}`},
		{"a=>-0", `{"a": "-0"}`, `{"a": -0}`},
		{"a=>t", `{"a": "t"}`, `{"a": true}`},
		{"a=>f", `{"a": "f"}`, `{"a": false}`},
		{"a=>true", `{"a": "true"}`, `{"a": "true"}`},
		{"a=>1e+", `{"a": "1e+"}`, `{"a": "1e+"}`},
		{"a=>-01", `{"a": "-01"}`, `{"a": "-01"}`},
		{`"é"=>"雪"`, `{"é": "雪"}`, `{"é": "雪"}`},
		{`"a\"b"=>"c\\d"`, `{"a\"b": "c\\d"}`, `{"a\"b": "c\\d"}`},
		{"a=>\"\n\t\x01<>&\"", `{"a": "\n\t\u0001<>&"}`, `{"a": "\n\t\u0001<>&"}`},
	} {
		original, err := source.Input(ctx, tc.input, -1)
		if err != nil {
			t.Fatal(err)
		}
		before, err := source.Output(ctx, original)
		if err != nil {
			t.Fatal(err)
		}
		for _, loose := range []bool{false, true} {
			got, err := original.HstoreJSON(ctx, source, result, loose)
			if err != nil || got.Type() != result || !got.ValidResult(result.Usage()) || got.IsNull() {
				t.Fatal("conversion", got, err)
			}
			want := tc.strict
			if loose {
				want = tc.loose
			}
			output, err := result.Output(ctx, got)
			if err != nil || output != want {
				t.Fatalf("JSON %q/%v want %q", output, err, want)
			}
			after, err := source.Output(ctx, original)
			if err != nil || after != before {
				t.Fatal("source mutated", err)
			}
		}
	}
	value, err := source.Input(ctx, "a=>1", -1)
	if err != nil {
		t.Fatal(err)
	}
	for _, loose := range []bool{false, true} {
		null, err := sqltype.Null(source).HstoreJSON(ctx, source, result, loose)
		if err != nil || !null.IsNull() || null.Type() != result {
			t.Fatal("typed SQL NULL", err)
		}
		for _, bad := range []sqltype.Value{{}, sqltype.String(text, "a=>1"), sqltype.Null(hstoreUnitType(t, source.ID()))} {
			if got, err := bad.HstoreJSON(ctx, source, result, loose); err == nil || got.Type() != nil {
				t.Fatal("foreign source accepted", err)
			}
		}
		for _, bad := range []*sqltype.Descriptor{nil, text, binary, source} {
			for _, input := range []sqltype.Value{value, sqltype.Null(source)} {
				if got, err := input.HstoreJSON(ctx, source, bad, loose); err == nil || got.Type() != nil {
					t.Fatal("wrong result accepted", err)
				}
			}
		}
		for _, bad := range []*sqltype.Descriptor{nil, text, hstoreUnitType(t, source.ID())} {
			if got, err := value.HstoreJSON(ctx, bad, result, loose); err == nil || got.Type() != nil {
				t.Fatal("foreign formal accepted", err)
			}
		}
	}
	for _, input := range []string{"a=>" + strings.Repeat("1", 32768), "a=>\"" + strings.Repeat("x", 32768) + "\""} {
		value, err := source.Input(ctx, input, -1)
		if err != nil {
			t.Fatal(err)
		}
		for _, remaining := range []int{1, 5, 8} {
			base, cancel := context.WithCancel(ctx)
			reached := &hstoreCancelContext{Context: base, cancel: cancel, remaining: remaining}
			got, err := value.HstoreJSON(reached, source, result, true)
			cancel()
			if !errors.Is(err, context.Canceled) || got.Type() != nil {
				t.Fatal("partial JSON cancellation publication", got, err)
			}
		}
	}
}
