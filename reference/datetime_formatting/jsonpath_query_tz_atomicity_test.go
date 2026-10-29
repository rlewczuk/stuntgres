package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONPathQueryTZAtomicity(t *testing.T) {
	jsonpathQueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec(`SET TIME ZONE 'UTC'`, 0))
		run(fixture.Exec(`CREATE TABLE jpqtz_input(id int,doc jsonb)`, 0))
		run(fixture.Exec(`INSERT INTO jpqtz_input VALUES(1,'[{"x":"2023-08-15"}]'),(2,'[{"x":"2023-08-16"},{}]')`, 2))
		run(fixture.Exec(`CREATE TABLE jpqtz_out(v jsonb)`, 0))
		run(fixture.Exec(`INSERT INTO jpqtz_out VALUES('99')`, 1))
		path := `strict $[*].x.timestamp_tz()`
		for _, q := range []string{
			`INSERT INTO jpqtz_out SELECT jsonb_path_query_tz(doc,'` + path + `') FROM jpqtz_input ORDER BY id`,
			`INSERT INTO jpqtz_out SELECT q.v FROM jpqtz_input,jsonb_path_query_tz(doc,'` + path + `') AS q(v) ORDER BY id`,
			`CREATE TABLE jpqtz_failed AS SELECT jsonb_path_query_tz(doc,'` + path + `') AS v FROM jpqtz_input ORDER BY id`,
			`UPDATE jpqtz_input SET doc=jsonb_path_query_array_tz(doc,'` + path + `')`,
		} {
			s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("2203A", `JSON object does not contain key "x"`))
			run(s)
			run(fixture.Prepared(s))
			run(fixture.Select(`SELECT v FROM jpqtz_out`, []string{"v"}, []string{"JSONB"}, jsonpathQueryRows(`99`)))
			run(fixture.Select(`SELECT doc FROM jpqtz_input ORDER BY id`, []string{"doc"}, []string{"JSONB"}, jsonpathQueryRows(`[{"x": "2023-08-15"}]`, `[{"x": "2023-08-16"}, {}]`)))
		}
		run(fixture.Select(`SELECT count(*) AS absent FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relname='jpqtz_failed'`, []string{"absent"}, []string{"INT8"}, [][]any{{int64(0)}}))
		for _, suffix := range []string{"", " LIMIT 1"} {
			q := `SELECT jsonb_path_query_tz($1,$2) AS changed` + suffix
			s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("2203A", `JSON object does not contain key "x"`), `[{"x":"2023-08-15"},{}]`, path)
			s.SelectRows = true
			fixture.JSONPathNavigateBoth(t, p, b, c, s)
		}
		run(fixture.Exec(`INSERT INTO jpqtz_out SELECT jsonb_path_query_tz(doc,'`+path+`','{}',true) FROM jpqtz_input ORDER BY id`, 2))
		run(fixture.Select(`SELECT v FROM jpqtz_out ORDER BY v`, []string{"v"}, []string{"JSONB"}, jsonpathQueryRows(`"2023-08-15T00:00:00+00:00"`, `"2023-08-16T00:00:00+00:00"`, `99`)))
	})
}
