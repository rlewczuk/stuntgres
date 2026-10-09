package reference_test

import "testing"

func TestReferenceCompareOpsRightOnlyPublicAuthorization(t *testing.T) {
	owner, reader := "scalar_compare_owner", "scalar_compare_reader"
	roleReferenceCleanup(t, []string{owner, reader})
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			conn, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			defer func() {
				if _, err := conn.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION"); err != nil {
					t.Error(err)
				}
			}()
			run := func(step referenceStep) { scalarRunStep(t, p.ctx, conn, backend.recorder, step) }
			run(referenceExec("CREATE USER "+owner, 0))
			run(referenceExec("CREATE USER "+reader, 0))
			if backend.pg {
				if _, err := referenceAdmin.ExecContext(p.ctx, "GRANT USAGE,CREATE ON SCHEMA public TO "+owner); err != nil {
					t.Fatal(err)
				}
				if _, err := referenceAdmin.ExecContext(p.ctx, "GRANT USAGE ON SCHEMA public TO "+reader); err != nil {
					t.Fatal(err)
				}
			}
			run(referenceExec("SET SESSION AUTHORIZATION "+owner, 0))
			run(referenceExec("CREATE TABLE secret(i INT)", 0))
			run(referenceExec("INSERT INTO secret VALUES(1)", 1))
			q := "SELECT 0<i FROM secret"
			held, err := conn.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			run(referenceExec("SET SESSION AUTHORIZATION "+reader, 0))
			for _, query := range []string{q, "SELECT 7 FROM secret WHERE 0<i"} {
				step := referenceImplicitError(query, referenceDiagnostic{Code: "42501", Message: "permission denied for table secret"})
				step.selectRows = true
				step.errorStage = "execute"
				run(step)
			}
			step := referenceImplicitError(q, referenceDiagnostic{Code: "42501", Message: "permission denied for table secret"})
			step.selectRows = true
			step.errorStage = "execute"
			scalarRunStep(t, p.ctx, referenceHeldStatement{conn, held, q}, backend.recorder, step)
		})
	}
}
