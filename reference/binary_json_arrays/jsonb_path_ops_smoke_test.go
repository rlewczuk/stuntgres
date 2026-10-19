package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONBPathOpsFeatureSmoke(t *testing.T) {
	jsonbPathOpsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for i, op := range jsonbPathOps {
			want := i == 0
			jsonpathNavigateBoth(t, p, b, c, jsonbPathOpsOne(jsonbPathOpsSQL(op), want, `false`, `$`))
			var missing any
			if i == 0 {
				missing = false
			}
			jsonpathNavigateBoth(t, p, b, c, jsonbPathOpsOne(jsonbPathOpsSQL(op), missing, `{}`, `$.missing`))
			jsonpathNavigateBoth(t, p, b, c, jsonbPathOpsOne(jsonbPathOpsSQL(op), nil, `{}`, `strict $.missing`))
			q := jsonbPathOpsSQL(op)
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				var prefix any
				if i == 1 {
					prefix = true
				}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonbPathOpsOne(q, prefix, `[{"x":true},{}]`, `strict $[*].x`))
				d := fixture.ControlsReferenceDiagnostic("42601", `syntax error at end of jsonpath input`)
				d.Context = "unnamed portal parameter $2 = '...'"
				s := fixture.ErrorDiagnostic(q, d, `{}`, `$[`)
				s.SelectRows = true
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonbPathOpsOne(q, want, `false`, `$`))
			}()
		}
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec(`CREATE TABLE jpo_smoke(doc jsonb,path jsonpath)`, 0))
		run(fixture.Exec(`INSERT INTO jpo_smoke VALUES('false','$')`, 1))
		run(fixture.Exec(`CREATE VIEW jpo_smoke_view AS SELECT doc @? path AS e,doc @@ path AS m FROM jpo_smoke`, 0))
		check := fixture.Select(`SELECT e,m FROM jpo_smoke_view`, []string{"e", "m"}, []string{"BOOL", "BOOL"}, [][]any{{true, false}})
		run(check)
		future := []fixture.Step{fixture.Exec(`UPDATE jpo_smoke SET doc='true'`, 1), fixture.Select(`SELECT e,m FROM jpo_smoke_view`, []string{"e", "m"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}})}
		jsonpathNavigateReplay(t, p, b, c, check, future...)
		for _, s := range future {
			run(s)
		}
	})
}
