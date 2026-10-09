//go:build reference

package reference_test

import "testing"

func TestReferenceRowLockAuthorizationControl(t *testing.T) {
	p := newReferencePair(t)
	// Native ACL mutation has an internal catalog API, not public GRANT syntax.
	// Independently assert the PostgreSQL column-ACL rule; engine tests exercise
	// the same authorization manifests through the native catalog authority.
	record := p.postgresRecord
	conn, err := p.postgres.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	referenceRunStep(t, p.ctx, conn, record, referenceExec("CREATE TABLE t(id INT,v INT)", 0))
	referenceRunStep(t, p.ctx, conn, record, referenceExec("INSERT INTO t VALUES(1,10)", 1))
	referenceRunStep(t, p.ctx, conn, record, referenceExec("CREATE ROLE row_lock_reader", 0))
	t.Cleanup(func() { referenceAdmin.Exec("DROP OWNED BY row_lock_reader; DROP ROLE row_lock_reader") })
	referenceRunStep(t, p.ctx, conn, record, referenceExec("GRANT USAGE ON SCHEMA public TO row_lock_reader", 0))
	referenceRunStep(t, p.ctx, conn, record, referenceExec("GRANT SELECT(id) ON t TO row_lock_reader", 0))
	referenceRunStep(t, p.ctx, conn, record, referenceExec("SET SESSION AUTHORIZATION row_lock_reader", 0))
	defer conn.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION")
	for _, clause := range referenceRowClauses {
		step := referenceImplicitError("SELECT id FROM t "+clause, referenceDiagnostic{Code: "42501", Message: "permission denied for table t"})
		step.selectRows = true
		referenceRunStep(t, p.ctx, conn, record, step)
	}
	referenceRunStep(t, p.ctx, conn, record, referenceExec("RESET SESSION AUTHORIZATION", 0))
	referenceRunStep(t, p.ctx, conn, record, referenceExec("GRANT UPDATE(v) ON t TO row_lock_reader", 0))
	referenceRunStep(t, p.ctx, conn, record, referenceExec("SET SESSION AUTHORIZATION row_lock_reader", 0))
	for _, clause := range referenceRowClauses {
		for _, prepared := range []bool{false, true} {
			step := referenceSelect("SELECT id FROM t "+clause, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}})
			if prepared {
				step = referencePrepared(step)
			}
			referenceRunStep(t, p.ctx, conn, record, step)
		}
	}
}

func TestReferenceRowLockSequenceAuthorizationControl(t *testing.T) {
	p := newReferencePair(t)
	conn, err := p.postgres.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	record := p.postgresRecord
	referenceRunStep(t, p.ctx, conn, record, referenceExec("CREATE SEQUENCE s", 0))
	referenceRunStep(t, p.ctx, conn, record, referenceExec("CREATE ROLE row_lock_reader", 0))
	t.Cleanup(func() { referenceAdmin.Exec("DROP OWNED BY row_lock_reader; DROP ROLE row_lock_reader") })
	referenceRunStep(t, p.ctx, conn, record, referenceExec("GRANT USAGE ON SCHEMA public TO row_lock_reader", 0))
	referenceRunStep(t, p.ctx, conn, record, referenceExec("GRANT SELECT ON SEQUENCE s TO row_lock_reader", 0))
	referenceRunStep(t, p.ctx, conn, record, referenceExec("SET SESSION AUTHORIZATION row_lock_reader", 0))
	defer conn.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION")
	for _, clause := range referenceRowClauses {
		for _, prepared := range []bool{false, true} {
			step := referenceImplicitError("SELECT last_value FROM s "+clause, referenceDiagnostic{Code: "42501", Message: "permission denied for sequence s"})
			step.selectRows = true
			step.errorStage = "execute"
			if prepared {
				step = referencePrepared(step)
			}
			referenceRunStep(t, p.ctx, conn, record, step)
		}
	}
	referenceRunStep(t, p.ctx, conn, record, referenceExec("RESET SESSION AUTHORIZATION", 0))
	referenceRunStep(t, p.ctx, conn, record, referenceExec("GRANT UPDATE ON SEQUENCE s TO row_lock_reader", 0))
	referenceRunStep(t, p.ctx, conn, record, referenceExec("SET SESSION AUTHORIZATION row_lock_reader", 0))
	for _, clause := range referenceRowClauses {
		for _, prepared := range []bool{false, true} {
			step := referenceImplicitError("SELECT last_value FROM s "+clause, referenceDiagnostic{Code: "42809", Message: `cannot lock rows in sequence "s"`})
			step.selectRows = true
			step.errorStage = "execute"
			if prepared {
				step = referencePrepared(step)
			}
			referenceRunStep(t, p.ctx, conn, record, step)
		}
	}
}
