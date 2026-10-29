package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceDeparsePartitionAdmission(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"dp_inquiry_reader"})
	fixture.DeparseRelationsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE ROLE dp_inquiry_reader", 0))
		for _, name := range []string{"pg_get_partkeydef", "pg_get_partition_constraintdef"} {
			t.Run(name, func(t *testing.T) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				// Prepare while EXECUTE is admitted, then revoke before execution.
				q := "SELECT " + name + "($1) AS definition"
				run(fixture.Exec("SET ROLE dp_inquiry_reader", 0))
				held, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				run(fixture.Exec("RESET ROLE", 0))
				signature := "pg_catalog." + name + "(oid)"
				defer func() {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					if _, err := c.ExecContext(ctx, "RESET ROLE"); err != nil {
						t.Error(err)
					}
					if _, err := c.ExecContext(ctx, "GRANT EXECUTE ON FUNCTION "+signature+" TO PUBLIC"); err != nil {
						t.Error(err)
					}
					if _, err := c.ExecContext(ctx, "REVOKE EXECUTE ON FUNCTION "+signature+" FROM dp_inquiry_reader"); err != nil {
						t.Error(err)
					}
				}()
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+signature+" FROM PUBLIC", 0))
				run(fixture.Exec("SET ROLE dp_inquiry_reader", 0))
				w := fixture.ControlsReferenceDiagnostic("42501", "permission denied for function "+name)
				// PostgreSQL's strict NULL plan eliminates the call despite the
				// revoke. A non-NULL OID still demands live routine permission.
				null := fixture.DeparseRelationsTextStep(t, b, q, nil, nil)
				executor := fixture.HeldStatement{Executor: c, Statement: held, SQL: q}
				fixture.RunStep(t, p.Ctx, executor, b.Recorder, null)
				run(null)
				s := fixture.ErrorDiagnostic(q, w, int64(0))
				s.SelectRows = true
				run(s)
				fixture.RunStep(t, p.Ctx, executor, b.Recorder, s)
				run(fixture.Exec("RESET ROLE", 0))
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+signature+" TO dp_inquiry_reader", 0))
				run(fixture.Exec("SET ROLE dp_inquiry_reader", 0))
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: q}, b.Recorder, fixture.DeparseRelationsTextStep(t, b, q, nil, nil))
				run(fixture.Exec("RESET ROLE", 0))
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+signature+" FROM dp_inquiry_reader", 0))
			})
		}
	})
}
