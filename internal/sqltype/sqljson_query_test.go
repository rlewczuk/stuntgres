package sqltype

import (
	"context"
	"errors"
	"stuntgres/internal/sqlstate"
	"testing"
	"time"
)

func TestSQLJSONQueryPlan(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	bind := func(kind, wrapper string, result ID, passing []Usage, names []string) SQLJSONQueryPlan {
		t.Helper()
		p, err := BindSQLJSONQuery(kind, wrapper, false, types[result].Usage(), types[JSONB].Usage(), types[JSONPath].Usage(), types[JSONB].Usage(), passing, names)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	document := jsonbSetUnitInput(t, types[JSONB], `[{"x":1},{}]`)
	p := bind("json_query", "unconditional", JSONB, nil, nil)
	outcome, err := p.Evaluate(ctx, document, jsonbSetUnitInput(t, types[JSONPath], `strict $[*].x`), nil, InputEnvironment{}, OutputEnvironment{})
	if err != nil || outcome.Error == nil || outcome.Value.Type() != nil {
		t.Fatalf("structural prefix leaked: %+v %v", outcome, err)
	}
	_, err = p.Evaluate(ctx, document, jsonbSetUnitInput(t, types[JSONPath], `$missing`), nil, InputEnvironment{}, OutputEnvironment{})
	if diagnostic, ok := err.(*sqlstate.Error); !ok || diagnostic.Code != "42704" {
		t.Fatalf("ordinary variable error: %v", err)
	}
	_, err = p.Evaluate(ctx, document, jsonbSetUnitInput(t, types[JSONPath], `$.double()`), nil, InputEnvironment{}, OutputEnvironment{})
	if diagnostic, ok := err.(*sqlstate.Error); !ok || diagnostic.Code != "0A000" {
		t.Fatalf("unsupported error: %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = p.Evaluate(canceled, Null(types[JSONB]), Null(types[JSONPath]), nil, InputEnvironment{}, OutputEnvironment{})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	p = bind("json_value", "", Int4, []Usage{types[Int4].Usage(), types[Int4].Usage()}, []string{"x", "x"})
	first := jsonbSetUnitInput(t, types[Int4], "1")
	second := jsonbSetUnitInput(t, types[Int4], "2")
	outcome, err = p.Evaluate(ctx, document, jsonbSetUnitInput(t, types[JSONPath], `$x`), []Value{first, second}, InputEnvironment{}, OutputEnvironment{})
	if err != nil || outcome.Error != nil || outcome.Value.integer != 1 {
		t.Fatal(outcome, err)
	}
	// Descriptor identity, rather than equal type IDs, owns executable inputs.
	foreign := testTypes()
	_, err = p.Evaluate(ctx, Null(foreign[JSONB]), Null(types[JSONPath]), []Value{first, second}, InputEnvironment{}, OutputEnvironment{})
	if err == nil {
		t.Fatal("foreign document accepted")
	}

	zone, err := ResolveZone("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	input := InputEnvironment{Zone: zone, TransactionStart: time.Date(2024, 3, 10, 15, 0, 0, 0, time.UTC)}
	a := jsonbSetUnitInput(t, types[Time], "01:00")
	b := jsonbSetUnitInput(t, types[Timetz], "01:00-08")
	comparison, ok, err := compareSQLJSONTemporal(ctx, a, b, input)
	if err != nil || !ok || comparison != 0 {
		t.Fatalf("DST civil-date offset: %d %t %v", comparison, ok, err)
	}
	path := jsonbSetUnitInput(t, types[JSONPath], `$a == $b`)
	if !SQLJSONPathMutable(path, []string{"a", "b"}, []Usage{types[Date].Usage(), types[Timestamptz].Usage()}) {
		t.Fatal("mixed-zone path immutable")
	}
	if SQLJSONPathMutable(jsonbSetUnitInput(t, types[JSONPath], `$a`), []string{"a"}, []Usage{types[Timestamptz].Usage()}) {
		t.Fatal("bare temporal path mutable")
	}
}

func TestSQLJSONPathMutable(t *testing.T) {
	types := testTypes()
	for _, tc := range []struct {
		name, path string
		want       bool
	}{
		{"direct", `$a == $b`, true},
		{"filter current", `$a ? (@ == $b)`, true},
		{"filter output", `($a ? (@ == $a)) == $b`, true},
		{"chained filter", `$a ? (@ == $a) ? (@ == $b)`, true},
		{"nested current restored mixed", `$a ? (exists ($b ? (@ == $b)) && @ == $b)`, true},
		{"nested current restored same", `$a ? (exists ($b ? (@ == $b)) && @ == $a)`, false},
		{"nested current zoned", `$a ? (exists ($b ? (@ == $a)))`, true},
		{"nested continuation", `$a ? (exists ($b ? (@ == $b))) ? (@ == $b)`, true},
		{"lax index", `$a[0] == $b`, true},
		{"explicit lax index", `lax $a[0] == $b`, true},
		{"lax wildcard", `$a[*] == $b`, true},
		{"lax range", `$a[0 to 1] == $b`, true},
		{"strict index", `strict $a[0] == $b`, false},
		{"strict wildcard", `strict $a[*] == $b`, false},
		{"strict range", `strict $a[0 to 1] == $b`, false},
		{"lax last index", `$a[last] == $b`, true},
		{"strict last index", `strict $a[last] == $b`, false},
		{"lax current index", `$a ? (@[0] == $b)`, true},
		{"strict current index", `strict $a ? (@[0] == $b)`, false},
		{"same nonzoned filter", `$a ? (@ == $c)`, false},
		{"same zoned filter", `$b ? (@ == $z)`, false},
		{"same nonzoned accessor", `$a[*] == $c`, false},
		{"same zoned accessor", `$b[0] == $z`, false},
		{"key clears", `$a.x == $b`, false},
		{"any key clears", `$a.* == $b`, false},
		{"method clears", `$a.type() == $b`, false},
		{"size clears", `$a.size() == $b`, false},
		{"boolean clears", `$a.boolean() == $b`, false},
		{"string clears", `$a.string() == $b`, false},
		{"keyvalue clears", `$a.keyvalue() == $b`, false},
		{"method preserves nested work", `($a ? (@ == $b)).type() == $b`, true},
		{"recursive default", `$a.** == $b`, true},
		{"recursive zero", `$a.**{0} == $b`, true},
		{"recursive zero range", `$a.**{0 to 2} == $b`, true},
		{"recursive positive", `$a.**{1 to 2} == $b`, false},
		{"recursive last", `$a.**{last} == $b`, false},
		{"recursive positive last", `$a.**{1 to last} == $b`, false},
		{"strict recursive zero", `strict $a.**{0} == $b`, true},
		{"index bound", `strict $a[$c ? (@ == $b)]`, true},
		{"range upper bound", `strict $a[0 to $c ? (@ == $b)]`, true},
		{"range lower bound", `strict $a[$c ? (@ == $b) to 1]`, true},
		{"bound does not replace input", `$a[$b] == $c`, false},
		{"bound retains current", `$a ? (exists (@[$c ? (@ == $c)]) && @ == $b)`, true},
		{"boolean left", `($a == $b) && ($a == $c)`, true},
		{"boolean right", `($a == $c) || ($a == $b)`, true},
		{"not", `!($a == $b)`, true},
		{"unknown", `($a == $b) is unknown`, true},
		{"exists", `exists ($a ? (@ == $b))`, true},
		{"missing binding", `$missing == $b`, false},
		{"non temporal binding", `$n == $b`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := jsonbSetUnitInput(t, types[JSONPath], tc.path)
			got := SQLJSONPathMutable(path, []string{"a", "b", "c", "z", "n"}, []Usage{
				types[Timestamp].Usage(), types[Timestamptz].Usage(),
				types[Timestamp].Usage(), types[Timestamptz].Usage(), types[Text].Usage(),
			})
			if got != tc.want {
				t.Fatalf("%s: mutable = %t, want %t", tc.path, got, tc.want)
			}
		})
	}
	for _, id := range []ID{Date, Time, Timestamp, Timetz, Timestamptz} {
		usage := types[id].Usage()
		if SQLJSONPathMutable(jsonbSetUnitInput(t, types[JSONPath], `$v`), []string{"v"}, []Usage{usage}) {
			t.Fatalf("bare %v variable is mutable", id)
		}
		// Values are deliberately NULL: declared usages still classify them.
		value := Null(types[id])
		want := id == Date || id == Time || id == Timestamp
		if got := SQLJSONPathMutable(jsonbSetUnitInput(t, types[JSONPath], `$v == $b`),
			[]string{"v", "b"}, []Usage{value.Type().Usage(), types[Timestamptz].Usage()}); got != want {
			t.Fatalf("typed NULL %v: mutable = %t, want %t", id, got, want)
		}
	}
	path := jsonbSetUnitInput(t, types[JSONPath], `$a == $b`)
	for _, tc := range []struct {
		first ID
		want  bool
	}{{Timestamp, true}, {Timestamptz, false}, {Text, false}} {
		if got := SQLJSONPathMutable(path, []string{"a", "a", "b"},
			[]Usage{types[tc.first].Usage(), types[Timestamp].Usage(), types[Timestamptz].Usage()}); got != tc.want {
			t.Fatalf("first duplicate %v: mutable = %t, want %t", tc.first, got, tc.want)
		}
	}
	if SQLJSONPathMutable(Null(types[JSONPath]), nil, nil) {
		t.Fatal("constant NULL path is mutable")
	}
}
