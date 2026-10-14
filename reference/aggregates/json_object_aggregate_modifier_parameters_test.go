package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// Root's pinned PG18.0 oracle confirmed modifier-driven ANY and
// occurrence-sensitive parameter expectations before native remediation.
func TestReferenceJSONObjectAggregateModifierParameters(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			t.Run("focused-smoke", func(t *testing.T) { jsonObjectAggregateModifierSmoke(t, p, b) })
			for _, f := range jsonObjectAggregateFamilies {
				t.Run(f.name, func(t *testing.T) {
					for _, modifier := range []string{"distinct", "order", "distinct-order"} {
						for _, slot := range []string{"key", "value", "both"} {
							t.Run(modifier+"/"+slot, func(t *testing.T) {
								args, order := "$1,1", "$1"
								goodArgs, nextArgs := []any{"x"}, []any{"y"}
								good, next := `{ "x" : 1 }`, `{ "y" : 1 }`
								nullArgs := []any{nil}
								nullWant := f.nullValue
								if f.typ == "JSONB" {
									good, next = `{"x": 1}`, `{"y": 1}`
								}
								if slot == "value" {
									args = "'a',$1"
									good, next = `{ "a" : "x" }`, `{ "a" : "y" }`
									if f.typ == "JSONB" {
										good, next = `{"a": "x"}`, `{"a": "y"}`
									}
								}
								if slot == "both" {
									args, order = "$1,$2", "$1,$2"
									goodArgs, nextArgs = []any{"x", "one"}, []any{"y", "two"}
									nullArgs = []any{nil, nil}
									good, next = `{ "x" : "one" }`, `{ "y" : "two" }`
									if f.typ == "JSONB" {
										good, next = `{"x": "one"}`, `{"y": "two"}`
									}
								}
								callArgs := args
								if modifier != "order" {
									callArgs = "DISTINCT " + callArgs
								}
								if modifier != "distinct" {
									callArgs += " ORDER BY " + order
								}
								q := "/*é😀*/ SELECT " + f.name + "(" + callArgs + ") AS v"
								jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect(q, f.typ, good, goodArgs...))
								// A SAME held statement must preserve modifier-inferred TEXT
								// through NULL/error/recovery, not infer from each binding.
								stmt, err := b.DB.PrepareContext(p.Ctx, q)
								if err != nil {
									t.Fatal(err)
								}
								defer stmt.Close()
								held := fixture.HeldStatement{b.DB, stmt, q}
								fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonArrayAggregateSelect(q, f.typ, good, goodArgs...))
								if slot == "value" {
									jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect(q, f.typ, nullWant, nullArgs...))
									fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonArrayAggregateSelect(q, f.typ, nullWant, nullArgs...))
								} else {
									jsonArrayAggregateError(t, p, b, q, jsonObjectAggregateNullKey(f.typ), false, nullArgs...)
									for _, query := range []bool{true, false} {
										s := fixture.ErrorDiagnostic(q, jsonObjectAggregateNullKey(f.typ), nullArgs...)
										s.SelectRows, s.ErrorStage = query, "execute"
										fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
									}
								}
								fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonArrayAggregateSelect(q, f.typ, next, nextArgs...))
							})
						}
						t.Run(modifier+"/same-number-input-occurrences", func(t *testing.T) {
							args := "$1,$1"
							if modifier != "order" {
								args = "DISTINCT " + args
							}
							if modifier != "distinct" {
								args += " ORDER BY $1"
							}
							q := "/*é😀*/ SELECT " + f.name + "(" + args + ") AS v"
							if modifier == "order" {
								// ORDER chooses the first equal target; the second
								// earlier input occurrence is not itself resolved.
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
						})
						// The aggregate's own modifier resolves only its own unknown
						// occurrence. An earlier, different ANY occurrence still fails
						// at its Unicode character position after the shared slot is typed.
						for _, args := range []string{"$1,1", "'a',$1"} {
							callArgs := args
							if modifier != "order" {
								callArgs = "DISTINCT " + callArgs
							}
							if modifier != "distinct" {
								callArgs += " ORDER BY $1"
							}
							q := "/*é😀*/ SELECT " + f.name + "(" + args + ") AS earlier," + f.name + "(" + callArgs + ") AS v"
							d := fixture.ControlsReferenceDiagnostic("42P08", "could not determine data type of parameter $1")
							d.Position = jsonArrayAggregatePosition(q, "$1")
							jsonArrayAggregateError(t, p, b, q, d, true, "2")
							// A later explicit integer cast consumes already-inferred
							// TEXT; it must not retroactively change the aggregate value.
							q = "/*é😀*/ SELECT " + f.name + "(" + callArgs + ") AS v,$1::int AS n"
							want := `{ "2" : 1 }`
							if f.typ == "JSONB" {
								want = `{"2": 1}`
							}
							if args == "'a',$1" {
								want = `{ "a" : "2" }`
								if f.typ == "JSONB" {
									want = `{"a": "2"}`
								}
							}
							jsonArrayAggregateRoutes(t, p, b, fixture.Select(q, []string{"v", "n"}, []string{f.typ, "INT4"}, [][]any{{want, int64(2)}}, "2"))
							// Typed-first reuse keeps INTEGER at the ANY input even
							// when a modifier would otherwise default UNKNOWN to TEXT.
							q = "/*é😀*/ SELECT $1::int AS n," + f.name + "(" + callArgs + ") AS v"
							if args == "'a',$1" {
								want = `{ "a" : 2 }`
								if f.typ == "JSONB" {
									want = `{"a": 2}`
								}
							}
							jsonArrayAggregateRoutes(t, p, b, fixture.Select(q, []string{"n", "v"}, []string{"INT4", f.typ}, [][]any{{int64(2), want}}, 2))
						}
					}
				})
			}
		})
	}
}

func jsonObjectAggregateModifierSmoke(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	const q = "SELECT jsonb_object_agg_strict($1::text,$2 ORDER BY $2) AS v"
	jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect(q, "JSONB", `{"a": "x"}`, "a", "x"))
	stmt, err := b.DB.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{b.DB, stmt, q}
	fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonArrayAggregateSelect(q, "JSONB", `{"a": "x"}`, "a", "x"))
	fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonArrayAggregateSelect(q, "JSONB", `{}`, "a", nil))
	for _, query := range []bool{true, false} {
		s := fixture.ErrorDiagnostic(q, jsonObjectAggregateNullKey("JSONB"), nil, "x")
		s.SelectRows, s.ErrorStage = query, "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	}
	fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonArrayAggregateSelect(q, "JSONB", `{"b": "y"}`, "b", "y"))
}
