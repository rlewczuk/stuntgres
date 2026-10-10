package reference_test

import "testing"

func TestReferenceQueryTableCreateCommandEffects(t *testing.T) {
	for _, form := range []string{"ctas", "into"} {
		t.Run(form, func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, b := range p.backends() {
				t.Run(b.name, func(t *testing.T) {
					_, run := queryTableCreateSession(t, p, b)
					queryTableCreateSource(run)
					run(referenceExec("CREATE SEQUENCE qc_command_draw START 10", 0))
					q := "CREATE TABLE qc_command_effect AS SELECT nextval('qc_command_draw') AS n FROM qc_source"
					if form == "into" {
						q = "SELECT nextval('qc_command_draw') AS n INTO qc_command_effect FROM qc_source"
					}
					run(referencePrepared(referenceSelect(q, []string{}, []string{}, nil)))
					run(referenceSelect("SELECT n FROM qc_command_effect ORDER BY n", []string{"n"}, []string{"INT8"}, [][]any{{int64(10)}, {int64(11)}, {int64(12)}}))
					run(referenceSelect("SELECT nextval('qc_command_draw') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(13)}}))
				})
			}
		})
	}
}

func TestReferenceQueryTableCreateAtomicSequence(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			_, run := queryTableCreateSession(t, p, b)
			queryTableCreateSource(run)
			run(referenceExec("CREATE TABLE qc_late_source(i INT,divisor INT)", 0))
			run(referenceExec("INSERT INTO qc_late_source VALUES(1,1),(2,1),(3,0)", 3))
			run(referenceExec("CREATE SEQUENCE qc_late_draw START 10", 0))
			run(referenceImplicitError("CREATE TABLE qc_late_effect AS SELECT nextval('qc_late_draw') AS n,10/divisor AS v FROM (SELECT i,divisor FROM qc_late_source ORDER BY i OFFSET 0) d", referenceDiagnostic{Code: "22012", Message: "division by zero"}))
			queryTableCreateAbsent(t, run, b.pg, "qc_late_effect")
			// Sequence effects are deliberately not included in statement rollback.
			run(referenceSelect("SELECT nextval('qc_late_draw') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(13)}}))
			run(queryTableCreateBag("qc_source"))
			run(referenceSelect("SELECT i,divisor FROM qc_late_source ORDER BY i", []string{"i", "divisor"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(1)}, {int64(3), int64(0)}}))
		})
	}
}

func TestReferenceQueryTableCreateStatementTimeout(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			a, c := referenceLockConnections(t, p.ctx, b.db)
			_, observe := queryTableCreateSession(t, p, b)
			run := func(x referenceExecutor, s referenceStep) { referenceRunStep(t, p.ctx, x, b.recorder, s) }
			run(a, referenceExec("CREATE TABLE qc_timer_source(i INT)", 0))
			run(a, referenceExec("INSERT INTO qc_timer_source VALUES(7)", 1))
			tx, err := a.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			run(tx, referenceSelect("SELECT i FROM qc_timer_source FOR UPDATE", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}))
			tuple := queryTableCreateTuple(t, p.ctx, c, b.pg, "qc_timer_source")
			run(c, referenceExec("SET statement_timeout='100ms'", 0))
			queryTableCreateLockExec(t, p, b, c, "CREATE TABLE qc_timed_out AS SELECT i FROM qc_timer_source FOR UPDATE", "57014", "canceling statement due to statement timeout", "qc_timer_source", tuple)
			// Dispose the armed physical owner without another SQL command.
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
			queryTableCreateAbsent(t, observe, b.pg, "qc_timed_out")
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			observe(referenceSelect("SELECT i FROM qc_timer_source", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}))
		})
	}
}
