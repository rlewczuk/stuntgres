package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func jsonObjectUniqueAggregateModifierSmoke(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	const q = "SELECT jsonb_object_agg_unique_strict($1::text,$2 ORDER BY $2) AS v"
	jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect(q, "JSONB", `{"a": "1"}`, "a", 1))
	stmt, err := b.DB.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{b.DB, stmt, q}
	fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonArrayAggregateSelect(q, "JSONB", `{"a": "1"}`, "a", 1))
	fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonArrayAggregateSelect(q, "JSONB", `{}`, "a", nil))
	for _, query := range []bool{true, false} {
		s := fixture.ErrorDiagnostic(q, jsonObjectAggregateNullKey("JSONB"), nil, 1)
		s.SelectRows, s.ErrorStage = query, "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	}
	fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonArrayAggregateSelect(q, "JSONB", `{"b": "2"}`, "b", 2))
}

func TestReferenceJSONObjectUniqueAggregateModifiers(t *testing.T) {
	jsonObjectUniqueAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		jsonObjectUniqueAggregateModifierSmoke(t, p, b)
		for _, f := range jsonObjectUniqueAggregateFamilies {
			t.Run(f.name, func(t *testing.T) {
				for _, tc := range []struct {
					args, rows string
					want       any
					bad        bool
				}{
					{"DISTINCT k,v ORDER BY k,v", "('a',1),('a',1)", f.singleton, false},
					{"DISTINCT k,v", "('a',1),('a',2)", nil, true},
					{"ALL k,v", "('a',1),('a',1)", nil, true},
					{"DISTINCT k,v", "('a',NULL::int),('a',NULL::int)", f.nullValue, false},
					{"DISTINCT k,v", "('a',NULL::int),('a',1)", nil, true},
				} {
					q := "SELECT " + f.name + "(" + tc.args + ") AS v FROM(VALUES" + tc.rows + ") x(k,v)"
					if tc.bad {
						jsonArrayAggregateError(t, p, b, q, sqlJSONObjectAggregateDuplicate(f.typ == "JSONB", `"a"`), false)
					} else {
						jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect(q, f.typ, tc.want))
					}
				}
				for _, modifier := range []string{"distinct", "order", "distinct-order"} {
					for _, slot := range []string{"key", "value", "both"} {
						args, order := "$1,1", "$1"
						values := []any{"x"}
						nextValues := []any{"y"}
						nulls := []any{nil}
						want, next := `{ "x" : 1 }`, `{ "y" : 1 }`
						if f.typ == "JSONB" {
							want, next = `{"x": 1}`, `{"y": 1}`
						}
						if slot == "value" {
							args = "'a',$1"
							want, next = `{ "a" : "x" }`, `{ "a" : "y" }`
							if f.typ == "JSONB" {
								want, next = `{"a": "x"}`, `{"a": "y"}`
							}
						}
						if slot == "both" {
							args, order = "$1,$2", "$1,$2"
							values, nextValues, nulls = []any{"x", "one"}, []any{"y", "two"}, []any{nil, nil}
							want, next = `{ "x" : "one" }`, `{ "y" : "two" }`
							if f.typ == "JSONB" {
								want, next = `{"x": "one"}`, `{"y": "two"}`
							}
						}
						if modifier != "order" {
							args = "DISTINCT " + args
						}
						if modifier != "distinct" {
							args += " ORDER BY " + order
						}
						q := "/*é😀*/ SELECT " + f.name + "(" + args + ") AS v"
						jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect(q, f.typ, want, values...))
						stmt, err := b.DB.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						held := fixture.HeldStatement{b.DB, stmt, q}
						fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonArrayAggregateSelect(q, f.typ, want, values...))
						if slot == "value" {
							fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonArrayAggregateSelect(q, f.typ, f.nullValue, nulls...))
						} else {
							for _, query := range []bool{true, false} {
								s := fixture.ErrorDiagnostic(q, jsonObjectAggregateNullKey(f.typ), nulls...)
								s.SelectRows, s.ErrorStage = query, "execute"
								fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
							}
						}
						fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonArrayAggregateSelect(q, f.typ, next, nextValues...))
						if err = stmt.Close(); err != nil {
							t.Fatal(err)
						}
					}
					args := "$1,$1"
					if modifier != "order" {
						args = "DISTINCT " + args
					}
					if modifier != "distinct" {
						args += " ORDER BY $1"
					}
					q := "/*é😀*/ SELECT " + f.name + "(" + args + ") AS v"
					if modifier == "order" {
						d := fixture.ControlsReferenceDiagnostic("42P08", "could not determine data type of parameter $1")
						d.Position = jsonArrayAggregatePosition(q, "$1 ORDER")
						jsonArrayAggregateError(t, p, b, q, d, true, "x")
					} else {
						want := `{ "x" : "x" }`
						if f.typ == "JSONB" {
							want = `{"x": "x"}`
						}
						jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect(q, f.typ, want, "x"))
					}
					for _, base := range []string{"$1,1", "'a',$1"} {
						args = base
						if modifier != "order" {
							args = "DISTINCT " + args
						}
						if modifier != "distinct" {
							args += " ORDER BY $1"
						}
						q = "/*é😀*/ SELECT " + f.name + "(" + base + ") AS earlier," + f.name + "(" + args + ") AS v"
						d := fixture.ControlsReferenceDiagnostic("42P08", "could not determine data type of parameter $1")
						d.Position = jsonArrayAggregatePosition(q, "$1")
						jsonArrayAggregateError(t, p, b, q, d, true, "2")
						want := `{ "2" : 1 }`
						if f.typ == "JSONB" {
							want = `{"2": 1}`
						}
						if base == "'a',$1" {
							want = `{ "a" : "2" }`
							if f.typ == "JSONB" {
								want = `{"a": "2"}`
							}
						}
						q = "SELECT " + f.name + "(" + args + ") AS v,$1::int AS n"
						jsonArrayAggregateRoutes(t, p, b, fixture.Select(q, []string{"v", "n"}, []string{f.typ, "INT4"}, [][]any{{want, int64(2)}}, "2"))
						if base == "'a',$1" {
							want = `{ "a" : 2 }`
							if f.typ == "JSONB" {
								want = `{"a": 2}`
							}
						}
						jsonArrayAggregateRoutes(t, p, b, fixture.Select("SELECT $1::int AS n,"+f.name+"("+args+") AS v", []string{"n", "v"}, []string{"INT4", f.typ}, [][]any{{int64(2), want}}, 2))
					}
				}
			})
		}
	})
}
