package reference_test

import (
	"fmt"
	"testing"
)

func TestReferenceViewOriginalInvokerControls(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			owner, reader := fmt.Sprintf("view_owner_%v", prepared), fmt.Sprintf("view_reader_%v", prepared)
			roleReferenceCleanup(t, []string{reader, owner})
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
			c, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(step referenceStep) {
				step.prepared = prepared
				referenceRunStep(t, p.ctx, c, backend.recorder, step)
			}
			for _, query := range []string{"CREATE USER " + owner, "CREATE USER " + reader, "CREATE TABLE t(id INT)", "INSERT INTO t VALUES(7)"} {
				count := int64(0)
				if query == "INSERT INTO t VALUES(7)" {
					count = 1
				}
				run(referenceExec(query, count))
			}
			grant := func(query string) {
				if _, err := backend.db.ExecContext(p.ctx, query); err != nil {
					t.Fatal(err)
				}
			}
			if backend.pg { grant("GRANT USAGE, CREATE ON SCHEMA public TO PUBLIC") }
			run(referenceExec("SET SESSION AUTHORIZATION "+owner, 0))
			run(referenceExec("CREATE VIEW inner_view WITH(security_invoker=true) AS SELECT id FROM t", 0))
			run(referenceExec("CREATE VIEW outer_view AS SELECT id FROM inner_view", 0))
			grant("GRANT SELECT ON t TO " + owner)
			grant("GRANT SELECT ON outer_view TO " + reader)
			run(referenceExec("RESET SESSION AUTHORIZATION", 0))
			run(referenceExec("SET SESSION AUTHORIZATION "+reader, 0))
			denied := roleReferenceError("SELECT id FROM outer_view", "42501", "permission denied for table t", "", prepared, "execute")
			denied.selectRows = true
			run(denied)
			grant("GRANT SELECT ON t TO " + reader)
			run(referenceSelect("SELECT id FROM outer_view", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
			grant("REVOKE SELECT ON t FROM " + reader)
			run(denied)
			run(referenceExec("RESET SESSION AUTHORIZATION", 0))
			}
		})
	}
}
