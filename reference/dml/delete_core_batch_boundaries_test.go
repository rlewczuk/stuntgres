package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDeleteCoreDiagnosticBoundaries(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(id INT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1),(2)", 2))
			for _, prepared := range []bool{false, true} {
				want := fixture.Diagnostic{Code: "42P18", Message: "could not determine data type of parameter $1"}
				stage := "execute"
				if prepared {
					stage = "prepare"
				}
				if b.PG && !prepared {
					want = fixture.Diagnostic{Code: "42P02", Message: "there is no parameter $2", Position: "24"}
				}
				s := fixture.ImplicitError("DELETE FROM t WHERE id=$2", want)
				s.Prepared, s.SelectRows, s.ErrorStage = prepared, true, stage
				run(s)
				fixture.DeleteReferenceRows(t, p, b.DB, b.Recorder, 1, 2)
			}
			q := "DELETE FROM t WHERE id=1; DELETE FROM t WHERE id=2"
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42601", Message: "cannot insert multiple commands into a prepared statement"})
			s.Prepared, s.ErrorStage = true, "prepare"
			run(s)
			fixture.DeleteReferenceRows(t, p, b.DB, b.Recorder, 1, 2)
			run(fixture.Exec(q, 1))
			fixture.DeleteReferenceRows(t, p, b.DB, b.Recorder)
		})
	}
}
