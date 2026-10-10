package reference_test

import "testing"

func TestReferenceJSONBIOCasts(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
		source := ` {"z":1,"a":1,"a":2} `
		normalized := `{"a": 2, "z": 1}`
		for _, prepared := range []bool{false, true} {
			for _, expr := range []string{"CAST($1::json AS jsonb)", "$1::json::jsonb", "CAST($1::text AS jsonb)", "$1::text::jsonb"} {
				s := referenceSelect("SELECT "+expr+" AS v", []string{"v"}, []string{"JSONB"}, [][]any{{normalized}}, source)
				if prepared {
					s = referencePrepared(s)
				}
				run(s)
			}
			s := referenceSelect("SELECT $1::json AS original,CAST($1::json AS jsonb)::json AS normalized,CAST($1::json AS jsonb)::text AS text", []string{"original", "normalized", "text"}, []string{"JSON", "JSON", "TEXT"}, [][]any{{source, normalized, normalized}}, source)
			if prepared {
				s = referencePrepared(s)
			}
			run(s)
			s = referenceSelect("SELECT NULL::json::jsonb AS b,NULL::jsonb::json AS j", []string{"b", "j"}, []string{"JSONB", "JSON"}, [][]any{{nil, nil}})
			if prepared {
				s = referencePrepared(s)
			}
			run(s)
		}
		run(referenceExec("CREATE TABLE jb_cast(b JSONB,j JSON)", 0))
		run(referenceExec("INSERT INTO jb_cast VALUES($1::json,$2::jsonb)", 1, source, source))
		run(referencePrepared(referenceExec("INSERT INTO jb_cast VALUES(NULL::json,NULL::jsonb)", 1)))
		s := referenceSelect("SELECT b,j FROM jb_cast", []string{"b", "j"}, []string{"JSONB", "JSON"}, [][]any{{normalized, normalized}, {nil, nil}})
		s.bag = true
		run(s)
		for _, tc := range jsonbReferenceInvalidCases() {
			if tc.name != "unicode_zero" && tc.name != "magnitude_overflow" {
				continue
			}
			t.Run(tc.name, func(t *testing.T) {
				want := tc.want
				s := referenceErrorDiagnostic("SELECT $1::json::jsonb", want, tc.input)
				s.selectRows = true
				s.errorStage = "execute"
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
			})
		}
		for _, tc := range []struct {
			q        string
			position string
		}{{"CREATE TABLE jb_modifier(v jsonb(2))", "28"}, {"SELECT 'null'::jsonb(2)", "16"}} {
			t.Run(tc.q, func(t *testing.T) {
				want := controlsReferenceDiagnostic("42601", `type modifier is not allowed for type "jsonb"`)
				want.Position = tc.position
				s := referenceErrorDiagnostic(tc.q, want)
				s.selectRows = tc.q[:6] == "SELECT"
				s.errorStage = "execute"
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
			})
		}
		for _, typ := range []string{"integer", "boolean"} {
			t.Run(typ, func(t *testing.T) {
				expr := "1"
				if typ == "boolean" {
					expr = "TRUE"
				}
				q := "INSERT INTO jb_cast(b) VALUES(" + expr + ")"
				want := controlsReferenceDiagnostic("42804", `column "b" is of type jsonb but expression is of type `+typ)
				want.Hint = "You will need to rewrite or cast the expression."
				want.Position = "31"
				s := referencePrepared(referenceErrorDiagnostic(q, want))
				s.errorStage = "prepare"
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
			})
		}
	}
}
