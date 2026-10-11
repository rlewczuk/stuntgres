package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryTableCreateCommandEffects(t *testing.T) {
	for _, form := range []string{"ctas", "into"} {
		t.Run(form, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					_, run := fixture.QueryTableCreateSession(t, p, b)
					fixture.QueryTableCreateSource(run)
					run(fixture.Exec("CREATE SEQUENCE qc_command_draw START 10", 0))
					q := "CREATE TABLE qc_command_effect AS SELECT nextval('qc_command_draw') AS n FROM qc_source"
					if form == "into" {
						q = "SELECT nextval('qc_command_draw') AS n INTO qc_command_effect FROM qc_source"
					}
					run(fixture.Prepared(fixture.Select(q, []string{}, []string{}, nil)))
					run(fixture.Select("SELECT n FROM qc_command_effect ORDER BY n", []string{"n"}, []string{"INT8"}, [][]any{{int64(10)}, {int64(11)}, {int64(12)}}))
					run(fixture.Select("SELECT nextval('qc_command_draw') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(13)}}))
				})
			}
		})
	}
}

func TestReferenceQueryTableCreateAtomicSequence(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			_, run := fixture.QueryTableCreateSession(t, p, b)
			fixture.QueryTableCreateSource(run)
			run(fixture.Exec("CREATE TABLE qc_late_source(i INT,divisor INT)", 0))
			run(fixture.Exec("INSERT INTO qc_late_source VALUES(1,1),(2,1),(3,0)", 3))
			run(fixture.Exec("CREATE SEQUENCE qc_late_draw START 10", 0))
			run(fixture.ImplicitError("CREATE TABLE qc_late_effect AS SELECT nextval('qc_late_draw') AS n,10/divisor AS v FROM (SELECT i,divisor FROM qc_late_source ORDER BY i OFFSET 0) d", fixture.Diagnostic{Code: "22012", Message: "division by zero"}))
			fixture.QueryTableCreateAbsent(t, run, b.PG, "qc_late_effect")
			// Sequence effects are deliberately not included in statement rollback.
			run(fixture.Select("SELECT nextval('qc_late_draw') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(13)}}))
			run(fixture.QueryTableCreateBag("qc_source"))
			run(fixture.Select("SELECT i,divisor FROM qc_late_source ORDER BY i", []string{"i", "divisor"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(1)}, {int64(3), int64(0)}}))
		})
	}
}

func TestReferenceQueryTableCreateStatementTimeout(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			a, c := fixture.LockConnections(t, p.Ctx, b.DB)
			_, observe := fixture.QueryTableCreateSession(t, p, b)
			run := func(x fixture.Executor, s fixture.Step) { fixture.RunStep(t, p.Ctx, x, b.Recorder, s) }
			run(a, fixture.Exec("CREATE TABLE qc_timer_source(i INT)", 0))
			run(a, fixture.Exec("INSERT INTO qc_timer_source VALUES(7)", 1))
			tx, err := a.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			run(tx, fixture.Select("SELECT i FROM qc_timer_source FOR UPDATE", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}))
			tuple := fixture.QueryTableCreateTuple(t, p.Ctx, c, b.PG, "qc_timer_source")
			run(c, fixture.Exec("SET statement_timeout='100ms'", 0))
			fixture.QueryTableCreateLockExec(t, p, b, c, "CREATE TABLE qc_timed_out AS SELECT i FROM qc_timer_source FOR UPDATE", "57014", "canceling statement due to statement timeout", "qc_timer_source", tuple)
			// Dispose the armed physical owner without another SQL command.
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
			fixture.QueryTableCreateAbsent(t, observe, b.PG, "qc_timed_out")
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			observe(fixture.Select("SELECT i FROM qc_timer_source", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}))
		})
	}
}
