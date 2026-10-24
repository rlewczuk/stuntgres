package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTransactionalIndexDiagnostics(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "held"}[held], func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, _ := fixture.LockConnections(t, p.Ctx, b.DB)
					run := transactionalIndexRun(t, p, b, c)
					run(fixture.Exec("CREATE TABLE t(n INT)", 0))
					run(fixture.Exec("CREATE INDEX idx ON t(n)", 0))
					for _, tc := range []struct{ begin, q, code, message string }{
						{"BEGIN", "CREATE INDEX retry ON missing(n)", "42P01", `relation "missing" does not exist`},
						{"BEGIN", "CREATE INDEX idx ON t(n)", "42P07", `relation "idx" already exists`},
						{"BEGIN READ ONLY", "CREATE INDEX retry ON missing(n)", "25006", "cannot execute CREATE INDEX in a read-only transaction"},
						{"BEGIN", "CREATE INDEX CONCURRENTLY retry ON t(n)", "25001", "CREATE INDEX CONCURRENTLY cannot run inside a transaction block"},
					} {
						var e fixture.Executor = c
						if held {
							stmt, err := c.PrepareContext(p.Ctx, tc.q)
							if err != nil {
								t.Fatal(err)
							}
							defer stmt.Close()
							e = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: tc.q}
						}
						run(fixture.Exec(tc.begin, 0))
						s := fixture.ErrorDiagnostic(tc.q, fixture.ControlsReferenceDiagnostic(tc.code, tc.message))
						s.ErrorStage = "execute"
						fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
						run(fixture.ErrorDiagnostic("SELECT 1", transactionalIndexAborted()))
						run(fixture.Exec("ROLLBACK", 0))
						run(transactionalIndexName("retry", false))
						run(transactionalIndexName("idx", true))
					}
				})
			}
		})
	}
}
