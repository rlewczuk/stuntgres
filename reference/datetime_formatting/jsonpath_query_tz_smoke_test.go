package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONPathQueryTZFeatureSmoke(t *testing.T) {
	jsonpathQueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`SET TIME ZONE 'UTC'`, 0))
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec(`SET TIME ZONE 'Asia/Tokyo'`, 0))
		for _, f := range jsonpathQueryTZFunctions {
			q := `SELECT ` + f.name + `($1,$2,$3,$4) AS changed`
			fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathQueryResult(q, f.typ, jsonpathQueryTZExpected(f.name, true), jsonpathQueryTZArgs(f.name)...))
			fixture.RunStep(t, p.Ctx, peer, b.Recorder, jsonpathQueryResult(q, f.typ, jsonpathQueryTZExpected(f.name, false), jsonpathQueryTZArgs(f.name)...))
		}
		fixture.JSONPathNavigateBoth(t, p, b, c, fixture.Select(`SELECT * FROM jsonb_path_query_tz('["2023-08-15"]','$[*].timestamp_tz()') WITH ORDINALITY AS q(v,n)`, []string{"v", "n"}, []string{"JSONB", "INT8"}, [][]any{{`"2023-08-15T00:00:00+00:00"`, int64(1)}}))
		d := fixture.ControlsReferenceDiagnostic("0A000", "cannot convert value from date to timestamptz without time zone usage")
		d.Hint = "Use *_tz() function for time zone support."
		s := fixture.ErrorDiagnostic(`SELECT jsonb_path_match($1,$2,$3)`, d, jsonpathQueryTZDocument, jsonpathQueryTZBoolean, jsonpathQueryTZVars)
		s.SelectRows = true
		fixture.JSONPathNavigateBoth(t, p, b, c, s)
		q := `SELECT jsonb_path_query_tz($1,$2,$3,$4) AS changed`
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonpathNavigateError(q, "2203A", `JSON object does not contain key "x"`, `[{"x":true},{}]`, `strict $[*].x`, `{}`, false))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonpathQueryOne(q, "JSONB", `true`, `[{"x":true},{}]`, `strict $[*].x`, `{}`, true))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonpathQueryOne(q, "JSONB", jsonpathQueryTZDocument, jsonpathQueryTZArgs("jsonb_path_query_tz")...))
		check := jsonpathQueryTZStored(t, p, b, c)
		future := jsonpathQueryTZFuture()
		fixture.JSONPathNavigateReplay(t, p, b, c, check, future...)
		for _, s := range future {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
	})
}
