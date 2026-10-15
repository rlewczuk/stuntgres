package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func jsonObjectUniqueAggregateHeld(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, f := range jsonObjectUniqueAggregateFamilies {
		t.Run(f.name, func(t *testing.T) {
			q := "SELECT " + f.name + "(k,v ORDER BY id) AS v FROM(VALUES(1,$1::text,$2::int),(2,$3::text,$4::int)) x(id,k,v)"
			stmt, err := b.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{b.DB, stmt, q}
			good := `{ "a" : 1, "b" : 2 }`
			omitted := `{ "b" : 2 }`
			if !f.strict {
				omitted = `{ "a" : null, "b" : 2 }`
			}
			if f.typ == "JSONB" {
				good = `{"a": 1, "b": 2}`
				omitted = `{"b": 2}`
				if !f.strict {
					omitted = `{"a": null, "b": 2}`
				}
			}
			duplicate := sqlJSONObjectAggregateDuplicate(f.typ == "JSONB", `"a"`)
			nullKey := jsonObjectAggregateNullKey(f.typ)
			for _, plain := range []bool{false, true} {
				for _, frame := range []struct {
					args       []any
					want       any
					diagnostic *fixture.Diagnostic
				}{
					{[]any{"a", 1, "b", 2}, good, nil},
					{[]any{"a", nil, "b", 2}, omitted, nil},
					{[]any{"a", nil, "a", 2}, nil, &duplicate},
					{[]any{"a", 1, "b", 2}, good, nil},
					{[]any{nil, nil, "b", 2}, nil, &nullKey},
					{[]any{"a", 1, "b", 2}, good, nil},
				} {
					if frame.diagnostic != nil {
						for _, query := range []bool{true, false} {
							s := fixture.ErrorDiagnostic(q, *frame.diagnostic, frame.args...)
							s.SelectRows, s.ErrorStage, s.NonContext = query, "execute", plain
							fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
						}
					} else {
						s := jsonArrayAggregateSelect(q, f.typ, frame.want, frame.args...)
						s.NonContext = plain
						fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
					}
				}
			}
		})
	}
}

func TestReferenceJSONObjectUniqueAggregateParameters(t *testing.T) {
	jsonObjectUniqueAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		jsonObjectUniqueAggregateHeld(t, p, b)
		for _, f := range jsonObjectUniqueAggregateFamilies {
			for _, args := range []string{"$1,1", "'a',$1"} {
				q := "SELECT " + f.name + "(" + args + ") AS v"
				jsonArrayAggregateError(t, p, b, q, fixture.ControlsReferenceDiagnostic("42P18", "could not determine data type of parameter $1"), true, "x")
				q = "/*é😀*/ SELECT " + f.name + "(" + args + ") AS v,$1::int AS n"
				d := fixture.ControlsReferenceDiagnostic("42P08", "could not determine data type of parameter $1")
				d.Position = jsonArrayAggregatePosition(q, "$1")
				jsonArrayAggregateError(t, p, b, q, d, true, 2)
			}
			want := `{ "2" : 2 }`
			if f.typ == "JSONB" {
				want = `{"2": 2}`
			}
			jsonArrayAggregateRoutes(t, p, b, fixture.Select("SELECT $1::int AS n,"+f.name+"($1,$1) AS v", []string{"n", "v"}, []string{"INT4", f.typ}, [][]any{{int64(2), want}}, 2))
			jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect("SELECT "+f.name+"('a',1) AS v", f.typ, f.singleton))
			jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect("SELECT "+f.name+"('a',NULL) AS v", f.typ, f.nullValue))
		}
	})
}
