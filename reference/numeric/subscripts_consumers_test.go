package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func subscriptsMalformed() fixture.Diagnostic {
	d := fixture.ControlsReferenceDiagnostic("22P02", `malformed array literal: "{1,,2}"`)
	d.Detail = `Unexpected "," character.`
	return d
}

func subscriptsAtomicity(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE TABLE subscripts_sink(s integer)", 0),
		fixture.Exec("INSERT INTO subscripts_sink VALUES(99)", 1),
		fixture.Exec("CREATE TABLE subscripts_bad_source(id integer,a text)", 0),
		fixture.Exec("INSERT INTO subscripts_bad_source VALUES(1,'{1,2}'),(2,'{1,,2}')", 2),
	} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
	for _, q := range []string{
		"INSERT INTO subscripts_sink SELECT generate_subscripts(a::integer[],1) FROM (SELECT a FROM subscripts_bad_source ORDER BY id) v",
		"CREATE TABLE subscripts_failed AS SELECT generate_subscripts(a::integer[],1) AS s FROM (SELECT a FROM subscripts_bad_source ORDER BY id) v",
	} {
		t.Run(q, func(t *testing.T) {
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(q, subscriptsMalformed())
				s.Prepared, s.ErrorStage = prepared, "execute"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT s FROM subscripts_sink", []string{"s"}, []string{"INT4"}, seriesRows(99)))
				missing := "SELECT * FROM subscripts_failed"
				d := seriesDiagnostic(missing, "subscripts_failed", "42P01", `relation "subscripts_failed" does not exist`, "")
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic(missing, d))
			}
		})
	}
}

func TestReferenceSubscriptsConsumers(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"subscripts_reader"})
	subscriptsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE subscripts_source(id integer,a integer[],d integer,r boolean)", 0),
			fixture.Exec("INSERT INTO subscripts_source VALUES(1,"+subscriptsMatrix+",1,false),(2,"+subscriptsMatrix+",2,true)", 2),
			fixture.Exec("CREATE VIEW subscripts_view AS SELECT generate_subscripts(a,d,r) AS s FROM subscripts_source", 0),
			fixture.Exec("CREATE TABLE subscripts_data AS SELECT s FROM subscripts_view", 5),
			fixture.Exec("CREATE TABLE subscripts_inserted(s integer)", 0),
			fixture.Exec("INSERT INTO subscripts_inserted SELECT s FROM subscripts_view", 5),
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		for _, table := range []string{"subscripts_view", "subscripts_data", "subscripts_inserted"} {
			seriesBoth(t, p, b, c, fixture.Select("SELECT s FROM "+table+" ORDER BY s", []string{"s"}, []string{"INT4"}, seriesRows(-1, 0, 3, 4, 5)))
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO subscripts_source SELECT id,a,d,r FROM subscripts_source", 2))
		seriesBoth(t, p, b, c, fixture.Select("SELECT s FROM subscripts_view ORDER BY s", []string{"s"}, []string{"INT4"}, seriesRows(-1, -1, 0, 0, 3, 3, 4, 4, 5, 5)))
		// All three retained arguments own stored dependencies.
		for _, column := range []string{"a", "d", "r"} {
			q := "ALTER TABLE subscripts_source DROP COLUMN " + column
			d := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop column "+column+" of table subscripts_source because other objects depend on it")
			d.Detail = "view subscripts_view depends on column " + column + " of table subscripts_source"
			d.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic(q, d))
		}
		subscriptsAtomicity(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE USER subscripts_reader", 0))
		if b.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO subscripts_reader", 0))
		}
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		const q = "SELECT generate_subscripts(a,d,r) AS s FROM subscripts_source ORDER BY s"
		st, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
		for _, column := range []string{"a", "d", "r"} {
			run(fixture.Exec("SET SESSION AUTHORIZATION subscripts_reader", 0))
			// Even a row-eliminating predicate cannot erase authorization.
			for _, query := range []string{q, "SELECT generate_subscripts(a,d,r) AS s FROM subscripts_source WHERE false", "SELECT generate_subscripts(NULL::integer[],d,r) AS s FROM subscripts_source"} {
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic(query, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table subscripts_source"))
					s.Prepared, s.SelectRows, s.ErrorStage = prepared, true, "execute"
					run(s)
				}
			}
			s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table subscripts_source"))
			s.SelectRows, s.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("GRANT SELECT("+column+") ON subscripts_source TO subscripts_reader", 0))
		}
		run(fixture.Exec("SET SESSION AUTHORIZATION subscripts_reader", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"s"}, []string{"INT4"}, seriesRows(-1, -1, 0, 0, 3, 3, 4, 4, 5, 5)))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	})
}
