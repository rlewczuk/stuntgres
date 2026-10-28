package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONBObjectFeatureSmoke(t *testing.T) {
	jsonbObjectScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		check := jsonbObjectStored(t, p, b, c)
		for _, expr := range []string{`jsonb_object('{a,1,b,2}')`, `jsonb_object('{{a,1},{b,2}}')`, `jsonb_object('[-2:-1]={a,b}'::text[],'[7:8]={1,2}'::text[])`} {
			s := jsonbObjectSelect("SELECT "+expr+" AS v", `{"a": "1", "b": "2"}`)
			s.CheckMetadata = func(t *testing.T, r *sql.Rows) { fixture.JSONBObjectMetadata(t, r, b.PG, "v") }
			jsonObjectBoth(t, p, b, c, s)
		}
		jsonObjectBoth(t, p, b, c, jsonbObjectSelect("SELECT jsonb_object(ARRAY['a','first','a',NULL]) AS v", `{"a": null}`))
		jsonObjectBoth(t, p, b, c, jsonbObjectSelect("SELECT jsonb_object(ARRAY['a','true','b','null']) AS v", `{"a": "true", "b": "null"}`))
		jsonObjectBoth(t, p, b, c, fixture.Select(
			`SELECT jsonb_object(ARRAY['b','2','a','old','a','1']) = '{"a":"1","b":"2"}'::jsonb AS equal`,
			[]string{"equal"}, []string{"BOOL"}, [][]any{{true}}))
		jsonObjectBoth(t, p, b, c, fixture.Select(
			"SELECT jsonb_object(a) AS doc,count(*) AS n FROM jbo_stored GROUP BY jsonb_object(a)",
			[]string{"doc", "n"}, []string{"JSONB", "INT8"}, [][]any{{`{"a": "1"}`, int64(2)}}))
		for _, q := range []string{"SELECT jsonb_object($1) AS v", "SELECT jsonb_object($1,$2) AS v"} {
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			args := []any{"{a,NULL,a,2}"}
			null := []any{nil}
			bad := []any{"{odd}"}
			empty := []any{"{}"}
			codec := []any{"{a,,b}"}
			msg := "array must have even number of elements"
			if q == "SELECT jsonb_object($1,$2) AS v" {
				args = []any{"{a,a}", "{NULL,2}"}
				null = []any{nil, "{x}"}
				bad = []any{"{a,b}", "{x}"}
				empty = []any{"{}", "{}"}
				codec = []any{"{a}", "{a,,b}"}
				msg = "mismatched array dimensions"
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonbObjectSelect(q, `{"a": "2"}`, args...))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonbObjectSelect(q, "{}", empty...))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonbObjectSelect(q, nil, null...))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonObjectError(q, "2202E", msg, "execute", bad...))
			d := fixture.ControlsReferenceDiagnostic("22P02", `malformed array literal: "{a,,b}"`)
			d.Detail = `Unexpected "," character.`
			d.Context = "unnamed portal parameter $1 = '...'"
			if len(codec) == 2 {
				d.Context = "unnamed portal parameter $2 = '...'"
			}
			s := fixture.ErrorDiagnostic(q, d, codec...)
			s.SelectRows = true
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonbObjectSelect(q, `{"a": "2"}`, args...))
			stmt.Close()
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("UPDATE jbo_stored SET a='{b,2}',k='{b}',v='{2}' WHERE id=1", 1))
		check.Rows = [][]any{{int64(1), `{"b": "2"}`, `{"b": "2"}`}, {int64(2), `{"a": "1"}`, `{"a": "1"}`}}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, check)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic("INSERT INTO jbo_stored(id,a) VALUES(3,'{ok,3}'),(4,'{odd}')", fixture.ControlsReferenceDiagnostic("2202E", "array must have even number of elements")))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, check)
		jsonbObjectControls(t, p, b, c, check)
	})
}
