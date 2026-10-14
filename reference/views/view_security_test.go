package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceViewOriginalInvokerControls(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			owner, reader := fmt.Sprintf("view_owner_%v", prepared), fmt.Sprintf("view_reader_%v", prepared)
			fixture.RoleReferenceCleanup(t, []string{reader, owner})
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				c, err := backend.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				run := func(step fixture.Step) {
					step.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, backend.Recorder, step)
				}
				for _, query := range []string{"CREATE USER " + owner, "CREATE USER " + reader, "CREATE TABLE t(id INT)", "INSERT INTO t VALUES(7)"} {
					count := int64(0)
					if query == "INSERT INTO t VALUES(7)" {
						count = 1
					}
					run(fixture.Exec(query, count))
				}
				grant := func(query string) {
					if _, err := backend.DB.ExecContext(p.Ctx, query); err != nil {
						t.Fatal(err)
					}
				}
				if backend.PG {
					grant("GRANT USAGE ON SCHEMA public TO PUBLIC")
				}
				run(fixture.Exec("SET SESSION AUTHORIZATION "+owner, 0))
				fixture.WithPublicSchemaOwner(t, p, backend, owner, func() {
					run(fixture.Exec("CREATE VIEW inner_view WITH(security_invoker=true) AS SELECT id FROM t", 0))
					run(fixture.Exec("CREATE VIEW outer_view AS SELECT id FROM inner_view", 0))
				})
				grant("GRANT SELECT ON t TO " + owner)
				grant("GRANT SELECT ON outer_view TO " + reader)
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				run(fixture.Exec("SET SESSION AUTHORIZATION "+reader, 0))
				denied := fixture.RoleReferenceError("SELECT id FROM outer_view", "42501", "permission denied for table t", "", prepared, "execute")
				denied.SelectRows = true
				run(denied)
				grant("GRANT SELECT ON t TO " + reader)
				run(fixture.Select("SELECT id FROM outer_view", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
				grant("REVOKE SELECT ON t FROM " + reader)
				run(denied)
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			}
		})
	}
}
