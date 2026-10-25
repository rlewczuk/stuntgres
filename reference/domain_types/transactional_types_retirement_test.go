package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalTypesHeldRetirement(t *testing.T) {
	for _, annotation := range []string{"tt_d[]", "_tt_d"} {
		t.Run(annotation, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, _ := fixture.LockConnections(t, p.Ctx, b.DB)
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					run(fixture.Exec("CREATE DOMAIN tt_d AS integer", 0))
					array := domainAlterOID(t, p, c, "SELECT typarray FROM pg_type WHERE oid='tt_d'::regtype")
					q := "SELECT ($1::" + annotation + ")::text AS v"
					stmt, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
					check := fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{"{7,NULL}"}}, "{7,NULL}")
					fixture.RunStep(t, p.Ctx, held, b.Recorder, check)
					run(fixture.Exec("BEGIN", 0))
					defer c.ExecContext(p.Ctx, "ROLLBACK")
					run(fixture.Exec("DROP DOMAIN tt_d", 0))
					run(fixture.Exec("CREATE DOMAIN tt_d AS integer", 0))
					fresh := domainAlterOID(t, p, c, "SELECT typarray FROM pg_type WHERE oid='tt_d'::regtype")
					if fresh == array {
						t.Fatal("replacement adopted retired companion identity")
					}
					run(fixture.Exec("SAVEPOINT replacement", 0))
					d := fixture.ControlsReferenceDiagnostic("0A000", "cached plan must not change parameter type")
					if b.PG {
						d = fixture.ControlsReferenceDiagnostic("XX000", fmt.Sprintf("cache lookup failed for type %d", array))
						d.Context = "unnamed portal parameter $1"
					}
					s := fixture.ErrorDiagnostic(q, d, "{7,NULL}")
					s.SelectRows = true
					s.ErrorStage = "execute"
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
					run(fixture.Exec("ROLLBACK TO replacement", 0))
					run(fixture.Prepared(check))
					run(fixture.Exec("ROLLBACK", 0))
					domainIdentitySameOID(t, p, c, "SELECT typarray FROM pg_type WHERE oid='tt_d'::regtype", array)
				})
			}
		})
	}
}
