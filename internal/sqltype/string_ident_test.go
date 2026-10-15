package sqltype

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestStringIdentParse(t *testing.T) {
	types := testTypes()
	text := types[Text]
	array := types[text.AssociatedArray()]
	if array.ID() != 1009 || text.ID() != 25 {
		t.Fatal("builtin array identity")
	}
	for _, tc := range []struct {
		input    string
		strict   bool
		elements []string
	}{
		{`"SomeSchema".someTable`, true, []string{"SomeSchema", "sometable"}},
		{`"a""b".""""`, true, []string{`a"b`, `"`}},
		{"\t A_1$ . \r B \v\f", true, []string{"a_1$", "b"}},
		{`ÄBC.İΣ.🙂X`, true, []string{"Äbc", "İΣ", "🙂x"}},
		{"\u00a0FOO", true, []string{"\u00a0foo"}},
		{`foo.boo[]`, false, []string{"foo", "boo"}},
		{`foo"unterminated`, false, []string{"foo"}},
		{strings.Repeat("A", 256), true, []string{strings.Repeat("a", 256)}},
	} {
		v, err := parseIdent(context.Background(), array, tc.input, tc.strict)
		if err != nil || v.Type() != array || v.ArrayRank() != 1 || v.ArrayLen() != len(tc.elements) {
			t.Fatal(tc, v, err)
		}
		if !reflect.DeepEqual(v.ArrayDimensions(), []ArrayDimension{{Lower: 1, Length: int32(len(tc.elements))}}) {
			t.Fatal(v.ArrayDimensions())
		}
		for i, want := range tc.elements {
			got := v.array.elements[i]
			part, err := got.Text()
			if err != nil || got.Type() != text || got.IsNull() || part != want {
				t.Fatal(i, got, err, want)
			}
		}
	}
}

func TestStringIdentDiagnostics(t *testing.T) {
	types := testTypes()
	array := types[types[Text].AssociatedArray()]
	for _, tc := range []struct{ input, detail string }{
		{"", ""}, {"123", ""}, {"foo bar", ""},
		{"foo.", `No valid identifier after ".".`}, {"foo.123", `No valid identifier after ".".`},
		{".foo", `No valid identifier before ".".`}, {"foo..bar", `No valid identifier before ".".`},
		{`""`, "Quoted identifier must not be empty."}, {`foo.""`, "Quoted identifier must not be empty."},
		{`"foo`, "String has unclosed double quotes."}, {`foo."bar`, "String has unclosed double quotes."},
	} {
		_, err := parseIdent(context.Background(), array, tc.input, true)
		var got *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: `string is not a valid identifier: "` + tc.input + `"`, Detail: tc.detail}
		if !errors.As(err, &got) || *got != want {
			t.Fatal(tc, err, want)
		}
		if tc.detail != "" {
			_, err = parseIdent(context.Background(), array, tc.input, false)
			if !errors.As(err, &got) || *got != want {
				t.Fatal("nonstrict malformed component", err)
			}
		}
	}
}

func TestStringIdentPlan(t *testing.T) {
	types := testTypes()
	fresh := testTypes()
	text, boolean := types[Text], types[Bool]
	array := types[text.AssociatedArray()]
	for _, reversed := range []bool{false, true} {
		roles := [4]bool{true, false}
		args := []Usage{text.Usage(), boolean.Usage()}
		values := [4]Value{String(text, "A.B[]"), Boolean(boolean, false)}
		if reversed {
			roles = [4]bool{false, true}
			args[0], args[1] = args[1], args[0]
			values[0], values[1] = values[1], values[0]
		}
		p, err := BindString("parse_ident", array.Usage(), text.Usage(), args, roles, false)
		if err != nil || !p.Strict() {
			t.Fatal(p, err)
		}
		v, err := p.Evaluate(context.Background(), values, InputEnvironment{}, OutputEnvironment{})
		if err != nil || v.Type() != array || v.ArrayLen() != 2 {
			t.Fatal(v, err)
		}
		values[0] = Null(args[0].Type)
		v, err = p.Evaluate(context.Background(), values, InputEnvironment{}, OutputEnvironment{})
		if err != nil || !v.IsNull() || v.Type() != array {
			t.Fatal(v, err)
		}
		values[0] = Null(fresh[args[0].Type.ID()])
		if _, err = p.Evaluate(context.Background(), values, InputEnvironment{}, OutputEnvironment{}); err == nil {
			t.Fatal("foreign runtime owner")
		}
	}
	// A NAME source contributes C collation to the result usage. That is a
	// valid derived usage, not a foreign array/element descriptor generation.
	derived := array.Usage()
	derived.Collation = types[Name].Usage().Collation
	plan, err := BindString("parse_ident", derived, text.Usage(), []Usage{types[Name].Usage()}, [4]bool{true}, false)
	if err != nil || plan.Result() != derived {
		t.Fatal("valid derived result usage rejected", plan, err)
	}
	value, err := plan.Evaluate(context.Background(), [4]Value{String(types[Name], "A.B")}, InputEnvironment{}, OutputEnvironment{})
	if err != nil || value.Type() != array || value.ArrayLen() != 2 {
		t.Fatal(value, err)
	}
	foreign := fresh[array.ID()].Usage()
	foreign.Collation = derived.Collation
	if _, err := BindString("parse_ident", foreign, text.Usage(), []Usage{types[Name].Usage()}, [4]bool{true}, false); err == nil {
		t.Fatal("foreign derived result owner accepted")
	}
	modified := derived
	modified.Typmod = 3
	if _, err := BindString("parse_ident", modified, text.Usage(), []Usage{types[Name].Usage()}, [4]bool{true}, false); err == nil {
		t.Fatal("modified result typmod accepted")
	}
	for _, tc := range []struct {
		result Usage
		args   []Usage
		roles  [4]bool
	}{
		{fresh[array.ID()].Usage(), []Usage{text.Usage()}, [4]bool{true}},
		{array.Usage(), []Usage{fresh[Text].Usage()}, [4]bool{true}},
		{array.Usage(), []Usage{boolean.Usage()}, [4]bool{}},
		{array.Usage(), []Usage{text.Usage(), types[Int4].Usage()}, [4]bool{true}},
		{array.Usage(), []Usage{text.Usage(), text.Usage()}, [4]bool{true, true}},
	} {
		if _, err := BindString("parse_ident", tc.result, text.Usage(), tc.args, tc.roles, false); err == nil {
			t.Fatal("invalid plan accepted", tc)
		}
	}
}

func TestStringIdentCancellationWork(t *testing.T) {
	types := testTypes()
	array := types[types[Text].AssociatedArray()]
	for _, input := range []string{strings.Repeat("A", 1<<18), `"` + strings.Repeat("x", 1<<18) + `"`, strings.Repeat(" ", 1<<18) + "a", `"` + strings.Repeat(`a""`, 1<<17) + `z"`} {
		// Both early and late cancellation exercise scanning/materialization,
		// rather than relying on cancellation at the provider's entry.
		for _, cancelAt := range []int{3, 70} {
			ctx, cancel := context.WithCancel(context.Background())
			counted := &stringTrimCancellationContext{Context: ctx, cancel: cancel, cancelAt: cancelAt}
			v, err := parseIdent(counted, array, input, true)
			cancel()
			if !errors.Is(err, context.Canceled) || v.Type() != nil || counted.calls < cancelAt {
				t.Fatal(counted.calls, v, err)
			}
		}
	}
}
