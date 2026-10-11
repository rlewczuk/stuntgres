package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

// Keep the applicable USING(TRUE) PostgreSQL companion: BASE intentionally
// does not evaluate policy expressions. TestReferenceRLSBaseLastPolicyDrop
// separately certifies shared enabled/forced state and post-drop default deny.
func TestReferenceRLSPolicyDefaultDenyPostgres(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"policy_deny_reader"})
			p := fixture.NewScenario(t)
			if p.Postgres == nil {
				t.Skip("requires real PostgreSQL enabled row security")
			}
			for _, backend := range p.Backends() {
				if !backend.PG {
					continue
				}
				t.Run(backend.Name, func(t *testing.T) {
					c, err := backend.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
					run := func(step fixture.Step) {
						step.Prepared = prepared
						fixture.RunStep(t, p.Ctx, c, backend.Recorder, step)
					}
					run(fixture.Exec("CREATE ROLE policy_deny_reader LOGIN NOSUPERUSER NOBYPASSRLS", 0))
					run(fixture.Exec("CREATE TABLE policy_deny(id INTEGER)", 0))
					run(fixture.Exec("INSERT INTO policy_deny VALUES(7)", 1))
					run(fixture.Exec("GRANT USAGE ON SCHEMA public TO policy_deny_reader", 0))
					run(fixture.Exec("GRANT SELECT ON policy_deny TO policy_deny_reader", 0))
					run(fixture.Exec("ALTER TABLE policy_deny ENABLE ROW LEVEL SECURITY", 0))
					run(fixture.Exec("CREATE POLICY p ON policy_deny TO policy_deny_reader USING(TRUE)", 0))
					run(fixture.Exec("SET SESSION AUTHORIZATION policy_deny_reader", 0))
					run(fixture.Select("SELECT id FROM policy_deny", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
					run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
					run(fixture.Exec("DROP POLICY p ON policy_deny", 0))
					run(fixture.Select("SELECT rowsecurity FROM pg_tables WHERE schemaname='public' AND tablename='policy_deny'", []string{"rowsecurity"}, []string{"BOOL"}, [][]any{{true}}))
					run(fixture.Exec("SET SESSION AUTHORIZATION policy_deny_reader", 0))
					run(fixture.Select("SELECT id FROM policy_deny", []string{"id"}, []string{"INT4"}, nil))
					run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
					run(fixture.Select("SELECT id FROM policy_deny", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
				})
			}
		})
	}
}
