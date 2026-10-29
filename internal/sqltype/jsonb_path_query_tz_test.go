package sqltype

import (
	"context"
	"errors"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestJSONPathQueryTZPolicy(t *testing.T) {
	types, fresh := testTypes(), testTypes()
	scalars, query := queryUnitPlans(t, types)
	usages := []Usage{types[JSONB].Usage(), types[JSONPath].Usage(), types[JSONB].Usage(), types[Bool].Usage()}
	array, err := BindJSONBPathQueryArray(types[JSONB].Usage(), usages...)
	if err != nil {
		t.Fatal(err)
	}
	scalars = append(scalars, array)
	ctx := context.Background()
	const doc = `"2015-08-01 12:00:00-05"`
	const vars = `{"limit":"2015-08-02"}`
	const filter = `$ ? (@.datetime() < $limit.datetime())`
	const predicate = `$.datetime() < $limit.datetime()`
	diagnostic := func(err error, code string) {
		t.Helper()
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != code {
			t.Fatalf("diagnostic %v want %s", err, code)
		}
	}
	for i, plan := range scalars {
		path := filter
		if i == 1 {
			path = predicate
		}
		for _, silent := range []bool{false, true} {
			values := navigateValues(t, types, doc, path, vars, silent)
			for _, east := range []int32{0, 9 * 3600, 0} {
				env := JSONPathEnvironment{Input: InputEnvironment{Zone: FixedZone("unit", east)}, Timezone: true}
				v, err := plan.Evaluate(ctx, values, env)
				if err != nil {
					t.Fatal(err)
				}
				if i < 2 {
					if v.typ != types[Bool] || v.null || v.boolean != (east == 0) {
						t.Fatal("current zone boolean", i, east, v)
					}
				} else {
					want := doc
					if i == 3 {
						want = `["2015-08-01 12:00:00-05"]`
						if east != 0 {
							want = `[]`
						}
					}
					if v.typ != types[JSONB] || v.null != (i == 2 && east != 0) || !v.null && v.text != want {
						t.Fatal("typed materialization", i, east, v)
					}
				}
				env.Timezone = false
				_, err = plan.Evaluate(ctx, values, env)
				diagnostic(err, "0A000")
			}
		}
		for slot := range 4 {
			values := navigateValues(t, types, doc, path, vars, false)
			values[slot] = Null(values[slot].typ)
			v, err := plan.Evaluate(ctx, values, JSONPathEnvironment{Timezone: true})
			if err != nil || !v.null {
				t.Fatal("strict NULL", i, slot, v, err)
			}
			values[slot] = Null(fresh[values[slot].typ.id])
			if _, err := plan.Evaluate(ctx, values, JSONPathEnvironment{Timezone: true}); err == nil {
				t.Fatal("foreign descriptor admitted")
			}
		}
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := plan.Evaluate(canceled, navigateValues(t, types, doc, path, vars, false), JSONPathEnvironment{Timezone: true}); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		_, err := plan.Evaluate(ctx, navigateValues(t, types, doc, `$missing`, `{}`, true), JSONPathEnvironment{Timezone: true})
		diagnostic(err, "42704")
	}
	values := navigateValues(t, types, doc, filter, vars, false)
	utc := JSONPathEnvironment{Timezone: true, Input: InputEnvironment{Zone: FixedZone("UTC", 0)}}
	old, err := query.Evaluate(ctx, values, utc)
	if err != nil || len(old) != 1 || old[0].typ != types[JSONB] || old[0].text != doc {
		t.Fatal(old, err)
	}
	tokyo := utc
	tokyo.Input.Zone = FixedZone("Tokyo", 9*3600)
	rows, err := query.Evaluate(ctx, values, tokyo)
	if err != nil || len(rows) != 0 {
		t.Fatal(rows, err)
	}
	if len(old) != 1 || old[0].text != doc {
		t.Fatal("later invocation mutated result")
	}
	ordinary := utc
	ordinary.Timezone = false
	rows, err = query.Evaluate(ctx, values, ordinary)
	diagnostic(err, "0A000")
	if rows != nil {
		t.Fatal("ordinary error published rows")
	}
	for slot := range 4 {
		nulls := append([]Value(nil), values...)
		nulls[slot] = Null(nulls[slot].typ)
		if rows, err := query.Evaluate(ctx, nulls, utc); err != nil || rows != nil {
			t.Fatal(rows, err)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if rows, err := query.Evaluate(canceled, values, utc); !errors.Is(err, context.Canceled) || rows != nil {
		t.Fatal(rows, err)
	}
	converted, err := array.Evaluate(ctx, navigateValues(t, types, `"2023-08-15"`, `$.timestamp_tz()`, `{}`, false), tokyo)
	if err != nil || converted.typ != types[JSONB] || converted.text != `["2023-08-15T00:00:00+09:00"]` {
		t.Fatal(converted, err)
	}
	bad := navigateValues(t, types, `[{"x":"2023-08-15"},{}]`, `strict $[*].x.timestamp_tz()`, `{}`, false)
	rows, err = query.Evaluate(ctx, bad, utc)
	diagnostic(err, "2203A")
	if rows != nil {
		t.Fatal("late error published prefix")
	}
	bad[3] = Boolean(types[Bool], true)
	rows, err = query.Evaluate(ctx, bad, utc)
	if err != nil || len(rows) != 1 || rows[0].text != `"2023-08-15T00:00:00+00:00"` {
		t.Fatal("silent prefix", rows, err)
	}
}
