package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAutoExplainFeatureSmoke(t *testing.T) {
	autoExplainScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE ae_smoke(id integer,v text)", 0))
		run(fixture.Exec("INSERT INTO ae_smoke VALUES(1,'one'),(2,NULL),(3,'three')", 3))
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		autoExplainConfigure(t, p, b, c, notices)
		const query = "SELECT id,v FROM ae_smoke WHERE id=$1 ORDER BY id"
		held, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		autoExplainQuiet(t, notices)
		for _, id := range []int64{2, 99} {
			var values [][]any
			if id == 2 {
				values = [][]any{{int64(2), nil}}
			}
			step := fixture.Select(query, []string{"id", "v"}, []string{"INT4", "TEXT"}, values, id)
			step.CheckMetadata = func(t *testing.T, rows *sql.Rows) { autoExplainTableMetadata(t, b, rows) }
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: query}, b.Recorder, step)
			parameter := "$1 = '99'"
			if id == 2 {
				parameter = "$1 = '2'"
			}
			autoExplainJSON(t, notices, query, &parameter, true, float64(len(values)))
		}
		run(fixture.Exec("SET auto_explain.log_parameter_max_length = 2", 0))
		const scalar = "SELECT $1::text AS v"
		text, err := c.PrepareContext(p.Ctx, scalar)
		if err != nil {
			t.Fatal(err)
		}
		defer text.Close()
		for _, tc := range []struct {
			input  any
			params string
		}{{"éx", "$1 = 'é...'"}, {nil, "$1 = NULL"}} {
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: text, SQL: scalar}, b.Recorder, fixture.Select(scalar, []string{"v"}, []string{"TEXT"}, [][]any{{tc.input}}, tc.input))
			autoExplainJSON(t, notices, scalar, &tc.params, true, 1)
		}
		run(fixture.Exec("SET auto_explain.log_parameter_max_length = 0", 0))
		run(fixture.Select(scalar, []string{"v"}, []string{"TEXT"}, [][]any{{"éx"}}, "éx"))
		autoExplainJSON(t, notices, scalar, nil, true, 1)
		run(fixture.Exec("SET auto_explain.log_buffers = on", 0))
		run(fixture.Exec("SET auto_explain.log_wal = on", 0))
		const insert = "INSERT INTO ae_smoke VALUES(4,'four')"
		run(fixture.Exec(insert, 1))
		document := autoExplainJSON(t, notices, insert, nil, true, 0)
		autoExplainMetric(t, b, document, 0, 1)
		const contents = "SELECT id,v FROM ae_smoke ORDER BY id"
		step := fixture.Select(contents, []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(2), nil}, {int64(3), "three"}, {int64(4), "four"}})
		run(fixture.Exec("SET auto_explain.sample_rate = 0", 0))
		run(step)
		autoExplainQuiet(t, notices)
		run(fixture.Exec("SET auto_explain.sample_rate = 1", 0))
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, step)
		autoExplainQuiet(t, notices)
		failure := fixture.ErrorDiagnostic("SELECT 10/(id-id) FROM ae_smoke", fixture.ControlsReferenceDiagnostic("22012", "division by zero"))
		failure.SelectRows = true
		failure.ErrorStage = "execute"
		run(failure)
		autoExplainQuiet(t, notices)
		run(step)
		autoExplainJSON(t, notices, contents, nil, true, 4)
		if err := held.Close(); err != nil {
			t.Fatal(err)
		}
		if err := text.Close(); err != nil {
			t.Fatal(err)
		}
		autoExplainQuiet(t, notices)
	})
}
