package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDeleteCoreQualifiedCastBoundaries(t *testing.T) {
	for _, query := range []string{
		"DELETE FROM t AS d WHERE d.v::int4=10",
		"DELETE FROM t AS d WHERE CAST(d.v AS int4)=10",
		"DELETE FROM t AS d WHERE int4(d.v)=10",
	} {
		for _, prepared := range []bool{false, true} {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name+query, func(t *testing.T) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
					run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
					run(fixture.Exec("INSERT INTO t VALUES(1,10),(2,20),(4,10),(4,10)", 4))
					s := fixture.Exec(query, 3)
					s.Prepared = prepared
					run(s)
					fixture.DeleteReferenceRows(t, p, backend.DB, backend.Recorder, 2)
				})
			}
		}
	}
}

func TestReferenceDeleteCoreTrailingDiagnostic(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			fixture.LockExec(t, p.Ctx, backend.DB, "CREATE TABLE t(id INT,v INT)", 0)
			fixture.LockExec(t, p.Ctx, backend.DB, "INSERT INTO t VALUES(1,10),(2,20)", 2)
			q := "DELETE FROM t AS d WHERE d.v IS NULL; nonsense"
			for _, prepared := range []bool{false, true} {
				if backend.PG {
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "nonsense"`, Position: "39"})
					s.Prepared = prepared
					s.ErrorStage = "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					fixture.DeleteReferenceExecuteStep(t, p, backend.DB, backend.Recorder, s)
				} else if prepared {
					held, err := backend.DB.PrepareContext(p.Ctx, q)
					if held != nil {
						held.Close()
						t.Fatal("excluded input prepared")
					}
					if err == nil {
						t.Fatal("excluded input accepted")
					}
				} else {
					result, err := backend.DB.ExecContext(p.Ctx, q)
					if result != nil || err == nil {
						t.Fatal(result, err)
					}
				}
				fixture.DeleteReferenceRows(t, p, backend.DB, backend.Recorder, 1, 2)
			}
		})
	}
}
