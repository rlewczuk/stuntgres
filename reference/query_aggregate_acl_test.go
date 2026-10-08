//go:build reference

package reference_test

import "testing"

// Public GRANT remains an explicit PostgreSQL-only boundary. Native column ACL
// manifests are covered through the existing engine authority in package units.
func TestReferenceCountExpressionPostgresColumnACL(t *testing.T) {
	p := newReferencePair(t)
	run := func(step referenceStep) { referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, step) }
	run(referenceExec("CREATE ROLE aggregate_reader", 0))
	run(referenceExec("CREATE TABLE t(k TEXT,v INT)", 0))
	t.Cleanup(func() {
		referenceAdmin.ExecContext(p.ctx, "DROP OWNED BY aggregate_reader CASCADE; DROP ROLE aggregate_reader")
	})
	if _, err := p.postgres.ExecContext(p.ctx, "GRANT USAGE ON SCHEMA public TO aggregate_reader"); err != nil {
		t.Fatal(err)
	}
	conn, err := p.postgres.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	defer conn.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION")
	referenceRunStep(t, p.ctx, conn, p.postgresRecord, referenceExec("SET SESSION AUTHORIZATION aggregate_reader", 0))
	denied := referenceImplicitError("SELECT count(*) FROM t", referenceDiagnostic{Code: "42501", Message: "permission denied for table t"})
	denied.selectRows = true
	referenceRunStep(t, p.ctx, conn, p.postgresRecord, denied)
	if _, err := referenceAdmin.ExecContext(p.ctx, "GRANT SELECT(v) ON t TO aggregate_reader"); err != nil {
		t.Fatal(err)
	}
	success := referenceSelect("SELECT count(*) FROM t", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}})
	referenceRunStep(t, p.ctx, conn, p.postgresRecord, success)
	referenceRunStep(t, p.ctx, conn, p.postgresRecord, referencePrepared(success))
	expression := referenceSelect("SELECT count(v) FROM t", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}})
	referenceRunStep(t, p.ctx, conn, p.postgresRecord, expression)
	referenceRunStep(t, p.ctx, conn, p.postgresRecord, referencePrepared(expression))
	for _, q := range []string{"SELECT count(k) FROM t", "SELECT count(k) FROM t WHERE FALSE", "SELECT count(v) FROM t HAVING count(k)>0", "SELECT count(v) FROM t ORDER BY count(k)", "SELECT count(*) FROM t GROUP BY k", "SELECT count(*) FROM t GROUP BY k HAVING k IS NULL", "SELECT count(*) FROM t GROUP BY k ORDER BY k"} {
		step := referenceImplicitError(q, referenceDiagnostic{Code: "42501", Message: "permission denied for table t"})
		step.selectRows = true
		referenceRunStep(t, p.ctx, conn, p.postgresRecord, step)
		referenceRunStep(t, p.ctx, conn, p.postgresRecord, referencePrepared(step))
	}
}
