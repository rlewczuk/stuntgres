package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONBIOCasts(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
		source := ` {"z":1,"a":1,"a":2} `
		normalized := `{"a": 2, "z": 1}`
		for _, prepared := range []bool{false, true} {
			for _, expr := range []string{"CAST($1::json AS jsonb)", "$1::json::jsonb", "CAST($1::text AS jsonb)", "$1::text::jsonb"} {
				s := fixture.Select("SELECT "+expr+" AS v", []string{"v"}, []string{"JSONB"}, [][]any{{normalized}}, source)
				if prepared {
					s = fixture.Prepared(s)
				}
				run(s)
			}
			s := fixture.Select("SELECT $1::json AS original,CAST($1::json AS jsonb)::json AS normalized,CAST($1::json AS jsonb)::text AS text", []string{"original", "normalized", "text"}, []string{"JSON", "JSON", "TEXT"}, [][]any{{source, normalized, normalized}}, source)
			if prepared {
				s = fixture.Prepared(s)
			}
			run(s)
			s = fixture.Select("SELECT NULL::json::jsonb AS b,NULL::jsonb::json AS j", []string{"b", "j"}, []string{"JSONB", "JSON"}, [][]any{{nil, nil}})
			if prepared {
				s = fixture.Prepared(s)
			}
			run(s)
		}
		run(fixture.Exec("CREATE TABLE jb_cast(b JSONB,j JSON)", 0))
		run(fixture.Exec("INSERT INTO jb_cast VALUES($1::json,$2::jsonb)", 1, source, source))
		run(fixture.Prepared(fixture.Exec("INSERT INTO jb_cast VALUES(NULL::json,NULL::jsonb)", 1)))
		s := fixture.Select("SELECT b,j FROM jb_cast", []string{"b", "j"}, []string{"JSONB", "JSON"}, [][]any{{normalized, normalized}, {nil, nil}})
		s.Bag = true
		run(s)
		for _, tc := range jsonbReferenceInvalidCases() {
			if tc.name != "unicode_zero" && tc.name != "magnitude_overflow" {
				continue
			}
			t.Run(tc.name, func(t *testing.T) {
				want := tc.want
				s := fixture.ErrorDiagnostic("SELECT $1::json::jsonb", want, tc.input)
				s.SelectRows = true
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
			})
		}
		for _, tc := range []struct {
			q        string
			position string
		}{{"CREATE TABLE jb_modifier(v jsonb(2))", "28"}, {"SELECT 'null'::jsonb(2)", "16"}} {
			t.Run(tc.q, func(t *testing.T) {
				want := fixture.ControlsReferenceDiagnostic("42601", `type modifier is not allowed for type "jsonb"`)
				want.Position = tc.position
				s := fixture.ErrorDiagnostic(tc.q, want)
				s.SelectRows = tc.q[:6] == "SELECT"
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
			})
		}
		for _, typ := range []string{"integer", "boolean"} {
			t.Run(typ, func(t *testing.T) {
				expr := "1"
				if typ == "boolean" {
					expr = "TRUE"
				}
				q := "INSERT INTO jb_cast(b) VALUES(" + expr + ")"
				want := fixture.ControlsReferenceDiagnostic("42804", `column "b" is of type jsonb but expression is of type `+typ)
				want.Hint = "You will need to rewrite or cast the expression."
				want.Position = "31"
				s := fixture.Prepared(fixture.ErrorDiagnostic(q, want))
				s.ErrorStage = "prepare"
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
			})
		}
	}
}
