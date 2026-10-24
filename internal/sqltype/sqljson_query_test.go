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
	outcome, err := p.Evaluate(ctx, document, jsonbSetUnitInput(t, types[JSONPath], `strict $[*].x`), nil, JSONPathEnvironment{}, OutputEnvironment{})
	if err != nil || outcome.Error == nil || outcome.Value.Type() != nil {
		t.Fatalf("structural prefix leaked: %+v %v", outcome, err)
	}
	_, err = p.Evaluate(ctx, document, jsonbSetUnitInput(t, types[JSONPath], `$missing`), nil, JSONPathEnvironment{}, OutputEnvironment{})
	if diagnostic, ok := err.(*sqlstate.Error); !ok || diagnostic.Code != "42704" {
		t.Fatalf("ordinary variable error: %v", err)
	}
	_, err = p.Evaluate(ctx, document, jsonbSetUnitInput(t, types[JSONPath], `$.double()`), nil, JSONPathEnvironment{}, OutputEnvironment{})
	if diagnostic, ok := err.(*sqlstate.Error); !ok || diagnostic.Code != "0A000" {
		t.Fatalf("unsupported error: %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = p.Evaluate(canceled, Null(types[JSONB]), Null(types[JSONPath]), nil, JSONPathEnvironment{}, OutputEnvironment{})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	p = bind("json_value", "", Int4, []Usage{types[Int4].Usage(), types[Int4].Usage()}, []string{"x", "x"})
	first := jsonbSetUnitInput(t, types[Int4], "1")
	second := jsonbSetUnitInput(t, types[Int4], "2")
	outcome, err = p.Evaluate(ctx, document, jsonbSetUnitInput(t, types[JSONPath], `$x`), []Value{first, second}, JSONPathEnvironment{}, OutputEnvironment{})
	if err != nil || outcome.Error != nil || outcome.Value.integer != 1 {
		t.Fatal(outcome, err)
	}
	// Descriptor identity, rather than equal type IDs, owns executable inputs.
	foreign := testTypes()
	_, err = p.Evaluate(ctx, Null(foreign[JSONB]), Null(types[JSONPath]), []Value{first, second}, JSONPathEnvironment{}, OutputEnvironment{})
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

// SQL/JSON's existing ON ERROR policy is diagnostic-class based, not COPY's
// source-soft policy. Both soft and hard input annotations remain transparent.
func TestSQLJSONMarkedInputConversion(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	path := jsonbSetUnitInput(t, types[JSONPath], "$")
	for _, tc := range []struct {
		name, document, code, message string
		result                        ID
		caught, soft                  bool
	}{
		{"array_bound", `"[1:0]={}"`, "2202E", "upper bound cannot be less than lower bound", 1007, true, true},
		{"range_bound", `"[3,1)"`, "22000", "range lower bound must be less than or equal to range upper bound", Int4Range, true, true},
		{"jsonpath_numeric", `"1e131072"`, "22003", "value overflows numeric format", JSONPath, true, false},
		{"jsonpath_scope", `"last"`, "42601", "LAST is allowed only in array subscripts", JSONPath, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := BindSQLJSONQuery("json_value", "", false, types[tc.result].Usage(), types[JSONB].Usage(), types[JSONPath].Usage(), types[JSONB].Usage(), nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			document := jsonbSetUnitInput(t, types[JSONB], tc.document)
			outcome, err := plan.Evaluate(ctx, document, path, nil, JSONPathEnvironment{}, OutputEnvironment{})
			cause := err
			if tc.caught {
				if err != nil || outcome.Error == nil {
					t.Fatal("conversion did not enter existing ON ERROR route", outcome, err)
				}
				cause = outcome.Error
			} else if err == nil || outcome.Error != nil {
				t.Fatal("ON ERROR broadened beyond existing diagnostic classes", outcome, err)
			}
			var diagnostic *sqlstate.Error
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message}
			if outcome.Value != (Value{}) || outcome.Empty || !errors.As(cause, &diagnostic) || *diagnostic != want || InputSoftError(cause) != tc.soft {
				t.Fatal("conversion changed source diagnostic or input authority", outcome, cause)
			}
			snapshot := *diagnostic
			behavior := SQLJSONBehaviorError("ERROR", "default", cause)
			if tc.caught {
				var record *sqlstate.Error
				expected := sqlstate.Error{
					Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804",
					Message: "could not coerce ON ERROR expression (DEFAULT) to the RETURNING type",
					Detail:  tc.message,
				}
				if !errors.As(behavior, &record) || *record != expected || InputSoftError(behavior) {
					t.Fatal("behavior lost the original message detail or invented soft input", behavior)
				}
			} else if behavior != cause {
				t.Fatal("non-conversion error identity changed", behavior, cause)
			}
			if *diagnostic != snapshot {
				t.Fatal("semantic consumer mutated source diagnostic", diagnostic, snapshot)
			}
		})
	}
	if SQLJSONBehaviorError("ERROR", "default", context.Canceled) != context.Canceled {
		t.Fatal("behavior changed cancellation identity")
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
