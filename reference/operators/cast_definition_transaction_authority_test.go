package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCastDefinitionTransactionAuthority(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"cast_tx_actor"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
			defer c.ExecContext(p.Ctx, "RESET ROLE")
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			for _, q := range []string{"CREATE ROLE cast_tx_actor", "GRANT USAGE ON SCHEMA public TO cast_tx_actor", castDefinitionSource, castDefinitionScore, "ALTER TYPE cast_src OWNER TO cast_tx_actor", "REVOKE ALL ON FUNCTION cast_score(cast_src) FROM PUBLIC"} {
				run(fixture.Exec(q, 0))
			}
			for _, q := range []string{castDefinitionEdge, "DROP CAST (cast_src AS integer)"} {
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				for _, held := range []bool{false, true} {
					run(fixture.Exec("BEGIN READ ONLY", 0))
					command := "CREATE"
					if q[0] == 'D' {
						command = "DROP"
					}
					s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("25006", "cannot execute "+command+" CAST in a read-only transaction"))
					s.ErrorStage = "execute"
					if held {
						fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
					} else {
						run(s)
					}
					run(fixture.ErrorDiagnostic("SELECT 1", fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
					run(fixture.Exec("ROLLBACK", 0))
				}
			}
			run(fixture.Exec("SET ROLE cast_tx_actor", 0))
			create, err := c.PrepareContext(p.Ctx, castDefinitionEdge)
			if err != nil {
				t.Fatal(err)
			}
			defer create.Close()
			for _, commit := range []bool{false, true} {
				run(fixture.Exec("BEGIN", 0))
				// Endpoint ownership authorizes declaration even without EXECUTE.
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: create, SQL: castDefinitionEdge}, b.Recorder, fixture.Exec(castDefinitionEdge, 0))
				run(fixture.Exec("SAVEPOINT denied", 0))
				q := "SELECT ($1::cast_src)::integer AS v"
				use, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				denied := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for function cast_score"), "red")
				denied.SelectRows, denied.ErrorStage = true, "execute"
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: use, SQL: q}, b.Recorder, denied)
				run(fixture.Exec("ROLLBACK TO denied", 0))
				run(fixture.Exec("RESET ROLE", 0))
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION cast_score(cast_src) TO cast_tx_actor", 0))
				run(fixture.Exec("SET ROLE cast_tx_actor", 0))
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: use, SQL: q}, b.Recorder, fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(41)}}, "red"))
				run(fixture.Exec("RESET ROLE", 0))
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION cast_score(cast_src) FROM cast_tx_actor", 0))
				run(fixture.Exec("SET ROLE cast_tx_actor", 0))
				run(fixture.Exec("SAVEPOINT revoked", 0))
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: use, SQL: q}, b.Recorder, denied)
				use.Close()
				run(fixture.Exec("ROLLBACK TO revoked", 0))
				run(fixture.Exec("DROP CAST (cast_src AS integer)", 0))
				end := "ROLLBACK"
				if commit {
					end = "COMMIT"
				}
				run(fixture.Exec(end, 0))
				run(fixture.Select("SELECT count(*) AS n FROM pg_cast WHERE castsource='cast_src'::regtype", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
			}
			run(fixture.Exec("RESET ROLE", 0))
		})
	}
}
